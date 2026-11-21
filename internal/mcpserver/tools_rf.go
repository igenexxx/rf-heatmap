package mcpserver

import (
	"encoding/json"
	"fmt"
	"math"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/ui"
)

const (
	defaultSweepCount         = 8
	maxSweepCount             = 30
	defaultRSSISamples        = 8
	maxRSSISamples            = 32
	defaultFallbackSquelchDBm = -78
	initialPeakDBm            = -130
)

type sweepBurstOut struct {
	Sweep     int     `json:"sweep"`
	Bin       int     `json:"bin"`
	FreqHz    uint32  `json:"freq_hz"`
	RSSIDBm   int32   `json:"rssi_dbm"`
	MeanDBm   float64 `json:"mean_dbm"`
	StdDBm    float64 `json:"std_dbm"`
	ZScore    float64 `json:"z_score"`
	Threshold int32   `json:"threshold_dbm"`
}

type sweepBinOut struct {
	Bin            int     `json:"bin"`
	FreqHz         uint32  `json:"freq_hz"`
	LastRSSIDBm    int32   `json:"last_rssi_dbm"`
	PeakRSSIDBm    int32   `json:"peak_rssi_dbm"`
	MeanDBm        float64 `json:"mean_dbm"`
	StdDBm         float64 `json:"std_dbm"`
	SmartThreshold int32   `json:"smart_threshold_dbm"`
}

func (s *Server) toolGetStatus() (*toolCallResult, *rpcError) {
	mode, connected, chip := s.radioStatusSummary()
	return jsonResult(map[string]any{
		"mode":               mode,
		"hardware_connected": connected,
		"chip":               chip,
		"captures_dir":       s.capturesDir,
		"config_path":        s.configPath,
		"settings":           s.settings,
		"bands":              buildBandSummaries(),
		"presets":            buildPresetSummaries(),
	})
}

func (s *Server) radioStatusSummary() (string, bool, map[string]string) {
	s.radioMu.Lock()
	defer s.radioMu.Unlock()
	if s.radio == nil {
		return "sim", false, nil
	}
	info := s.radio.Info()
	return "hardware", true, map[string]string{
		"part_number": fmt.Sprintf("0x%02X", info.PartNumber),
		"version":     fmt.Sprintf("0x%02X", info.Version),
	}
}

func buildBandSummaries() []map[string]any {
	bands := make([]map[string]any, len(ui.DefaultBands))
	for i, b := range ui.DefaultBands {
		var minF, maxF uint32
		if len(b.FreqsHz) > 0 {
			minF, maxF = b.FreqsHz[0], b.FreqsHz[len(b.FreqsHz)-1]
		}
		bands[i] = map[string]any{
			"index":       i,
			"name":        b.Name,
			"bins":        len(b.FreqsHz),
			"min_freq_hz": minF,
			"max_freq_hz": maxF,
		}
	}
	return bands
}

func buildPresetSummaries() []map[string]string {
	presets := make([]map[string]string, cc1101.PresetCount())
	for i := range cc1101.PresetCount() {
		spec := cc1101.GetPresetSpec(cc1101.Preset(i))
		presets[i] = map[string]string{
			"name":         spec.Name,
			"subfile_name": spec.SubFileName,
		}
	}
	return presets
}

type sweepBandParams struct {
	bandIdx  int
	sweeps   int
	squelchK float64
}

func (s *Server) parseSweepBandParams(rawArgs json.RawMessage) (sweepBandParams, *toolCallResult, *rpcError) {
	var args struct {
		Band     int      `json:"band"`
		Sweeps   *int     `json:"sweeps"`
		SquelchK *float64 `json:"squelch_k"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return sweepBandParams{}, nil, err
	}
	if args.Band < 0 || args.Band >= len(ui.DefaultBands) {
		res, rerr := toolError(fmt.Sprintf("band %d out of range [0..%d]", args.Band, len(ui.DefaultBands)-1))
		return sweepBandParams{}, res, rerr
	}
	sweeps := defaultSweepCount
	if args.Sweeps != nil {
		sweeps = *args.Sweeps
	}
	if sweeps < 1 || sweeps > maxSweepCount {
		res, rerr := toolError(fmt.Sprintf("sweeps %d out of range [1..%d]", sweeps, maxSweepCount))
		return sweepBandParams{}, res, rerr
	}
	sqK := s.settings.SquelchK
	if args.SquelchK != nil {
		if math.IsNaN(*args.SquelchK) || math.IsInf(*args.SquelchK, 0) {
			res, rerr := toolError("squelch_k must be a finite number")
			return sweepBandParams{}, res, rerr
		}
		sqK = *args.SquelchK
	}
	return sweepBandParams{bandIdx: args.Band, sweeps: sweeps, squelchK: sqK}, nil, nil
}

func (s *Server) toolSweepBand(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	p, errRes, rpcErr := s.parseSweepBandParams(rawArgs)
	if errRes != nil || rpcErr != nil {
		return errRes, rpcErr
	}
	band := ui.DefaultBands[p.bandIdx]
	blCfg := rfml.DefaultBaselineConfig(len(band.FreqsHz))
	blCfg.SquelchK = p.squelchK
	bl := rfml.NewBinBaseline(blCfg)

	sweep, peak, bursts, err := s.runSweepLoop(band.FreqsHz, p.sweeps, bl)
	if err != nil {
		return toolError(err.Error())
	}
	bins, noiseFloor, smartThr := summarizeSweepBins(band.FreqsHz, sweep, peak, bl)
	return jsonResult(map[string]any{
		"band_index":      p.bandIdx,
		"band_name":       band.Name,
		"sweeps":          p.sweeps,
		"squelch_k":       bl.SquelchK(),
		"ready":           bl.Ready(0),
		"bins":            bins,
		"noise_floor":     noiseFloor,
		"smart_threshold": smartThr,
		"bursts":          bursts,
	})
}

func (s *Server) runSweepLoop(freqs []uint32, sweeps int, bl *rfml.BinBaseline) ([]int32, []int32, []sweepBurstOut, error) {
	n := len(freqs)
	sweep := make([]int32, n)
	peak := make([]int32, n)
	for i := range peak {
		peak[i] = initialPeakDBm
	}
	bursts := make([]sweepBurstOut, 0)

	s.radioMu.Lock()
	defer func() {
		if s.radio != nil {
			_ = s.radio.Idle()
		}
		s.radioMu.Unlock()
	}()

	for sw := 1; sw <= sweeps; sw++ {
		for i, freq := range freqs {
			rssi, err := s.sampleFreqLocked(freq, sw, i, n)
			if err != nil {
				return nil, nil, nil, fmt.Errorf("radio sweep failed at %d Hz: %w", freq, err)
			}
			sweep[i] = rssi
			peak[i] = max(peak[i], rssi)
		}
		bl.Update(sweep)
		if evt, ok := bl.DetectBurst(sweep); ok {
			bursts = append(bursts, sweepBurstOut{
				Sweep: sw, Bin: evt.Bin, FreqHz: freqs[evt.Bin],
				RSSIDBm: evt.RSSIDBm, MeanDBm: round1(evt.MeanDBm),
				StdDBm: round1(evt.StdDBm), ZScore: round1(evt.ZScore), Threshold: evt.Threshold,
			})
		}
	}
	return sweep, peak, bursts, nil
}

func summarizeSweepBins(freqs []uint32, sweep, peak []int32, bl *rfml.BinBaseline) ([]sweepBinOut, map[string]float64, map[string]int32) {
	n := len(freqs)
	bins := make([]sweepBinOut, n)
	minMean, maxMean, sumMean := 999.0, -999.0, 0.0
	var minThr, maxThr int32 = 999, -999
	for i := range n {
		m := bl.MeanDBm(i)
		thr := bl.SmartThreshold(i, defaultFallbackSquelchDBm)
		sumMean += m
		minMean = min(minMean, m)
		maxMean = max(maxMean, m)
		minThr = min(minThr, thr)
		maxThr = max(maxThr, thr)
		bins[i] = sweepBinOut{
			Bin: i, FreqHz: freqs[i], LastRSSIDBm: sweep[i], PeakRSSIDBm: peak[i],
			MeanDBm: round1(m), StdDBm: round1(bl.StdDBm(i)), SmartThreshold: thr,
		}
	}
	noiseFloor := map[string]float64{"min_dbm": round1(minMean), "avg_dbm": round1(sumMean / float64(n)), "max_dbm": round1(maxMean)}
	smartThr := map[string]int32{"min_dbm": minThr, "max_dbm": maxThr}
	return bins, noiseFloor, smartThr
}

func parsePresetOrDefault(name string) (cc1101.Preset, error) {
	if name == "" {
		return cc1101.PresetAM650, nil
	}
	p, ok := cc1101.PresetFromSubName(name)
	if !ok {
		return 0, fmt.Errorf("unknown preset %q (use AM650, AM270, FM238, FM476)", name)
	}
	return p, nil
}

func (s *Server) toolReadRSSI(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		FreqHz  uint32 `json:"freq_hz"`
		Preset  string `json:"preset"`
		Samples *int   `json:"samples"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if args.FreqHz < minFreqHz || args.FreqHz > maxFreqHz {
		return toolError(fmt.Sprintf("freq_hz %d out of range [%d..%d]", args.FreqHz, minFreqHz, maxFreqHz))
	}
	preset, err := parsePresetOrDefault(args.Preset)
	if err != nil {
		return toolError(err.Error())
	}
	count := defaultRSSISamples
	if args.Samples != nil {
		count = *args.Samples
	}
	if count < 1 || count > maxRSSISamples {
		return toolError(fmt.Sprintf("samples %d out of range [1..%d]", count, maxRSSISamples))
	}

	blCfg := rfml.DefaultBaselineConfig(1)
	blCfg.SquelchK = s.settings.SquelchK
	bl := rfml.NewBinBaseline(blCfg)

	samples, minV, maxV, err := s.collectRSSISamples(args.FreqHz, preset, count, bl)
	if err != nil {
		return toolError(err.Error())
	}
	return jsonResult(map[string]any{
		"freq_hz": args.FreqHz, "preset": cc1101.GetPresetSpec(preset).Name,
		"samples_count": count, "samples_dbm": samples,
		"min_dbm": minV, "max_dbm": maxV, "last_dbm": samples[count-1],
		"mean_dbm": round1(bl.MeanDBm(0)), "std_dbm": round1(bl.StdDBm(0)),
		"smart_threshold_dbm": bl.SmartThreshold(0, defaultFallbackSquelchDBm), "ready": bl.Ready(0),
	})
}

func (s *Server) collectRSSISamples(freqHz uint32, preset cc1101.Preset, count int, bl *rfml.BinBaseline) ([]int32, int32, int32, error) {
	samples := make([]int32, count)
	var minV, maxV int32 = 999, -999

	s.radioMu.Lock()
	defer func() {
		if s.radio != nil {
			_ = s.radio.Idle()
		}
		s.radioMu.Unlock()
	}()

	if s.radio != nil {
		if err := s.radio.ApplyPreset(preset); err != nil {
			return nil, 0, 0, fmt.Errorf("apply preset failed: %w", err)
		}
	}
	for i := range count {
		rssi, err := s.sampleFreqLocked(freqHz, i+1, 0, 1)
		if err != nil {
			return nil, 0, 0, fmt.Errorf("read rssi failed: %w", err)
		}
		samples[i] = rssi
		bl.UpdateBin(0, rssi)
		minV = min(minV, rssi)
		maxV = max(maxV, rssi)
	}
	return samples, minV, maxV, nil
}
