package rfml

import "math"

// FeatureVector holds the 12 normalized float32 features fed into the on-device MLP classifier.
// Indices match scripts/train_rfml.py:
//
//	0: BandNorm      (315/433/868/915 normalized)
//	1: LogPulseCount (log10(n+1)/3)
//	2: ShortUsNorm   (ShortUs/1500)
//	3: LongUsNorm    (LongUs/3000)
//	4: RatioNorm     (LongShortRatio/5)
//	5: LogSyncUs     (log10(SyncUs+1)/5)
//	6: JitterRatio   (0..1)
//	7: DutyCycle     (sum(+hi)/sum(|all|))
//	8: FrameLenNorm  (dataPulses / max(syncGaps, 1) / 180.0 — distinguishes 20-52p Fixed OOK vs 120-170p Rolling)
//	9: FrameRepsNorm (min(syncGaps, 8)/8)
//	10: SNRNorm      (clamp(snrDB, 0, 50)/50)
//	11: GlitchRate   (glitchesRemoved/len(raw))
type FeatureVector [12]float32

// ExtractFeatures computes the 12-feature RF vector and 1D K-Means clusters from RAW_Data.
func ExtractFeatures(raw []int32, freqHz uint32, snrDB float64) (FeatureVector, PulseClusters) {
	clusters := ClusterTimings(raw, DefaultMinGlitchUs)

	var f FeatureVector
	f[0] = float32(normalizeBand(freqHz))
	if len(raw) == 0 {
		f[6] = 1.0 // Maximum uncertainty/jitter on empty capture
		f[11] = 1.0
		return f, clusters
	}

	f[1] = clamp32(math.Log10(float64(len(raw))+1.0) / 3.0)
	f[2] = clamp32(clusters.ShortUs / 1500.0)
	f[3] = clamp32(clusters.LongUs / 3000.0)
	f[4] = clamp32(clusters.LongShortRatio / 5.0)
	f[5] = clamp32(math.Log10(clusters.SyncUs+1.0) / 5.0)
	if clusters.Valid {
		f[6] = clamp32(clusters.JitterRatio)
	} else {
		f[6] = 0.75
	}

	f[7] = computeDutyCycle(raw)

	frames := max(clusters.SyncGaps, 1)
	pulsesPerFrame := float64(clusters.DataPulses) / float64(frames)
	f[8] = clamp32(pulsesPerFrame / 180.0)
	f[9] = clamp32(float64(min(clusters.SyncGaps, 8)) / 8.0)
	if !math.IsNaN(snrDB) && !math.IsInf(snrDB, 0) {
		f[10] = clamp32(snrDB / 50.0)
	}
	f[11] = clamp32(float64(clusters.GlitchesRemoved) / float64(len(raw)))

	return f, clusters
}

func computeDutyCycle(raw []int32) float32 {
	var hiSum, totalSum float64
	for _, p := range raw {
		mag := float64(absInt64(p))
		totalSum += mag
		if p > 0 {
			hiSum += mag
		}
	}
	if totalSum <= 0 {
		return 0
	}
	return clamp32(hiSum / totalSum)
}

func normalizeBand(freqHz uint32) float64 {
	switch {
	case freqHz == 0:
		return 0.50
	case freqHz < 374_000_000:
		return 0.25 // 315 MHz band
	case freqHz < 650_500_000:
		return 0.50 // 433 MHz band
	case freqHz < 890_000_000:
		return 0.75 // 868 MHz band
	default:
		return 1.00 // 915 MHz band
	}
}

func clamp32(v float64) float32 {
	if math.IsNaN(v) || v <= 0 {
		return 0
	}
	if math.IsInf(v, 1) || v >= 1 {
		return 1
	}
	return float32(v)
}
