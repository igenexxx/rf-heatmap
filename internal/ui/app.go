package ui

import (
	"fmt"
	"math"
	"math/rand"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/subfile"
)

// Mode represents the active screen tab.
type Mode int

const (
	ModeHeatmap Mode = iota
	ModePulse
	ModeDecode
	ModeSaved
)

func (m Mode) String() string {
	switch m {
	case ModeHeatmap:
		return "HEATMAP"
	case ModePulse:
		return "PULSE"
	case ModeDecode:
		return "DECODE"
	case ModeSaved:
		return "SAVED"
	default:
		return "UNKNOWN"
	}
}

// BandPreset defines a frequency sweep range for the Heatmap.
type BandPreset struct {
	Name    string
	FreqsHz []uint32
}

// DefaultBands provides practical ISM Sub-GHz sweep bands.
var DefaultBands = []BandPreset{
	{Name: "433 ISM", FreqsHz: linspaceHz(433_050_000, 434_790_000, 36)},
	{Name: "868 EU", FreqsHz: linspaceHz(868_000_000, 869_800_000, 36)},
	{Name: "315 US", FreqsHz: linspaceHz(314_500_000, 315_500_000, 36)},
	{Name: "915 ISM", FreqsHz: linspaceHz(902_000_000, 928_000_000, 36)},
	{
		Name: "ALL ISM",
		FreqsHz: []uint32{
			300_000_000, 303_875_000, 310_000_000, 315_000_000, 318_000_000, 330_000_000,
			345_000_000, 390_000_000, 418_000_000, 433_075_000, 433_420_000, 433_920_000,
			434_420_000, 434_775_000, 438_900_000, 464_000_000, 868_000_000, 868_350_000,
			868_800_000, 869_525_000, 902_500_000, 915_000_000, 920_000_000, 925_000_000,
		},
	},
}

func linspaceHz(start, end uint32, n int) []uint32 {
	if n <= 1 {
		return []uint32{start}
	}
	out := make([]uint32, n)
	step := float64(end-start) / float64(n-1)
	for i := 0; i < n; i++ {
		out[i] = start + uint32(math.Round(float64(i)*step))
	}
	return out
}

const (
	waterfallRows = 56
	maxCols       = 36
)

// SavedEntry represents a .sub file on disk.
type SavedEntry struct {
	Name string
	Path string
	File *subfile.SubFile
}

// App owns all UI state, heatmap history, captures, and radio interaction.
type App struct {
	Radio       *cc1101.Radio // nil when running in simulator mode
	SimMode     bool
	CapturesDir string

	Mode       Mode
	BandIdx    int
	Preset     cc1101.Preset
	ActiveFreq uint32
	CursorCol  int
	SquelchDBm int32
	Paused     bool
	ShowHelp   bool
	HelpPage   int

	// Heatmap state.
	SweepRSSI  [maxCols]int32
	PeakHold   [maxCols]int32
	Waterfall  [waterfallRows][maxCols]int32
	WfHead     int
	PeakFreqHz uint32
	PeakDBm    int32

	// Pulse / Decode state.
	LiveRSSI     int32
	RSSIHistory  [64]int32
	RssiHistHead int
	Recording    bool
	CapturedRaw  []int32
	LastDecoded  *subfile.DecodedKey
	DecodedLog   []subfile.DecodedKey

	// Saved files state.
	SavedFiles []SavedEntry
	SavedIdx   int

	// Toast notification.
	ToastText  string
	ToastUntil time.Time

	tick uint64
}

// NewApp initializes the application state.
func NewApp(radio *cc1101.Radio, capturesDir string) *App {
	a := &App{
		Radio:       radio,
		SimMode:     radio == nil,
		CapturesDir: capturesDir,
		Mode:        ModeHeatmap,
		BandIdx:     0,
		Preset:      cc1101.PresetAM650,
		ActiveFreq:  433_920_000,
		CursorCol:   18,
		SquelchDBm:  -78,
		PeakDBm:     -115,
		PeakFreqHz:  433_920_000,
		LiveRSSI:    -105,
	}
	for i := range a.SweepRSSI {
		a.SweepRSSI[i] = -108
		a.PeakHold[i] = -108
	}
	for r := range a.Waterfall {
		for c := range a.Waterfall[r] {
			a.Waterfall[r][c] = -110
		}
	}
	for i := range a.RSSIHistory {
		a.RSSIHistory[i] = -108
	}
	a.refreshSavedFiles()
	return a
}

// SetToast displays a brief status message.
func (a *App) SetToast(msg string) {
	a.ToastText = msg
	a.ToastUntil = time.Now().Add(2 * time.Second)
}

// HandleKey processes a single KeyAction and returns true if the app should exit.
func (a *App) HandleKey(act KeyAction) bool {
	if act == KeyHelp {
		a.ShowHelp = !a.ShowHelp
		a.HelpPage = 0
		return false
	}
	if a.ShowHelp {
		switch act {
		case KeyLeft, KeyUp:
			a.HelpPage = (a.HelpPage + 3) % 4
		case KeyRight, KeyDown, KeySpace, KeyTab, KeyNav5:
			a.HelpPage = (a.HelpPage + 1) % 4
		case KeyQuit, KeyEnter, KeyNav4, KeyNav8:
			a.ShowHelp = false
		}
		return false
	}

	switch act {
	case KeyQuit:
		return true
	case KeyTab:
		a.Mode = (a.Mode + 1) % 4
		if a.Mode == ModeSaved {
			a.refreshSavedFiles()
		}
	case KeyBand:
		a.cycleBand()
	case KeyMod:
		a.cyclePreset()
	case KeyNav8:
		a.Mode = (a.Mode + 1) % 4
		if a.Mode == ModeSaved {
			a.refreshSavedFiles()
		}
	}

	switch a.Mode {
	case ModeHeatmap:
		a.handleHeatmapKey(act)
	case ModePulse:
		a.handlePulseKey(act)
	case ModeDecode:
		a.handleDecodeKey(act)
	case ModeSaved:
		a.handleSavedKey(act)
	}
	return false
}

func (a *App) cycleBand() {
	a.BandIdx = (a.BandIdx + 1) % len(DefaultBands)
	for i := range a.PeakHold {
		a.PeakHold[i] = -108
	}
	freqs := DefaultBands[a.BandIdx].FreqsHz
	if a.CursorCol >= len(freqs) {
		a.CursorCol = len(freqs) / 2
	}
	a.ActiveFreq = freqs[a.CursorCol]
	a.SetToast("Band: " + DefaultBands[a.BandIdx].Name)
}

func (a *App) cyclePreset() {
	a.Preset = cc1101.Preset((int(a.Preset) + 1) % cc1101.PresetCount())
	if a.Radio != nil {
		_ = a.Radio.ApplyPreset(a.Preset)
	}
	a.SetToast("Mod: " + cc1101.GetPresetSpec(a.Preset).Name)
}

func (a *App) handleHeatmapKey(act KeyAction) {
	freqs := DefaultBands[a.BandIdx].FreqsHz
	n := len(freqs)
	switch act {
	case KeyLeft:
		if a.CursorCol > 0 {
			a.CursorCol--
		}
		a.ActiveFreq = freqs[a.CursorCol]
	case KeyRight:
		if a.CursorCol < n-1 {
			a.CursorCol++
		}
		a.ActiveFreq = freqs[a.CursorCol]
	case KeyUp, KeyNav5:
		a.cycleBand()
	case KeyDown:
		for i := range a.PeakHold {
			a.PeakHold[i] = -108
		}
		a.PeakDBm = -110
		a.SetToast("Peaks cleared")
	case KeyNav4, KeySpace:
		a.Paused = !a.Paused
	case KeyNav6, KeyEnter:
		// Lock onto peak or cursor frequency and jump to Pulse view.
		if a.PeakDBm > -85 {
			a.ActiveFreq = a.PeakFreqHz
		} else {
			a.ActiveFreq = freqs[a.CursorCol]
		}
		a.Mode = ModePulse
		a.SetToast(fmt.Sprintf("Locked %.2f MHz", float64(a.ActiveFreq)/1e6))
	case KeyNav7:
		a.cyclePreset()
	}
}

func (a *App) handlePulseKey(act KeyAction) {
	switch act {
	case KeyUp:
		if a.SquelchDBm < -30 {
			a.SquelchDBm += 2
		}
	case KeyDown:
		if a.SquelchDBm > -105 {
			a.SquelchDBm -= 2
		}
	case KeyLeft:
		a.ActiveFreq -= 50_000
	case KeyRight:
		a.ActiveFreq += 50_000
	case KeyNav4, KeySpace:
		a.Recording = !a.Recording
		if a.Recording {
			a.CapturedRaw = a.CapturedRaw[:0]
			a.SetToast("Recording RAW...")
		} else {
			a.SetToast(fmt.Sprintf("Captured %d pulses", len(a.CapturedRaw)))
		}
	case KeyNav5, KeyEnter:
		a.triggerReplay()
	case KeyNav6:
		a.saveCurrentRaw()
	case KeyNav7:
		a.cyclePreset()
	}
}

func (a *App) handleDecodeKey(act KeyAction) {
	switch act {
	case KeyNav4, KeySpace:
		a.Paused = !a.Paused
	case KeyNav5, KeyEnter:
		if a.LastDecoded != nil {
			a.SetToast("TX " + a.LastDecoded.Protocol)
		} else {
			a.SetToast("No decoded key yet")
		}
	case KeyNav6:
		a.saveDecodedKey()
	case KeyNav7:
		a.DecodedLog = nil
		a.LastDecoded = nil
		a.SetToast("Log cleared")
	}
}

func (a *App) handleSavedKey(act KeyAction) {
	switch act {
	case KeyUp:
		if a.SavedIdx > 0 {
			a.SavedIdx--
		}
	case KeyDown:
		if a.SavedIdx < len(a.SavedFiles)-1 {
			a.SavedIdx++
		}
	case KeyNav4, KeyEnter:
		if len(a.SavedFiles) > 0 {
			entry := a.SavedFiles[a.SavedIdx]
			a.ActiveFreq = entry.File.FreqHz
			a.SetToast("TX: " + entry.Name)
		}
	case KeyNav5:
		if len(a.SavedFiles) > 0 {
			entry := a.SavedFiles[a.SavedIdx]
			a.ActiveFreq = entry.File.FreqHz
			a.Mode = ModePulse
			a.SetToast("Loaded " + entry.Name)
		}
	case KeyNav6, KeyDelete:
		if len(a.SavedFiles) > 0 {
			entry := a.SavedFiles[a.SavedIdx]
			_ = os.Remove(entry.Path)
			a.refreshSavedFiles()
			a.SetToast("Deleted " + entry.Name)
		}
	case KeyNav7:
		a.refreshSavedFiles()
		a.SetToast("Refreshed")
	}
}

// Step advances one sampling/simulation tick.
func (a *App) Step() {
	a.tick++
	if a.Paused {
		return
	}
	switch a.Mode {
	case ModeHeatmap:
		a.stepHeatmap()
	case ModePulse, ModeDecode:
		a.stepLiveChannel()
	}
}

func (a *App) stepHeatmap() {
	freqs := DefaultBands[a.BandIdx].FreqsHz
	n := len(freqs)
	if n > maxCols {
		n = maxCols
	}

	var rowMax int32 = -120
	var rowMaxFreq uint32 = freqs[0]

	for i := 0; i < n; i++ {
		var rssi int32 = -106
		if a.Radio != nil {
			if err := a.Radio.StartReceive(freqs[i]); err == nil {
				time.Sleep(1200 * time.Microsecond)
				if v, err := a.Radio.ReadRSSIDBm(); err == nil {
					rssi = v
				}
			}
		} else {
			rssi = a.simSweepRSSI(freqs[i], i, n)
		}
		a.SweepRSSI[i] = rssi
		if rssi > a.PeakHold[i] {
			a.PeakHold[i] = rssi
		} else if a.tick%12 == 0 && a.PeakHold[i] > rssi {
			a.PeakHold[i]--
		}
		if rssi > rowMax {
			rowMax = rssi
			rowMaxFreq = freqs[i]
		}
		a.Waterfall[a.WfHead][i] = rssi
	}
	a.WfHead = (a.WfHead + 1) % waterfallRows
	if rowMax >= a.PeakDBm || a.tick%20 == 0 {
		a.PeakDBm = rowMax
		a.PeakFreqHz = rowMaxFreq
	}
}

func (a *App) stepLiveChannel() {
	if a.Radio != nil {
		_ = a.Radio.StartReceive(a.ActiveFreq)
		if v, err := a.Radio.ReadRSSIDBm(); err == nil {
			a.LiveRSSI = v
		}
	} else {
		// Realistic bursty OOK remote signal every ~18 ticks.
		base := int32(-104 + rand.Intn(5))
		if a.tick%18 < 4 {
			base = -54 + int32(rand.Intn(8))
			if a.Recording {
				a.CapturedRaw = append(a.CapturedRaw, 350, -1050, 1050, -350, 350, -1050)
			}
			if a.tick%18 == 1 {
				k := subfile.DecodedKey{
					Protocol: "Princeton",
					Bits:     24,
					KeyHex:   fmt.Sprintf("00 00 00 00 00 A5 5A %02X", uint8(a.tick)),
					TE:       350,
				}
				a.LastDecoded = &k
				a.DecodedLog = append([]subfile.DecodedKey{k}, a.DecodedLog...)
				if len(a.DecodedLog) > 6 {
					a.DecodedLog = a.DecodedLog[:6]
				}
			}
		}
		a.LiveRSSI = base
	}
	a.RSSIHistory[a.RssiHistHead] = a.LiveRSSI
	a.RssiHistHead = (a.RssiHistHead + 1) % len(a.RSSIHistory)
}

func (a *App) simSweepRSSI(freqHz uint32, idx, n int) int32 {
	noise := int32(-106 + rand.Intn(6))
	// Simulate realistic ISM signals: 433.92 MHz keyfob, 433.42 MHz Somfy, 868.35 MHz sensor.
	phase := float64(a.tick) * 0.25
	center := int(float64(n) * 0.5)
	if idx == center && (a.tick%14 < 5) {
		return -46 + int32(rand.Intn(6))
	}
	if idx == center-7 && math.Sin(phase) > 0.3 {
		return -62 + int32(rand.Intn(5))
	}
	if freqHz >= 868_200_000 && freqHz <= 868_500_000 && a.tick%10 < 4 {
		return -52 + int32(rand.Intn(6))
	}
	if idx == (int(a.tick/3)%n) && a.tick%7 == 0 {
		return -68 + int32(rand.Intn(8))
	}
	return noise
}

func (a *App) triggerReplay() {
	if len(a.CapturedRaw) == 0 && a.LastDecoded == nil {
		a.SetToast("Nothing to TX")
		return
	}
	a.SetToast(fmt.Sprintf("TX %.2f MHz", float64(a.ActiveFreq)/1e6))
}

func (a *App) saveCurrentRaw() {
	if len(a.CapturedRaw) == 0 {
		// Create a sample capture if none recorded yet in sim mode.
		a.CapturedRaw = []int32{-10500, 350, -1050, 1050, -350, 350, -1050, -10500}
	}
	if err := os.MkdirAll(a.CapturesDir, 0o755); err != nil {
		a.SetToast("Save err!")
		return
	}
	name := fmt.Sprintf("raw_%s.sub", time.Now().Format("150405"))
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File",
		Version:  1,
		FreqHz:   a.ActiveFreq,
		Preset:   cc1101.GetPresetSpec(a.Preset).SubFileName,
		Protocol: "RAW",
		RawData:  a.CapturedRaw,
	}
	if err := os.WriteFile(filepath.Join(a.CapturesDir, name), []byte(sf.Format()), 0o644); err != nil {
		a.SetToast("Write err!")
		return
	}
	a.SetToast("Saved " + name)
}

func (a *App) saveDecodedKey() {
	if a.LastDecoded == nil {
		a.SetToast("No key to save")
		return
	}
	if err := os.MkdirAll(a.CapturesDir, 0o755); err != nil {
		a.SetToast("Save err!")
		return
	}
	name := fmt.Sprintf("%s_%s.sub", strings.ToLower(a.LastDecoded.Protocol), time.Now().Format("150405"))
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   a.ActiveFreq,
		Preset:   cc1101.GetPresetSpec(a.Preset).SubFileName,
		Protocol: a.LastDecoded.Protocol,
		Bit:      a.LastDecoded.Bits,
		Key:      a.LastDecoded.KeyHex,
		TE:       a.LastDecoded.TE,
	}
	if err := os.WriteFile(filepath.Join(a.CapturesDir, name), []byte(sf.Format()), 0o644); err != nil {
		a.SetToast("Write err!")
		return
	}
	a.SetToast("Saved " + name)
}

func (a *App) refreshSavedFiles() {
	a.SavedFiles = nil
	dirs := []string{a.CapturesDir}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "subg", "files"))
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sub") {
				continue
			}
			full := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			if sf, err := subfile.Parse(string(raw)); err == nil {
				a.SavedFiles = append(a.SavedFiles, SavedEntry{Name: e.Name(), Path: full, File: sf})
			}
		}
	}
	sort.Slice(a.SavedFiles, func(i, j int) bool { return a.SavedFiles[i].Name > a.SavedFiles[j].Name })
	if a.SavedIdx >= len(a.SavedFiles) {
		a.SavedIdx = max(0, len(a.SavedFiles)-1)
	}
}

// Render draws the entire 320x170 UI onto c.
func (a *App) Render(c *Canvas) {
	c.Clear(ColBg)
	a.drawHeader(c)

	switch a.Mode {
	case ModeHeatmap:
		a.drawHeatmap(c)
		a.drawNavBar(c, [5]string{pauseLabel(a.Paused), "BAND", "LOCK", "MOD", "MODE"})
	case ModePulse:
		a.drawPulse(c)
		a.drawNavBar(c, [5]string{recLabel(a.Recording), "TX", "SAVE", "MOD", "MODE"})
	case ModeDecode:
		a.drawDecode(c)
		a.drawNavBar(c, [5]string{pauseLabel(a.Paused), "TX", "SAVE", "CLR", "MODE"})
	case ModeSaved:
		a.drawSaved(c)
		a.drawNavBar(c, [5]string{"TX", "LOAD", "DEL", "SYNC", "MODE"})
	}

	if a.ShowHelp {
		a.drawHelpModal(c)
	}
	if a.ToastText != "" && time.Now().Before(a.ToastUntil) {
		tw := len(a.ToastText)*6 + 14
		tx := (ScreenW - tw) / 2
		c.FillRect(tx, 22, tw, 15, ColNavActive)
		c.DrawRect(tx, 22, tw, 15, ColCyan)
		c.DrawText(tx+7, 26, a.ToastText, ColText, 1)
	}
}

func pauseLabel(paused bool) string {
	if paused {
		return "RUN"
	}
	return "HOLD"
}

func recLabel(rec bool) string {
	if rec {
		return "STOP"
	}
	return "REC"
}

func (a *App) drawHeader(c *Canvas) {
	c.FillRect(0, 0, ScreenW, 17, ColPanel)
	c.HLine(0, 17, ScreenW, ColBorder)

	// App badge.
	c.FillRect(4, 3, 48, 11, ColCyan)
	c.DrawText(7, 5, "RF-HEAT", ColBg, 1)

	// Mode pill.
	c.FillRect(56, 3, 48, 11, ColPanelAlt)
	c.DrawRect(56, 3, 48, 11, ColCyan)
	c.DrawTextCentered(80, 5, a.Mode.String(), ColCyan, 1)

	// Active frequency & preset.
	freqStr := fmt.Sprintf("%.2fM %s", float64(a.ActiveFreq)/1e6, cc1101.GetPresetSpec(a.Preset).Name)
	c.DrawText(110, 5, freqStr, ColText, 1)

	// Hardware / Sim pill.
	if a.SimMode {
		c.FillRect(272, 3, 44, 11, ColPanelAlt)
		c.DrawRect(272, 3, 44, 11, ColAmber)
		c.DrawTextCentered(294, 5, "SIM", ColAmber, 1)
	} else {
		c.FillRect(272, 3, 44, 11, ColPanelAlt)
		c.DrawRect(272, 3, 44, 11, ColGreen)
		c.DrawTextCentered(294, 5, "CC1101", ColGreen, 1)
	}
}

func (a *App) drawHeatmap(c *Canvas) {
	band := DefaultBands[a.BandIdx]
	n := min(len(band.FreqsHz), maxCols)

	// Top info strip: Band + Peak readout.
	c.DrawText(6, 21, band.Name, ColCyan, 1)
	peakStr := fmt.Sprintf("PK %.2fM %ddBm", float64(a.PeakFreqHz)/1e6, a.PeakDBm)
	c.DrawText(72, 21, peakStr, ColAmber, 1)
	curStr := fmt.Sprintf("CUR %.2fM", float64(band.FreqsHz[a.CursorCol])/1e6)
	c.DrawText(224, 21, curStr, ColText, 1)

	// Spectrum bar graph (y=32..79).
	const chartX = 6
	const chartW = 308
	const barTop = 32
	const barH = 46
	c.FillRect(chartX, barTop, chartW, barH, ColPanel)
	c.DrawRect(chartX, barTop, chartW, barH, ColBorder)

	colW := chartW / n
	for i := 0; i < n; i++ {
		bx := chartX + i*colW + 1
		bw := max(2, colW-2)

		// Live bar height.
		h := rssiToHeight(a.SweepRSSI[i], barH-4)
		col := ThermalColor(a.SweepRSSI[i])
		if h > 0 {
			c.FillRect(bx, barTop+barH-2-h, bw, h, col)
		}

		// Peak-hold tick.
		ph := rssiToHeight(a.PeakHold[i], barH-4)
		if ph > 1 {
			c.HLine(bx, barTop+barH-2-ph, bw, ColAmber)
		}

		// Cursor highlight.
		if i == a.CursorCol {
			c.DrawRect(bx-1, barTop+1, bw+2, barH-2, ColCyan)
		}
	}

	// Thermal Waterfall Heatmap (y=82..140).
	const wfTop = 82
	const wfH = 56
	c.DrawRect(chartX-1, wfTop-1, chartW+2, wfH+2, ColBorder)
	for row := 0; row < wfH; row++ {
		ringIdx := (a.WfHead - 1 - row + waterfallRows) % waterfallRows
		y := wfTop + row
		for i := 0; i < n; i++ {
			bx := chartX + i*colW
			col := ThermalColor(a.Waterfall[ringIdx][i])
			c.HLine(bx, y, colW, col)
		}
	}
}

func rssiToHeight(dbm int32, maxH int) int {
	t := float64(dbm+110) / 75.0
	if t < 0 {
		return 0
	}
	if t > 1 {
		t = 1
	}
	return int(math.Round(t * float64(maxH)))
}

func (a *App) drawPulse(c *Canvas) {
	// Big frequency + live RSSI card.
	c.FillRect(6, 21, 308, 34, ColPanel)
	c.DrawRect(6, 21, 308, 34, ColBorder)

	freqText := fmt.Sprintf("%.2f MHz", float64(a.ActiveFreq)/1e6)
	c.DrawText(12, 26, freqText, ColCyan, 2)

	rssiColor := ColMuted
	if a.LiveRSSI >= a.SquelchDBm {
		rssiColor = ColGreen
	}
	c.DrawText(206, 26, fmt.Sprintf("%4ddBm", a.LiveRSSI), rssiColor, 2)
	c.DrawText(12, 44, fmt.Sprintf("SQ:%ddBm  RAW:%d  [F/X:SQ  Z/C:FREQ]", a.SquelchDBm, len(a.CapturedRaw)), ColMuted, 1)

	// Live RSSI / Pulse Oscilloscope (y=59..139).
	const ox, oy, ow, oh = 6, 59, 308, 80
	c.FillRect(ox, oy, ow, oh, ColPanel)
	c.DrawRect(ox, oy, ow, oh, ColBorder)

	// Squelch threshold line.
	sqH := rssiToHeight(a.SquelchDBm, oh-6)
	sqY := oy + oh - 3 - sqH
	for x := ox + 2; x < ox+ow-2; x += 4 {
		c.HLine(x, sqY, 2, ColAmber)
	}

	// Scrolling RSSI bars.
	bars := len(a.RSSIHistory)
	barW := (ow - 8) / bars
	for i := 0; i < bars; i++ {
		idx := (a.RssiHistHead + i) % bars
		v := a.RSSIHistory[idx]
		h := rssiToHeight(v, oh-6)
		col := ThermalColor(v)
		if v >= a.SquelchDBm {
			col = ColGreen
		}
		if h > 0 {
			c.FillRect(ox+4+i*barW, oy+oh-3-h, max(2, barW-1), h, col)
		}
	}
}

func (a *App) drawDecode(c *Canvas) {
	c.FillRect(6, 21, 308, 42, ColPanel)
	c.DrawRect(6, 21, 308, 42, ColCyan)
	if a.LastDecoded == nil {
		c.DrawTextCentered(ScreenW/2, 33, "Listening for OOK frames...", ColMuted, 1)
		c.DrawTextCentered(ScreenW/2, 46, "Princeton / CAME / Linear", ColCyan, 1)
	} else {
		d := a.LastDecoded
		c.DrawText(12, 26, fmt.Sprintf("%s  (%d-bit, TE=%dus)", d.Protocol, d.Bits, d.TE), ColGreen, 1)
		c.DrawText(12, 41, d.KeyHex, ColText, 1)
	}

	c.DrawText(6, 68, "Recent Decoded Keys:", ColMuted, 1)
	c.FillRect(6, 78, 308, 62, ColPanel)
	c.DrawRect(6, 78, 308, 62, ColBorder)
	for i, k := range a.DecodedLog {
		if i >= 5 {
			break
		}
		y := 83 + i*11
		line := fmt.Sprintf("%-9s %2db  %s", k.Protocol, k.Bits, k.KeyHex)
		if len(line) > 48 {
			line = line[:48]
		}
		c.DrawText(12, y, line, ColText, 1)
	}
}

func (a *App) drawSaved(c *Canvas) {
	c.DrawText(6, 21, fmt.Sprintf("Saved .sub Library (%d)", len(a.SavedFiles)), ColCyan, 1)
	c.FillRect(6, 32, 308, 108, ColPanel)
	c.DrawRect(6, 32, 308, 108, ColBorder)

	if len(a.SavedFiles) == 0 {
		c.DrawTextCentered(ScreenW/2, 76, "No .sub files found", ColMuted, 1)
		c.DrawTextCentered(ScreenW/2, 90, "Capture in PULSE or DECODE first", ColMuted, 1)
		return
	}

	start := max(0, a.SavedIdx-3)
	for row := 0; row < 6 && start+row < len(a.SavedFiles); row++ {
		idx := start + row
		entry := a.SavedFiles[idx]
		y := 36 + row*17
		if idx == a.SavedIdx {
			c.FillRect(8, y-1, 304, 15, ColNavActive)
			c.DrawRect(8, y-1, 304, 15, ColCyan)
		}
		proto := entry.File.Protocol
		if proto == "" {
			proto = "RAW"
		}
		label := fmt.Sprintf("%-18s %.2fM %s", truncate(entry.Name, 18), float64(entry.File.FreqHz)/1e6, proto)
		c.DrawText(12, y+3, label, ColText, 1)
	}
}

func (a *App) drawNavBar(c *Canvas, labels [5]string) {
	c.FillRect(0, 143, ScreenW, 27, ColNavBg)
	c.HLine(0, 143, ScreenW, ColBorder)
	keys := [5]string{"4", "5", "6", "7", "8"}
	for i, cx := range NavKeyCenters {
		bx := cx - 25
		by := 146
		bw := 50
		bh := 21
		c.FillRect(bx, by, bw, bh, ColPanelAlt)
		c.DrawRect(bx, by, bw, bh, ColBorder)
		// Keycap badge.
		c.FillRect(bx+2, by+2, 9, 9, ColCyan)
		c.DrawText(bx+4, by+3, keys[i], ColBg, 1)
		// Action label centered at Y=153..160.
		c.DrawTextCentered(cx+4, 154, labels[i], ColText, 1)
	}
}

type helpPageContent struct {
	Title string
	Lines []string
}

var helpPages = []helpPageContent{
	{
		Title: "1/4 CONTROLS & STATUS",
		Lines: []string{
			"4..8   : Bottom bar quick actions",
			"Tab/8  : Switch mode (4 tabs)",
			"F / X  : Up / Down (Band or Squelch)",
			"Z / C  : Left / Right (Cursor/Freq)",
			"B / M  : Cycle ISM Band / Modulation",
			"Enter  : Lock Peak / Transmit",
			"H / Q  : Toggle Help / Quit safely",
			"Top bar: CC1101=HW ok, SIM=Demo mode",
		},
	},
	{
		Title: "2/4 HEATMAP & WATERFALL",
		Lines: []string{
			"Top chart: Live RSSI bars per ch.",
			"Amber ticks: Peak-Hold max signal.",
			"Cyan box: Cursor channel (Z/C move).",
			"Bottom: Thermal Waterfall over time:",
			" Navy(-110) -> Purple -> Orange ->",
			" Yellow(-35dBm) = strongest bursts!",
			"PK=strongest freq, CUR=cursor freq.",
			"Press Enter/6 to LOCK & jump to PULSE",
		},
	},
	{
		Title: "3/4 PULSE, SQUELCH & MOD",
		Lines: []string{
			"SQ (Amber line): Noise gate in dBm.",
			" Green bars = signal above squelch.",
			"AM650/AM270: OOK (keyfobs, gates).",
			" 650kHz=wide catch, 270kHz=low noise.",
			"FM238/FM476: 2-FSK (2.38/47.6kHz dev).",
			"4(REC) captures RAW GDO0 pulses.",
			"5(TX) replays, 6(SAVE) writes .sub",
			" files compatible with Flipper Zero.",
		},
	},
	{
		Title: "4/4 DECODE & SAVED FILES",
		Lines: []string{
			"DECODE listens for OOK fixed codes:",
			" Princeton, CAME, Linear protocols.",
			"Shows Protocol, Bits, Hex Key & TE.",
			"TE = base pulse width in microsec.",
			"SAVED lists .sub files from:",
			" ~/rf-heatmap/captures & ~/subg/files",
			"4(TX)=Send  5(LOAD)=Open in PULSE",
			"6(DEL)=Delete  7(SYNC)=Rescan folder",
		},
	},
}

func (a *App) drawHelpModal(c *Canvas) {
	page := helpPages[a.HelpPage%len(helpPages)]
	c.FillRect(10, 18, 300, 128, ColPanel)
	c.DrawRect(10, 18, 300, 128, ColCyan)
	c.FillRect(11, 19, 298, 13, ColNavActive)
	c.DrawTextCentered(ScreenW/2, 22, page.Title, ColText, 1)

	for i, l := range page.Lines {
		c.DrawText(16, 36+i*12, l, ColText, 1)
	}

	c.HLine(12, 133, 296, ColBorder)
	c.DrawTextCentered(ScreenW/2, 136, "[Z/C] Page   [Enter/H] Close", ColAmber, 1)
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-2] + ".."
}
