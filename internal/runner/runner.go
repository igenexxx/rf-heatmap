package runner

import (
	"context"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/config"
	"rf-heatmap/internal/mcpserver"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/ui"
)

const (
	defaultSPISpeedHz    = 500_000
	defaultProbeSweeps   = 10
	probeSettleDelay     = 1200 * time.Microsecond
	probeFallbackRSSIDBm = -106
	probeFallbackSqDBm   = -78
	uiFrameInterval      = 45 * time.Millisecond
)

// Config holds runtime parameters for rf-heatmap.
type Config struct {
	SimMode     bool
	ProbeOnly   bool
	ProbeBand   int
	ProbeSweeps int
	MCPMode     bool
	ConfigPath  string
	FBPath      string
	SPIPath     string
	CapturesDir string
	Stdin       io.Reader // defaults to os.Stdin when nil
}

// DefaultCapturesDir returns the default directory for saved .sub captures.
func DefaultCapturesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/tmp/rf-heatmap/captures"
	}
	return filepath.Join(home, "rf-heatmap", "captures")
}

// Run executes the MCP server, the CC1101 hardware probe, or the interactive UI loop.
func Run(ctx context.Context, cfg Config, stdout, stderr io.Writer) error {
	if cfg.MCPMode {
		return runMCP(ctx, cfg, stdout, stderr)
	}
	if cfg.ProbeOnly {
		return runProbe(cfg.SPIPath, cfg.ProbeBand, cfg.ProbeSweeps, stdout)
	}
	return runUI(ctx, cfg, stderr)
}

func openOptionalRadio(simMode bool, spiPath string, stderr io.Writer, label string) (*cc1101.Radio, error) {
	if simMode {
		return nil, nil
	}
	tr, err := cc1101.OpenLinuxTransport(spiPath, defaultSPISpeedHz)
	if err != nil {
		fmt.Fprintf(stderr, "rf-heatmap: %sSPI init failed: %v\n", label, err)
		return nil, err
	}
	r, err := cc1101.Open(tr, cc1101.DefaultCrystalHz)
	if err != nil {
		fmt.Fprintf(stderr, "rf-heatmap: %sCC1101 init failed: %v\n", label, err)
		_ = tr.Close()
		return nil, err
	}
	return r, nil
}

func runMCP(ctx context.Context, cfg Config, stdout, stderr io.Writer) error {
	cfgPath := cfg.ConfigPath
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	settings, err := config.Load(cfgPath)
	if err != nil {
		return fmt.Errorf("load settings: %w", err)
	}
	if !settings.MCPEnabled {
		return errors.New("MCP server is disabled. Enable it in RF Heatmap -> SET (Settings) first.")
	}

	var backend mcpserver.RadioBackend
	if r, _ := openOptionalRadio(cfg.SimMode, cfg.SPIPath, stderr, "MCP "); r != nil {
		defer r.Close()
		backend = mcpserver.NewCC1101Backend(r)
	}

	capturesDir := cfg.CapturesDir
	if capturesDir == "" {
		capturesDir = DefaultCapturesDir()
	}
	in := cfg.Stdin
	if in == nil {
		in = os.Stdin
	}
	srv := mcpserver.NewServer(mcpserver.Options{
		Radio:       backend,
		CapturesDir: capturesDir,
		ConfigPath:  cfgPath,
		Settings:    settings,
	})
	return srv.ServeStdio(ctx, in, stdout)
}

func runProbe(spiPath string, bandIdx, numSweeps int, out io.Writer) error {
	tr, err := cc1101.OpenLinuxTransport(spiPath, defaultSPISpeedHz)
	if err != nil {
		return fmt.Errorf("cc1101 transport: %w", err)
	}
	radio, err := cc1101.Open(tr, cc1101.DefaultCrystalHz)
	if err != nil {
		_ = tr.Close()
		return fmt.Errorf("cc1101 open: %w", err)
	}
	defer radio.Close()

	info := radio.Info()
	fmt.Fprintf(out, "CC1101 OK: partnum=0x%02X version=0x%02X\n", info.PartNumber, info.Version)

	if bandIdx < 0 || bandIdx >= len(ui.DefaultBands) {
		bandIdx = 0
	}
	if numSweeps <= 0 {
		numSweeps = defaultProbeSweeps
	}
	band := ui.DefaultBands[bandIdx]
	bl := rfml.NewBinBaseline(rfml.DefaultBaselineConfig(len(band.FreqsHz)))

	lastSweep, totalSweep, totalML, bursts := runProbeSweeps(radio, band.FreqsHz, numSweeps, bl, out)
	summarizeProbeBaseline(band, numSweeps, bl, totalSweep, totalML, bursts, out)
	verifyProbeBurstFreeze(bl, lastSweep, out)
	return nil
}

func runProbeSweeps(radio *cc1101.Radio, freqs []uint32, numSweeps int, bl *rfml.BinBaseline, out io.Writer) ([]int32, time.Duration, time.Duration, int) {
	sweep := make([]int32, len(freqs))
	var totalML, totalSweep time.Duration
	bursts := 0

	for s := 1; s <= numSweeps; s++ {
		t0 := time.Now()
		for i, freq := range freqs {
			rssi := int32(probeFallbackRSSIDBm)
			if err := radio.StartReceive(freq); err == nil {
				time.Sleep(probeSettleDelay)
				if v, err := radio.ReadRSSIDBm(); err == nil {
					rssi = v
				}
			}
			sweep[i] = rssi
		}
		totalSweep += time.Since(t0)

		tML := time.Now()
		bl.Update(sweep)
		evt, ok := bl.DetectBurst(sweep)
		totalML += time.Since(tML)
		if ok {
			bursts++
			fmt.Fprintf(out, "sweep %2d: BURST bin=%02d freq=%.2fMHz rssi=%ddBm mean=%.1fdBm Z=+%.1f thr=%ddBm\n",
				s, evt.Bin, float64(freqs[evt.Bin])/1e6, evt.RSSIDBm, evt.MeanDBm, evt.ZScore, evt.Threshold)
		}
	}
	return sweep, totalSweep, totalML, bursts
}

func summarizeProbeBaseline(band ui.BandPreset, numSweeps int, bl *rfml.BinBaseline, totalSweep, totalML time.Duration, bursts int, out io.Writer) {
	n := len(band.FreqsHz)
	minMean, maxMean, sumMean := 999.0, -999.0, 0.0
	var minThr, maxThr int32 = 999, -999
	for i := range n {
		m := bl.MeanDBm(i)
		thr := bl.SmartThreshold(i, probeFallbackSqDBm)
		sumMean += m
		minMean = min(minMean, m)
		maxMean = max(maxMean, m)
		minThr = min(minThr, thr)
		maxThr = max(maxThr, thr)
	}
	fmt.Fprintf(out, "Baseline (%s, %d bins, %d sweeps): ready=%v\n", band.Name, n, numSweeps, bl.Ready(0))
	fmt.Fprintf(out, "  Noise floor : min=%.1f dBm  avg=%.1f dBm  max=%.1f dBm\n", minMean, sumMean/float64(n), maxMean)
	fmt.Fprintf(out, "  SmartSquelch: [%d .. %d] dBm (k=%.1f)\n", minThr, maxThr, bl.SquelchK())
	fmt.Fprintf(out, "  Avg latency : sweep=%v  rfml=%v\n", totalSweep/time.Duration(numSweeps), totalML/time.Duration(numSweeps))
	fmt.Fprintf(out, "  Live bursts : %d detected\n", bursts)
}

func verifyProbeBurstFreeze(bl *rfml.BinBaseline, lastSweep []int32, out io.Writer) {
	checkBin := min(18, len(lastSweep)-1)
	meanBefore := bl.MeanDBm(checkBin)
	testSweep := append([]int32(nil), lastSweep...)
	testSweep[checkBin] = int32(math.Round(meanBefore)) + 30
	evt, ok := bl.DetectBurst(testSweep)
	bl.Update(testSweep)
	meanAfter := bl.MeanDBm(checkBin)
	fmt.Fprintf(out, "  Self-check (+30dB on bin %d): detected=%v bin=%d Z=+%.1f freeze_drift=%.3fdB\n",
		checkBin, ok, evt.Bin, evt.ZScore, math.Abs(meanAfter-meanBefore))
}

func runUI(ctx context.Context, cfg Config, stderr io.Writer) error {
	canvas, err := ui.NewCanvas(cfg.FBPath)
	if err != nil {
		return err
	}
	defer canvas.Close()

	radio, hwErr := openOptionalRadio(cfg.SimMode, cfg.SPIPath, stderr, "")
	if radio != nil {
		defer radio.Close()
	}

	keys, stopInput := ui.StartEvdevReader()
	defer stopInput()

	cfgPath := cfg.ConfigPath
	if cfgPath == "" {
		cfgPath = config.DefaultPath()
	}
	settings, err := config.Load(cfgPath)
	if err != nil {
		fmt.Fprintf(stderr, "rf-heatmap: config load failed (using defaults): %v\n", err)
		settings = config.DefaultSettings()
	}

	app := ui.NewApp(radio, cfg.CapturesDir)
	app.SetConfigPath(cfgPath)
	app.ApplySettings(settings)
	setStartupToast(app, radio, hwErr)

	return runUIEventLoop(ctx, app, canvas, keys)
}

func setStartupToast(app *ui.App, radio *cc1101.Radio, hwErr error) {
	if radio != nil {
		app.SetToast(fmt.Sprintf("CC1101 v0x%02X Ready", radio.Info().Version))
	} else if hwErr != nil {
		app.SetToast("SIM Mode (CC1101 err)")
	} else {
		app.SetToast("SIM Mode (No CC1101)")
	}
}

func runUIEventLoop(ctx context.Context, app *ui.App, canvas *ui.Canvas, keys <-chan ui.KeyAction) error {
	ticker := time.NewTicker(uiFrameInterval)
	defer ticker.Stop()

	app.Render(canvas)
	_ = canvas.Flush()

	for {
		select {
		case <-ctx.Done():
			return nil
		case act := <-keys:
			if app.HandleKey(act) {
				return nil
			}
			app.Render(canvas)
			_ = canvas.Flush()
		case <-ticker.C:
			app.Step()
			app.Render(canvas)
			_ = canvas.Flush()
		}
	}
}
