package rfml

import (
	"math"
	"slices"
)

const (
	// DefaultSnapshotHistBins is the default number of microsecond timing histogram buckets.
	DefaultSnapshotHistBins = 24
	minSnapshotHistBins     = 8
	maxSnapshotHistBins     = 48
	// DefaultSnapshotWindowUs is the default oscilloscope time span (~32 ms) at 1x zoom.
	DefaultSnapshotWindowUs int64 = 32_000
	minSnapshotSpanUs       int64 = 200
)

// PulseHistogramBin holds the +HIGH and -LOW pulse counts within a [MinUs, MaxUs] bucket.
type PulseHistogramBin struct {
	MinUs      int32 `json:"min_us"`
	MaxUs      int32 `json:"max_us"`
	CenterUs   int32 `json:"center_us"`
	HighCount  int   `json:"high_count"`
	LowCount   int   `json:"low_count"`
	TotalCount int   `json:"total_count"`
}

// WaveformSegment represents one contiguous OOK carrier-on (+HIGH) or carrier-off (-LOW) pulse.
type WaveformSegment struct {
	Index      int   `json:"index"`
	StartUs    int64 `json:"start_us"`
	DurationUs int32 `json:"duration_us"`
	High       bool  `json:"high"`
	IsSync     bool  `json:"is_sync"`
	IsGlitch   bool  `json:"is_glitch"`
}

// SignalSnapshot captures a complete time-domain oscilloscope trace and pulse-width histogram
// for a Sub-GHz RAW or synthesized OOK pulse train.
type SignalSnapshot struct {
	FreqHz          uint32              `json:"freq_hz"`
	Preset          string              `json:"preset"`
	TotalPulses     int                 `json:"total_pulses"`
	HighPulses      int                 `json:"high_pulses"`
	LowPulses       int                 `json:"low_pulses"`
	SyncGaps        int                 `json:"sync_gaps"`
	FramesEstimated int                 `json:"frames_estimated"`
	TotalDurationUs int64               `json:"total_duration_us"`
	TotalDurationMs float64             `json:"total_duration_ms"`
	DutyCyclePct    float64             `json:"duty_cycle_pct"`
	MinDataUs       int32               `json:"min_data_us"`
	MaxDataUs       int32               `json:"max_data_us"`
	MedianDataUs    int32               `json:"median_data_us"`
	MaxSyncUs       int32               `json:"max_sync_us"`
	HistMinUs       int32               `json:"hist_min_us"`
	HistMaxUs       int32               `json:"hist_max_us"`
	HistPeakCount   int                 `json:"hist_peak_count"`
	ShortBinIdx     int                 `json:"short_bin_idx"`
	LongBinIdx      int                 `json:"long_bin_idx"`
	Clusters        PulseClusters       `json:"clusters"`
	Histogram       []PulseHistogramBin `json:"histogram"`
	Segments        []WaveformSegment   `json:"segments"`
}

// BuildSignalSnapshot analyzes raw signed microsecond pulses (+high / -low) into a
// SignalSnapshot containing waveform segments, K-Means timing centroids, duty cycle,
// and a dual-polarity pulse-width histogram.
func BuildSignalSnapshot(raw []int32, freqHz uint32, preset string, bins int) SignalSnapshot {
	bins = clampHistBins(bins)
	snap := SignalSnapshot{
		FreqHz:      freqHz,
		Preset:      preset,
		ShortBinIdx: -1,
		LongBinIdx:  -1,
		Clusters:    ClusterTimings(raw, DefaultMinGlitchUs),
	}
	if len(raw) == 0 {
		return snap
	}

	segments, stats := collectWaveformSegments(raw)
	if len(segments) == 0 {
		return snap
	}

	snap.Segments = segments
	snap.TotalPulses = len(segments)
	snap.HighPulses = stats.highCount
	snap.LowPulses = stats.lowCount
	snap.SyncGaps = stats.syncCount
	snap.FramesEstimated = max(1, stats.syncCount)
	snap.TotalDurationUs = stats.totalUs
	snap.TotalDurationMs = math.Round(float64(stats.totalUs)/100.0) / 10.0
	if stats.totalUs > 0 {
		snap.DutyCyclePct = math.Round((float64(stats.highUs)/float64(stats.totalUs))*1000.0) / 10.0
	}
	snap.MaxSyncUs = stats.maxSyncUs

	dataMags := stats.dataMags
	if len(dataMags) == 0 {
		dataMags = stats.allMags
	}
	snap.MinDataUs, snap.MaxDataUs, snap.MedianDataUs = summarizeDataMags(dataMags)

	histMin, histMax := computeHistogramBounds(snap.MinDataUs, snap.MaxDataUs, snap.Clusters)
	snap.HistMinUs = histMin
	snap.HistMaxUs = histMax
	snap.Histogram, snap.HistPeakCount = buildPulseHistogram(segments, histMin, histMax, bins, len(stats.dataMags) > 0)
	if snap.Clusters.Valid {
		snap.ShortBinIdx = mapUsToBin(snap.Clusters.ShortUs, histMin, histMax, bins)
		snap.LongBinIdx = mapUsToBin(snap.Clusters.LongUs, histMin, histMax, bins)
	}
	return snap
}

// RecommendedWindowUs returns an informative default 1x oscilloscope window span in µs.
func (s SignalSnapshot) RecommendedWindowUs() int64 {
	if s.TotalDurationUs <= 0 {
		return minSnapshotSpanUs
	}
	if s.TotalDurationUs <= DefaultSnapshotWindowUs {
		return max(minSnapshotSpanUs, s.TotalDurationUs)
	}
	if s.FramesEstimated > 1 {
		perFrame := s.TotalDurationUs / int64(s.FramesEstimated)
		window := perFrame * 2
		return max(minSnapshotSpanUs, min(s.TotalDurationUs, max(int64(8000), min(window, int64(64_000)))))
	}
	return DefaultSnapshotWindowUs
}

// SliceWaveformWindow returns waveform segments overlapping [startUs, startUs+spanUs].
func (s SignalSnapshot) SliceWaveformWindow(startUs, spanUs int64) []WaveformSegment {
	if len(s.Segments) == 0 || spanUs <= 0 {
		return nil
	}
	if startUs < 0 {
		startUs = 0
	}
	endUs := startUs + spanUs
	out := make([]WaveformSegment, 0, 64)
	for _, seg := range s.Segments {
		segEnd := seg.StartUs + int64(seg.DurationUs)
		if segEnd <= startUs {
			continue
		}
		if seg.StartUs >= endUs {
			break
		}
		out = append(out, seg)
	}
	return out
}

type pulseStats struct {
	highCount int
	lowCount  int
	syncCount int
	highUs    int64
	totalUs   int64
	maxSyncUs int32
	dataMags  []int32
	allMags   []int32
}

func collectWaveformSegments(raw []int32) ([]WaveformSegment, pulseStats) {
	segments := make([]WaveformSegment, 0, len(raw))
	stats := pulseStats{
		dataMags: make([]int32, 0, len(raw)),
		allMags:  make([]int32, 0, len(raw)),
	}
	var cursorUs int64
	for _, p := range raw {
		if p == 0 {
			continue
		}
		mag64 := absInt64(p)
		dur := int32(min(int64(math.MaxInt32), mag64))
		isHigh := p > 0
		isSync := dur > DefaultSyncThresholdUs
		isGlitch := dur < DefaultMinGlitchUs

		segments = append(segments, WaveformSegment{
			Index:      len(segments),
			StartUs:    cursorUs,
			DurationUs: dur,
			High:       isHigh,
			IsSync:     isSync,
			IsGlitch:   isGlitch,
		})
		cursorUs += int64(dur)
		stats.totalUs += int64(dur)
		stats.allMags = append(stats.allMags, dur)
		if isHigh {
			stats.highCount++
			stats.highUs += int64(dur)
		} else {
			stats.lowCount++
		}
		if isSync {
			stats.syncCount++
			if dur > stats.maxSyncUs {
				stats.maxSyncUs = dur
			}
		} else if !isGlitch {
			stats.dataMags = append(stats.dataMags, dur)
		}
	}
	return segments, stats
}

func summarizeDataMags(mags []int32) (minV, maxV, medV int32) {
	if len(mags) == 0 {
		return 0, 0, 0
	}
	sorted := slices.Clone(mags)
	slices.Sort(sorted)
	return sorted[0], sorted[len(sorted)-1], sorted[len(sorted)/2]
}

func computeHistogramBounds(minUs, maxUs int32, clusters PulseClusters) (int32, int32) {
	lo := int64(minUs)
	hi := int64(maxUs)
	if clusters.Valid && clusters.ShortUs > 0 {
		lo = min(lo, int64(math.Floor(clusters.ShortUs)))
		hi = max(hi, int64(math.Ceil(clusters.LongUs)))
	}
	span := hi - lo
	if span < 120 {
		pad := (120 - span) / 2
		lo = max(int64(0), lo-pad)
		hi = lo + 120
	} else {
		pad := max(int64(25), span/10)
		lo = max(int64(0), lo-pad)
		hi += pad
	}
	if hi <= lo {
		hi = lo + 120
	}
	return int32(min(int64(math.MaxInt32-1), lo)), int32(min(int64(math.MaxInt32), hi))
}

func buildPulseHistogram(segments []WaveformSegment, minUs, maxUs int32, bins int, excludeSync bool) ([]PulseHistogramBin, int) {
	hist := make([]PulseHistogramBin, bins)
	span := float64(int64(maxUs) - int64(minUs))
	if span <= 0 {
		span = 1
	}
	for i := range bins {
		bLo := int32(math.Round(float64(minUs) + (float64(i)/float64(bins))*span))
		bHi := int32(math.Round(float64(minUs) + (float64(i+1)/float64(bins))*span))
		hist[i] = PulseHistogramBin{
			MinUs:    bLo,
			MaxUs:    bHi,
			CenterUs: int32((int64(bLo) + int64(bHi)) / 2),
		}
	}

	peak := 0
	for _, seg := range segments {
		if excludeSync && (seg.IsSync || seg.IsGlitch) {
			continue
		}
		idx := mapUsToBin(float64(seg.DurationUs), minUs, maxUs, bins)
		if idx < 0 || idx >= bins {
			continue
		}
		if seg.High {
			hist[idx].HighCount++
		} else {
			hist[idx].LowCount++
		}
		hist[idx].TotalCount++
		if hist[idx].TotalCount > peak {
			peak = hist[idx].TotalCount
		}
	}
	return hist, peak
}

func mapUsToBin(us float64, minUs, maxUs int32, bins int) int {
	if bins <= 0 || maxUs <= minUs {
		return -1
	}
	t := (us - float64(minUs)) / float64(int64(maxUs)-int64(minUs))
	if t < 0 {
		t = 0
	}
	if t >= 1 {
		return bins - 1
	}
	return int(t * float64(bins))
}

func clampHistBins(bins int) int {
	if bins <= 0 {
		return DefaultSnapshotHistBins
	}
	return min(maxSnapshotHistBins, max(minSnapshotHistBins, bins))
}
