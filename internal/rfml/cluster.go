package rfml

import (
	"math"
	"slices"
)

const (
	// DefaultMinGlitchUs is the default minimum pulse duration (µs) below which
	// pulses are treated as EMI glitches and merged into adjacent pulses.
	DefaultMinGlitchUs int32 = 60

	// DefaultSyncThresholdUs separates normal OOK data pulses from long sync gaps.
	DefaultSyncThresholdUs int32 = 3500

	maxKMeansIters = 20
	minDataPulses  = 6
)

// PulseClusters summarizes the timing structure discovered by 1D K-Means on RAW_Data.
type PulseClusters struct {
	ShortUs         float64 `json:"short_us"`
	LongUs          float64 `json:"long_us"`
	SyncUs          float64 `json:"sync_us"`
	LongShortRatio  float64 `json:"long_short_ratio"`
	JitterRatio     float64 `json:"jitter_ratio"`
	DataPulses      int     `json:"data_pulses"`
	SyncGaps        int     `json:"sync_gaps"`
	GlitchesRemoved int     `json:"glitches_removed"`
	ModulationHint  string  `json:"modulation_hint"`
	Valid           bool    `json:"valid"`
}

// DenoisePulses strips sub-threshold EMI glitches (|p| < minGlitchUs) and merges
// adjacent same-sign pulses so sign alternation (+high / -low) is preserved.
func DenoisePulses(raw []int32, minGlitchUs int32) ([]int32, int) {
	if len(raw) == 0 {
		return nil, 0
	}
	if minGlitchUs <= 0 {
		minGlitchUs = DefaultMinGlitchUs
	}

	out := make([]int32, 0, len(raw))
	glitches := 0

	for _, p := range raw {
		if p == 0 {
			glitches++
			continue
		}
		mag := absInt64(p)
		if mag < int64(minGlitchUs) {
			glitches++
			// Absorb short glitch duration into the preceding pulse to preserve total period.
			if len(out) > 0 {
				out[len(out)-1] = addSameSign(out[len(out)-1], mag)
			}
			continue
		}
		// Merge adjacent pulses that ended up with the same sign after glitch absorption.
		if len(out) > 0 && sameSign(out[len(out)-1], p) {
			out[len(out)-1] = addSameSign(out[len(out)-1], mag)
			continue
		}
		out = append(out, p)
	}
	return out, glitches
}

// ClusterTimings runs glitch denoising followed by deterministic 1D K-Means (k=2)
// over data-pulse magnitudes (|p| <= DefaultSyncThresholdUs), separating sync gaps.
func ClusterTimings(raw []int32, minGlitchUs int32) PulseClusters {
	if minGlitchUs <= 0 {
		minGlitchUs = DefaultMinGlitchUs
	}
	cleaned, glitches := DenoisePulses(raw, minGlitchUs)
	if len(cleaned) == 0 {
		return PulseClusters{GlitchesRemoved: glitches, ModulationHint: "Unstructured"}
	}

	dataMags, syncSum, syncCount := splitDataAndSyncPulses(cleaned)
	res := PulseClusters{
		DataPulses:      len(dataMags),
		SyncGaps:        syncCount,
		GlitchesRemoved: glitches,
		ModulationHint:  "Unstructured",
	}
	if syncCount > 0 {
		res.SyncUs = math.Round(syncSum / float64(syncCount))
	}
	if len(dataMags) < minDataPulses {
		return res
	}

	c0, c1, uniform := runKMeans1D(dataMags)
	if uniform {
		res.ShortUs, res.LongUs = c0, c0
		res.LongShortRatio = 1.0
		res.ModulationHint = "Uniform / Manchester"
		res.Valid = true
		return res
	}

	jitter := computeClusterJitter(dataMags, c0, c1)
	ratio := 1.0
	if c0 > 0 {
		ratio = c1 / c0
	}
	res.ShortUs = math.Round(c0)
	res.LongUs = math.Round(c1)
	res.LongShortRatio = math.Round(ratio*100) / 100
	res.JitterRatio = math.Round(jitter*1000) / 1000
	res.ModulationHint = classifyModulationHint(ratio, jitter)
	res.Valid = res.ShortUs > 0
	return res
}

func splitDataAndSyncPulses(cleaned []int32) ([]float64, float64, int) {
	dataMags := make([]float64, 0, len(cleaned))
	var syncSum float64
	syncCount := 0
	for _, p := range cleaned {
		mag := float64(absInt64(p))
		if mag > float64(DefaultSyncThresholdUs) {
			syncSum += mag
			syncCount++
		} else {
			dataMags = append(dataMags, mag)
		}
	}
	return dataMags, syncSum, syncCount
}

func runKMeans1D(dataMags []float64) (float64, float64, bool) {
	// Deterministic quantile initialization (p20, p80) avoids RNG variance.
	sorted := slices.Clone(dataMags)
	slices.Sort(sorted)
	c0 := sorted[len(sorted)/5]
	c1 := sorted[(len(sorted)*4)/5]
	if c1 <= c0 {
		return c0, c0, true
	}

	for range maxKMeansIters {
		var sum0, sum1 float64
		var cnt0, cnt1 int
		mid := 0.5 * (c0 + c1)
		for _, v := range dataMags {
			if v <= mid {
				sum0 += v
				cnt0++
			} else {
				sum1 += v
				cnt1++
			}
		}
		if cnt0 == 0 || cnt1 == 0 {
			break
		}
		next0, next1 := sum0/float64(cnt0), sum1/float64(cnt1)
		converged := math.Abs(next0-c0) < 0.5 && math.Abs(next1-c1) < 0.5
		c0, c1 = next0, next1
		if converged {
			break
		}
	}
	if c0 > c1 {
		c0, c1 = c1, c0
	}
	return c0, c1, false
}

func computeClusterJitter(dataMags []float64, c0, c1 float64) float64 {
	mid := 0.5 * (c0 + c1)
	var jitterSum float64
	for _, v := range dataMags {
		centroid := c0
		if v > mid {
			centroid = c1
		}
		if centroid > 0 {
			jitterSum += math.Abs(v-centroid) / centroid
		}
	}
	return jitterSum / float64(len(dataMags))
}

// QuantizePulses denoises raw pulses and snaps every data pulse (|p| <= DefaultSyncThresholdUs)
// to the nearest discovered centroid (ShortUs or LongUs) while preserving signs and sync gaps.
func QuantizePulses(raw []int32, clusters PulseClusters, minGlitchUs int32) []int32 {
	if !clusters.Valid || clusters.ShortUs <= 0 {
		return nil
	}
	cleaned, _ := DenoisePulses(raw, minGlitchUs)
	if len(cleaned) == 0 {
		return nil
	}

	shortVal := int32(math.Round(clusters.ShortUs))
	longVal := max(shortVal, int32(math.Round(clusters.LongUs)))
	mid := 0.5 * (clusters.ShortUs + clusters.LongUs)

	out := make([]int32, len(cleaned))
	for i, p := range cleaned {
		mag := float64(absInt64(p))
		if mag > float64(DefaultSyncThresholdUs) {
			out[i] = p
			continue
		}
		q := shortVal
		if mag > mid {
			q = longVal
		}
		if p < 0 {
			out[i] = -q
		} else {
			out[i] = q
		}
	}
	return out
}

func classifyModulationHint(ratio, jitter float64) string {
	if jitter > 0.32 {
		return "Noise / Unstructured"
	}
	switch {
	case ratio >= 2.3 && ratio <= 4.2:
		return "OOK PWM 1:3"
	case ratio >= 1.5 && ratio < 2.3:
		return "OOK PWM 1:2"
	case ratio < 1.35:
		return "Uniform / Manchester"
	default:
		return "Wide Ratio OOK"
	}
}

func absInt64(v int32) int64 {
	if v < 0 {
		return -int64(v)
	}
	return int64(v)
}

func sameSign(a, b int32) bool {
	return (a > 0 && b > 0) || (a < 0 && b < 0)
}

func addSameSign(base int32, deltaMag int64) int32 {
	if base < 0 {
		return int32(max(int64(math.MinInt32+1), int64(base)-deltaMag))
	}
	return int32(min(int64(math.MaxInt32), int64(base)+deltaMag))
}
