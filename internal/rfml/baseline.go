package rfml

import "math"

const (
	// MaxBaselineBins is the upper bound on frequency bins tracked by BinBaseline.
	MaxBaselineBins = 64

	defaultAlpha      = 0.03
	defaultMinStdDBm  = 2.0
	defaultFreezeZ    = 3.0
	defaultSquelchK   = 3.5
	defaultLockZ      = 4.0
	defaultWarmup     = 5
	minSquelchK       = 1.5
	maxSquelchK       = 8.0
	minThresholdDBm   = -120
	maxThresholdDBm   = -20
	minValidRSSIDBm   = -130
	maxValidRSSIDBm   = 10
)

// BaselineConfig configures the per-bin online EWMA noise baseline and anomaly detector.
type BaselineConfig struct {
	Bins          int
	Alpha         float64 // EWMA smoothing factor (0 < Alpha <= 1)
	MinStdDBm     float64 // Minimum std floor in dBm to avoid div-by-zero on flat noise
	FreezeZ       float64 // Z-score above which EWMA updates pause for that bin
	SquelchK      float64 // Multiplier k for SmartThreshold = mean + k*std
	LockZ         float64 // Minimum Z-score for DetectBurst
	WarmupSamples uint32  // Number of initial samples before Ready() returns true
}

// DefaultBaselineConfig returns tuned defaults for CC1101 Sub-GHz RSSI sweeps.
func DefaultBaselineConfig(bins int) BaselineConfig {
	return BaselineConfig{
		Bins:          bins,
		Alpha:         defaultAlpha,
		MinStdDBm:     defaultMinStdDBm,
		FreezeZ:       defaultFreezeZ,
		SquelchK:      defaultSquelchK,
		LockZ:         defaultLockZ,
		WarmupSamples: defaultWarmup,
	}
}

// BurstEvent describes the strongest anomalous RF burst detected across a sweep.
type BurstEvent struct {
	Bin       int
	RSSIDBm   int32
	MeanDBm   float64
	StdDBm    float64
	ZScore    float64
	Threshold int32
}

type binState struct {
	mean    float64
	m2      float64 // Running sum of squared deviations during warmup
	varEWMA float64 // Exponentially weighted variance after warmup
	samples uint32
}

// BinBaseline maintains an online per-bin noise floor (mean + variance) with burst-freeze.
// All operations are zero-allocation on the hot path.
type BinBaseline struct {
	cfg  BaselineConfig
	bins [MaxBaselineBins]binState
}

// NewBinBaseline creates a BinBaseline with sanitized configuration defaults.
func NewBinBaseline(cfg BaselineConfig) *BinBaseline {
	if cfg.Bins <= 0 {
		cfg.Bins = 1
	}
	if cfg.Bins > MaxBaselineBins {
		cfg.Bins = MaxBaselineBins
	}
	if cfg.Alpha <= 0 || cfg.Alpha > 1 || math.IsNaN(cfg.Alpha) || math.IsInf(cfg.Alpha, 0) {
		cfg.Alpha = defaultAlpha
	}
	if cfg.MinStdDBm <= 0 || math.IsNaN(cfg.MinStdDBm) || math.IsInf(cfg.MinStdDBm, 0) {
		cfg.MinStdDBm = defaultMinStdDBm
	}
	if cfg.FreezeZ <= 0 || math.IsNaN(cfg.FreezeZ) || math.IsInf(cfg.FreezeZ, 0) {
		cfg.FreezeZ = defaultFreezeZ
	}
	if cfg.LockZ <= 0 || math.IsNaN(cfg.LockZ) || math.IsInf(cfg.LockZ, 0) {
		cfg.LockZ = defaultLockZ
	}
	if cfg.WarmupSamples == 0 {
		cfg.WarmupSamples = defaultWarmup
	}
	if math.IsNaN(cfg.SquelchK) || math.IsInf(cfg.SquelchK, 0) || cfg.SquelchK <= 0 {
		cfg.SquelchK = defaultSquelchK
	}
	cfg.SquelchK = clampFloat(cfg.SquelchK, minSquelchK, maxSquelchK)

	return &BinBaseline{cfg: cfg}
}

// Reset clears all learned per-bin statistics (call on band or frequency change).
func (b *BinBaseline) Reset() {
	clear(b.bins[:b.cfg.Bins])
}

// Bins returns the number of configured bins.
func (b *BinBaseline) Bins() int { return b.cfg.Bins }

// SquelchK returns the current Z-score multiplier k used for SmartThreshold.
func (b *BinBaseline) SquelchK() float64 { return b.cfg.SquelchK }

// SetSquelchK updates the SmartThreshold multiplier k (clamped to [1.5, 8.0]).
func (b *BinBaseline) SetSquelchK(k float64) {
	if math.IsNaN(k) || math.IsInf(k, 0) {
		return
	}
	b.cfg.SquelchK = clampFloat(k, minSquelchK, maxSquelchK)
}

// Ready reports whether bin has completed warmup and can produce reliable statistics.
func (b *BinBaseline) Ready(bin int) bool {
	if !b.validBin(bin) {
		return false
	}
	return b.bins[bin].samples >= b.cfg.WarmupSamples
}

// Update ingests a full RSSI sweep slice (up to configured Bins).
func (b *BinBaseline) Update(sweep []int32) {
	n := min(len(sweep), b.cfg.Bins)
	for i := range n {
		b.UpdateBin(i, sweep[i])
	}
}

// UpdateBin ingests a single RSSI reading (dBm) for bin.
// Readings classified as active bursts (z >= FreezeZ) after warmup do not shift the noise floor.
func (b *BinBaseline) UpdateBin(bin int, rssi int32) {
	if !b.validBin(bin) {
		return
	}
	x := clampFloat(float64(rssi), minValidRSSIDBm, maxValidRSSIDBm)
	st := &b.bins[bin]

	// Warmup phase: exact Welford online mean & variance for fast convergence.
	if st.samples < b.cfg.WarmupSamples {
		st.samples++
		delta := x - st.mean
		st.mean += delta / float64(st.samples)
		delta2 := x - st.mean
		st.m2 += delta * delta2
		if st.samples > 1 {
			st.varEWMA = st.m2 / float64(st.samples-1)
		}
		return
	}

	// Burst-freeze guard: skip baseline adaptation if reading is an upward anomaly.
	std := b.stdFromState(st)
	if (x-st.mean)/std >= b.cfg.FreezeZ {
		return
	}

	// Steady-state EWMA update for mean and variance.
	diff := x - st.mean
	st.mean += b.cfg.Alpha * diff
	// Update variance around the updated mean to keep it non-negative.
	st.varEWMA = (1.0-b.cfg.Alpha)*st.varEWMA + b.cfg.Alpha*(diff*(x-st.mean))
	if st.varEWMA < 0 {
		st.varEWMA = 0
	}
	st.samples++
}

// MeanDBm returns the learned noise-floor mean for bin (or 0 if invalid/empty).
func (b *BinBaseline) MeanDBm(bin int) float64 {
	if !b.validBin(bin) || b.bins[bin].samples == 0 {
		return 0
	}
	return b.bins[bin].mean
}

// StdDBm returns the effective standard deviation for bin (floored at MinStdDBm).
func (b *BinBaseline) StdDBm(bin int) float64 {
	if !b.validBin(bin) {
		return b.cfg.MinStdDBm
	}
	return b.stdFromState(&b.bins[bin])
}

// AnomalyScore returns the signed Z-score (rssi - mean) / max(std, MinStdDBm).
// Returns 0 if the bin is invalid or still warming up.
func (b *BinBaseline) AnomalyScore(bin int, rssi int32) float64 {
	if !b.Ready(bin) {
		return 0
	}
	x := clampFloat(float64(rssi), minValidRSSIDBm, maxValidRSSIDBm)
	st := &b.bins[bin]
	return (x - st.mean) / b.stdFromState(st)
}

// SmartThreshold returns mean + k*std (clamped to [-120, -20] dBm) once ready,
// or fallback during warmup / invalid bin.
func (b *BinBaseline) SmartThreshold(bin int, fallback int32) int32 {
	if !b.Ready(bin) {
		return fallback
	}
	st := &b.bins[bin]
	thr := int32(math.Round(st.mean + b.cfg.SquelchK*b.stdFromState(st)))
	return min(maxThresholdDBm, max(minThresholdDBm, thr))
}

// DetectBurst scans sweep and returns the bin with the highest Z-score >= LockZ.
func (b *BinBaseline) DetectBurst(sweep []int32) (BurstEvent, bool) {
	n := min(len(sweep), b.cfg.Bins)
	var best BurstEvent
	found := false
	for i := range n {
		if !b.Ready(i) {
			continue
		}
		z := b.AnomalyScore(i, sweep[i])
		if z >= b.cfg.LockZ && (!found || z > best.ZScore) {
			st := &b.bins[i]
			best = BurstEvent{
				Bin:       i,
				RSSIDBm:   sweep[i],
				MeanDBm:   st.mean,
				StdDBm:    b.stdFromState(st),
				ZScore:    z,
				Threshold: b.SmartThreshold(i, -78),
			}
			found = true
		}
	}
	return best, found
}

func (b *BinBaseline) validBin(bin int) bool {
	return bin >= 0 && bin < b.cfg.Bins
}

func (b *BinBaseline) stdFromState(st *binState) float64 {
	std := math.Sqrt(st.varEWMA)
	if math.IsNaN(std) || std < b.cfg.MinStdDBm {
		return b.cfg.MinStdDBm
	}
	return std
}

func clampFloat(v, lo, hi float64) float64 {
	if v < lo {
		return lo
	}
	if v > hi {
		return hi
	}
	return v
}
