package ui

import (
	"math"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/config"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

const (
	waterfallRows        = 56
	maxCols              = 36
	rssiHistoryLen       = 64
	modeCount            = 5
	defaultFreqHz        = 433_920_000
	defaultCursorCol     = 18
	defaultSquelchDBm    = -78
	defaultPeakDBm       = -115
	defaultLiveRSSIDBm   = -105
	defaultFloorDBm      = -108
	defaultWaterfallDBm  = -110
	manualCursorHoldTick = 15
	toastDuration        = 2 * time.Second
)

// Mode represents the active screen tab.
type Mode int

const (
	ModeHeatmap Mode = iota
	ModePulse
	ModeDecode
	ModeSaved
	ModeSettings
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
	case ModeSettings:
		return "SET"
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
	{Name: "433 ISM", FreqsHz: linspaceHz(433_050_000, 434_790_000, maxCols)},
	{Name: "868 EU", FreqsHz: linspaceHz(868_000_000, 869_800_000, maxCols)},
	{Name: "315 US", FreqsHz: linspaceHz(314_500_000, 315_500_000, maxCols)},
	{Name: "915 ISM", FreqsHz: linspaceHz(902_000_000, 928_000_000, maxCols)},
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
	for i := range n {
		out[i] = start + uint32(math.Round(float64(i)*step))
	}
	return out
}

// SavedEntry represents a .sub file on disk.
type SavedEntry struct {
	Name string
	Path string
	File *subfile.SubFile
}

// RadioState holds transceiver handle, active tuning, modulation preset, and manual squelch.
type RadioState struct {
	Radio      *cc1101.Radio // nil when running in simulator mode
	SimMode    bool
	BandIdx    int
	Preset     cc1101.Preset
	ActiveFreq uint32
	SquelchDBm int32
}

// HeatmapState holds spectrum sweep buffers, peak-hold, cursor, and waterfall ring.
type HeatmapState struct {
	CursorCol  int
	SweepRSSI  [maxCols]int32
	PeakHold   [maxCols]int32
	Waterfall  [waterfallRows][maxCols]int32
	WfHead     int
	PeakFreqHz uint32
	PeakDBm    int32
}

// PulseState holds live RSSI oscilloscope history, RAW capture buffer, and decoded OOK keys.
type PulseState struct {
	LiveRSSI     int32
	RSSIHistory  [rssiHistoryLen]int32
	RssiHistHead int
	Recording    bool
	CapturedRaw  []int32
	LastDecoded  *subfile.DecodedKey
	DecodedLog   []subfile.DecodedKey
}

// MLState holds on-device RF ML baselines, anomaly/cluster/MLP/fingerprint/foxhunt state.
type MLState struct {
	SweepBaseline        *rfml.BinBaseline
	LiveBaseline         *rfml.BinBaseline
	AutoSquelch          bool
	AutoLock             bool
	LastBurst            rfml.BurstEvent
	HasBurst             bool
	LastCluster          rfml.PulseClusters
	LastPrediction       rfml.Prediction
	HasPrediction        bool
	FPIndex              *rfml.FingerprintIndex
	LastMatch            rfml.MatchResult
	HasMatch             bool
	Foxhunt              *rfml.KalmanRSSI
	Activity             *rfml.PeriodicityTracker
	LastKalman           rfml.KalmanState
	LastActivity         rfml.ActivityReport
	noiseSaveWarnUntil   time.Time
	manualCursorCooldown int
}

// SavedState holds the .sub capture library directory and list selection.
type SavedState struct {
	CapturesDir       string
	SavedFiles        []SavedEntry
	SavedIdx          int
	purgeConfirmUntil time.Time
}

// SettingsState holds persisted opt-in settings and SET tab cursor.
type SettingsState struct {
	ConfigPath  string
	Settings    config.Settings
	SettingsIdx int
}

// ChromeState holds active screen mode, pause/help overlays, toast notification, and tick counter.
type ChromeState struct {
	Mode       Mode
	Paused     bool
	ShowHelp   bool
	HelpPage   int
	ToastText  string
	ToastUntil time.Time
	tick       uint64
}
