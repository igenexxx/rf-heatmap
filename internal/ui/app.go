package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/config"
	"rf-heatmap/internal/dbsync"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

// App coordinates all UI views, embedded domain state sub-structs, and radio interaction.
type App struct {
	RadioState
	HeatmapState
	PulseState
	MLState
	SavedState
	ProbeState
	SourcesManageState
	SettingsState
	ChromeState
	RollingState
	SensorState
	FoxhuntViewState
	TrimmerState
}

// NewApp initializes the application state.
func NewApp(radio *cc1101.Radio, capturesDir string) *App {
	def := config.DefaultSettings()
	var tx library.Transmitter
	if radio != nil {
		tx = library.NewRadioTransmitter(radio)
	} else {
		tx = &library.SimTransmitter{}
	}
	var extraDirs []string
	if home, err := os.UserHomeDir(); err == nil && home != "" {
		extraDirs = []string{filepath.Join(home, "subg", "files")}
	}
	store := library.NewStore(capturesDir, library.DefaultDatabasesDir(), extraDirs)

	a := &App{
		RadioState: RadioState{
			Radio:      radio,
			SimMode:    radio == nil,
			BandIdx:    0,
			Preset:     cc1101.PresetAM650,
			ActiveFreq: defaultFreqHz,
			SquelchDBm: defaultSquelchDBm,
		},
		HeatmapState: HeatmapState{
			CursorCol:  defaultCursorCol,
			PeakDBm:    defaultPeakDBm,
			PeakFreqHz: defaultFreqHz,
			DiffBins:   make([]rfml.DiffBin, 0, maxCols),
		},
		PulseState: PulseState{
			LiveRSSI: defaultLiveRSSIDBm,
		},
		MLState: MLState{
			SweepBaseline: rfml.NewBinBaseline(rfml.DefaultBaselineConfig(maxCols)),
			LiveBaseline:  rfml.NewBinBaseline(rfml.DefaultBaselineConfig(1)),
			Foxhunt:       rfml.NewKalmanRSSI(rfml.DefaultKalmanQ, rfml.DefaultKalmanR),
			Activity:      rfml.NewPeriodicityTracker(rfml.DefaultMinBurstGap),
			AutoSquelch:   def.AutoSquelch,
			AutoLock:      def.AutoLock,
		},
		SavedState: SavedState{
			CapturesDir: capturesDir,
		},
		ProbeState: ProbeState{
			Store: store,
			TX:    library.NewTXWorker(tx),
			Probe: library.NewProbeSession(def.ProbeIntervalMs),
		},
		SettingsState: SettingsState{
			SourcesPath:  dbsync.DefaultSourcesPath(),
			BaselinesDir: rfml.DefaultBaselinesDir(),
			Settings:     def,
			Syncer:       dbsync.NewSyncer(store.DatabasesDir),
			syncDoneCh:   make(chan []dbsync.SyncReport, 1),
		},
		ChromeState: ChromeState{
			Mode: ModeHeatmap,
		},
		RollingState: RollingState{
			RollingDetector: subfile.NewRollingDetector(),
		},
		FoxhuntViewState: FoxhuntViewState{
			Tracker: rfml.NewFoxhuntTracker(rfml.DefaultKalmanQ, rfml.DefaultKalmanR),
		},
	}
	a.initBuffers()
	a.refreshSavedFiles()
	a.refreshProbePacks()
	return a
}

// Close stops background TX, GDO0 RX capture, and DB sync workers cleanly.
func (a *App) Close() {
	a.stopRXCapture()
	if a.syncCancel != nil {
		a.syncCancel()
		a.syncCancel = nil
	}
	if a.TX != nil {
		a.TX.Close()
	}
}

// SetLibraryStore replaces the Store (e.g. for custom databases dir or isolated tests).
func (a *App) SetLibraryStore(store *library.Store) {
	if store == nil {
		return
	}
	a.Store = store
	a.CapturesDir = store.UserDir
	a.Syncer = dbsync.NewSyncer(store.DatabasesDir)
	a.Packs = nil
	a.PackIdx = 0
	a.refreshSavedFiles()
	a.refreshProbePacks()
}

// SetSourcesPath sets the JSON path used to load remote DB sync sources.
func (a *App) SetSourcesPath(path string) {
	a.SourcesPath = path
}

func (a *App) initBuffers() {
	for i := range a.SweepRSSI {
		a.SweepRSSI[i] = defaultFloorDBm
		a.PeakHold[i] = defaultFloorDBm
	}
	for r := range a.Waterfall {
		for c := range a.Waterfall[r] {
			a.Waterfall[r][c] = defaultWaterfallDBm
		}
	}
	for i := range a.RSSIHistory {
		a.RSSIHistory[i] = defaultFloorDBm
	}
}

// SetConfigPath sets the JSON settings path used for persistence in the SET tab.
func (a *App) SetConfigPath(path string) {
	a.ConfigPath = path
}

// ApplySettings applies persisted settings to the runtime UI and ML baselines.
func (a *App) ApplySettings(s config.Settings) {
	s = s.Normalize()
	a.Settings = s
	a.AutoSquelch = s.AutoSquelch
	a.AutoLock = s.AutoLock
	if a.SweepBaseline != nil {
		a.SweepBaseline.SetSquelchK(s.SquelchK)
	}
	if a.LiveBaseline != nil {
		a.LiveBaseline.SetSquelchK(s.SquelchK)
	}
	if a.Probe != nil {
		a.Probe.SetIntervalMs(s.ProbeIntervalMs)
	}
	if a.Radio != nil {
		_ = a.Radio.SetTXPowerDBm(a.ActiveFreq, s.TXPowerDBm)
	}
}

// SetToast displays a brief status message.
func (a *App) SetToast(msg string) {
	a.ToastText = msg
	a.ToastUntil = time.Now().Add(toastDuration)
}

// EffectiveSquelchDBm returns the active squelch threshold for the live channel
// (adaptive Smart Squelch when AutoSquelch is ON and warmed up, otherwise manual SquelchDBm).
func (a *App) EffectiveSquelchDBm() int32 {
	if a.AutoSquelch && a.LiveBaseline != nil {
		return a.LiveBaseline.SmartThreshold(0, a.SquelchDBm)
	}
	return a.SquelchDBm
}

// HandleKey processes a single KeyAction and returns true if the app should exit.
func (a *App) HandleKey(act KeyAction) bool {
	if act == KeyHelp {
		a.ShowHelp = !a.ShowHelp
		a.HelpPage = 0
		return false
	}
	if a.ShowHelp {
		a.handleHelpKey(act)
		return false
	}
	if a.ShowSources {
		a.handleSourcesManageKey(act)
		return false
	}
	if a.ShowFoxhunt {
		a.handleFoxhuntKey(act)
		return false
	}
	if a.ShowTrimmer {
		a.handleTrimmerKey(act)
		return false
	}
	if a.ShowSensors {
		a.handleSensorsKey(act)
		return false
	}
	if a.handleGlobalKey(act) {
		return true
	}
	a.dispatchModeKey(act)
	return false
}

func (a *App) handleHelpKey(act KeyAction) {
	n := len(helpPages)
	switch act {
	case KeyLeft, KeyUp:
		a.HelpPage = (a.HelpPage + n - 1) % n
	case KeyRight, KeyDown, KeySpace, KeyTab, KeyNav5:
		a.HelpPage = (a.HelpPage + 1) % n
	case KeyQuit, KeyEnter, KeyNav4, KeyNav8:
		a.ShowHelp = false
	}
}

func (a *App) handleGlobalKey(act KeyAction) bool {
	switch act {
	case KeyQuit:
		a.stopActiveProbe()
		return true
	case KeyTab, KeyNav8:
		if a.Mode == ModeProbe {
			a.stopActiveProbe()
		}
		a.Mode = (a.Mode + 1) % modeCount
		if a.Mode == ModeSaved {
			a.refreshSavedFiles()
		} else if a.Mode == ModeProbe {
			a.refreshProbePacks()
		}
	case KeyBand:
		if a.Mode == ModeHeatmap {
			return false
		}
		if a.Mode == ModeProbe && a.cycleProbeGenFreq() {
			return false
		}
		a.cycleBand()
	case KeyMod:
		a.cyclePreset()
		if a.Mode == ModeProbe {
			a.syncProbeGenPreset()
		}
	case KeyAutoSquelch:
		a.toggleAutoSquelch()
	case KeyAutoLock:
		a.toggleAutoLock()
	}
	return false
}

func (a *App) toggleAutoSquelch() {
	a.AutoSquelch = !a.AutoSquelch
	a.Settings.AutoSquelch = a.AutoSquelch
	if a.AutoSquelch {
		a.SetToast(fmt.Sprintf("Auto Squelch ON (k=%.1f)", a.LiveBaseline.SquelchK()))
		return
	}
	a.SetToast(fmt.Sprintf("Manual Squelch (%ddBm)", a.SquelchDBm))
}

func (a *App) toggleAutoLock() {
	a.AutoLock = !a.AutoLock
	a.Settings.AutoLock = a.AutoLock
	if a.AutoLock {
		a.SetToast("Auto-Lock ON")
		return
	}
	a.SetToast("Auto-Lock OFF")
}

func (a *App) dispatchModeKey(act KeyAction) {
	switch a.Mode {
	case ModeHeatmap:
		a.handleHeatmapKey(act)
	case ModePulse:
		a.handlePulseKey(act)
	case ModeDecode:
		a.handleDecodeKey(act)
	case ModeSaved:
		a.handleSavedKey(act)
	case ModeProbe:
		a.handleProbeKey(act)
	case ModeSettings:
		a.handleSettingsKey(act)
	}
}

func (a *App) cycleBand() {
	a.BandIdx = (a.BandIdx + 1) % len(DefaultBands)
	for i := range a.PeakHold {
		a.PeakHold[i] = defaultFloorDBm
	}
	if a.SweepBaseline != nil {
		a.SweepBaseline.Reset()
	}
	if a.LiveBaseline != nil {
		a.LiveBaseline.Reset()
	}
	a.HasBurst = false
	a.DiffMode = false
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

// Step advances one sampling/simulation tick.
func (a *App) Step() {
	a.tick++
	a.pollTXResults()
	a.pollSyncResults()
	a.syncRXCapture()
	a.drainRXPulses()
	if a.Paused {
		return
	}
	if a.ShowFoxhunt {
		a.stepFoxhunt()
		return
	}
	if a.ShowSensors {
		a.stepSensors()
		return
	}
	switch a.Mode {
	case ModeHeatmap:
		a.stepHeatmap()
	case ModePulse, ModeDecode:
		a.stepLiveChannel()
	case ModeProbe:
		a.stepProbe()
	}
}

// Render draws the entire 320x170 UI onto c.
func (a *App) Render(c *Canvas) {
	c.Clear(ColBg)
	a.drawHeader(c)

	switch {
	case a.ShowFoxhunt:
		a.drawFoxhunt(c)
		a.drawNavBar(c, [5]string{"RST PK", "MUTE", "BAND", "MOD", "BACK"})
	case a.ShowTrimmer:
		a.drawTrimmer(c)
		a.drawNavBar(c, [5]string{"HDL", "AUTO", "TX", "SAVE", "BACK"})
	case a.ShowSensors:
		a.drawSensors(c)
		a.drawNavBar(c, [5]string{pauseLabel(a.Paused), "UNIT", "PROF", "CLR", "BACK"})
	default:
		a.renderModeView(c)
	}

	if a.ShowHelp {
		a.drawHelpModal(c)
	}
	a.drawToast(c)
}

func (a *App) renderModeView(c *Canvas) {
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
		a.drawNavBar(c, [5]string{"TX", "LOAD", "DEL", "PURGE", "MODE"})
	case ModeProbe:
		a.drawProbe(c)
		a.drawNavBar(c, a.probeNavLabels())
	case ModeSettings:
		if a.ShowSources {
			a.drawSourcesManage(c)
			a.drawNavBar(c, [5]string{"ON/OFF", "DIR", "ALL", "SYNC", "BACK"})
		} else {
			a.drawSettings(c)
			a.drawNavBar(c, [5]string{"TOGGLE", "SAVE", "RESET", "HELP", "MODE"})
		}
	}
}

func (a *App) drawToast(c *Canvas) {
	if a.ToastText == "" || !time.Now().Before(a.ToastUntil) {
		return
	}
	tw := len(a.ToastText)*6 + 14
	tx := (ScreenW - tw) / 2
	c.FillRect(tx, 22, tw, 15, ColNavActive)
	c.DrawRect(tx, 22, tw, 15, ColCyan)
	c.DrawText(tx+7, 26, a.ToastText, ColText, 1)
}
