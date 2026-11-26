package rfml

import (
	"math"
	"testing"
)

func TestBuildSignalSnapshot_PrincetonOOK(t *testing.T) {
	// Synthesize 2 frames of a clean 1:3 ratio OOK signal (TE=350us, Long=1050us, Sync=-10850us).
	var raw []int32
	for range 2 {
		raw = append(raw, -10850)
		for b := range 12 {
			if b%2 == 0 {
				raw = append(raw, 350, -1050)
			} else {
				raw = append(raw, 1050, -350)
			}
		}
		raw = append(raw, 350)
	}
	raw = append(raw, -10850)

	snap := BuildSignalSnapshot(raw, 433_920_000, "AM650", 24)
	if snap.TotalPulses != len(raw) {
		t.Fatalf("TotalPulses = %d, want %d", snap.TotalPulses, len(raw))
	}
	if snap.SyncGaps != 3 {
		t.Fatalf("SyncGaps = %d, want 3", snap.SyncGaps)
	}
	if snap.FramesEstimated != 3 {
		t.Fatalf("FramesEstimated = %d, want 3", snap.FramesEstimated)
	}
	if snap.MinDataUs != 350 || snap.MaxDataUs != 1050 {
		t.Fatalf("data range = [%d..%d], want [350..1050]", snap.MinDataUs, snap.MaxDataUs)
	}
	if !snap.Clusters.Valid || math.Abs(snap.Clusters.ShortUs-350) > 10 || math.Abs(snap.Clusters.LongUs-1050) > 10 {
		t.Fatalf("unexpected clusters: %+v", snap.Clusters)
	}
	if len(snap.Histogram) != 24 {
		t.Fatalf("Histogram len = %d, want 24", len(snap.Histogram))
	}
	if snap.ShortBinIdx < 0 || snap.LongBinIdx < 0 || snap.ShortBinIdx >= snap.LongBinIdx {
		t.Fatalf("centroid bins invalid: short=%d long=%d", snap.ShortBinIdx, snap.LongBinIdx)
	}
	if snap.HistPeakCount <= 0 {
		t.Fatalf("HistPeakCount = %d, want > 0", snap.HistPeakCount)
	}
	if snap.DutyCyclePct <= 0 || snap.DutyCyclePct >= 100 {
		t.Fatalf("DutyCyclePct = %.1f, want (0, 100)", snap.DutyCyclePct)
	}

	// Verify histogram counts match total non-sync, non-glitch pulses.
	sumBins := 0
	for _, b := range snap.Histogram {
		if b.TotalCount != b.HighCount+b.LowCount {
			t.Fatalf("bin total mismatch: %+v", b)
		}
		sumBins += b.TotalCount
	}
	wantData := snap.TotalPulses - snap.SyncGaps
	if sumBins != wantData {
		t.Fatalf("histogram sum = %d, want %d", sumBins, wantData)
	}
}

func TestBuildSignalSnapshot_AdversarialEdgeCases(t *testing.T) {
	cases := []struct {
		name     string
		raw      []int32
		bins     int
		wantBins int
		wantCnt  int
	}{
		{name: "nil input", raw: nil, bins: 0, wantBins: 0, wantCnt: 0},
		{name: "empty input", raw: []int32{}, bins: 16, wantBins: 0, wantCnt: 0},
		{name: "all zeros", raw: []int32{0, 0, 0}, bins: 16, wantBins: 0, wantCnt: 0},
		{name: "single high pulse", raw: []int32{420}, bins: 4, wantBins: minSnapshotHistBins, wantCnt: 1},
		{name: "bins clamped high", raw: []int32{300, -300, 300, -300}, bins: 999, wantBins: maxSnapshotHistBins, wantCnt: 4},
		{name: "only sync gaps", raw: []int32{-12000, -15000, 9000}, bins: 12, wantBins: 12, wantCnt: 3},
		{name: "only glitches", raw: []int32{10, -20, 30, -15}, bins: 12, wantBins: 12, wantCnt: 4},
		{name: "int32 min and max boundaries", raw: []int32{math.MinInt32, math.MaxInt32, 350, -700}, bins: 16, wantBins: 16, wantCnt: 4},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			snap := BuildSignalSnapshot(tc.raw, 433_920_000, "AM650", tc.bins)
			if snap.TotalPulses != tc.wantCnt {
				t.Fatalf("TotalPulses = %d, want %d", snap.TotalPulses, tc.wantCnt)
			}
			if len(snap.Histogram) != tc.wantBins {
				t.Fatalf("len(Histogram) = %d, want %d", len(snap.Histogram), tc.wantBins)
			}
			if snap.TotalDurationUs < 0 {
				t.Fatalf("TotalDurationUs negative: %d", snap.TotalDurationUs)
			}
			if snap.DutyCyclePct < 0 || snap.DutyCyclePct > 100 {
				t.Fatalf("DutyCyclePct out of [0,100]: %.2f", snap.DutyCyclePct)
			}
		})
	}
}

func TestSignalSnapshot_WindowAndSlice(t *testing.T) {
	raw := []int32{-10000, 350, -1050, 1050, -350, 350, -1050, -10000, 350, -1050, 1050, -350}
	snap := BuildSignalSnapshot(raw, 433_920_000, "AM650", 24)

	win := snap.RecommendedWindowUs()
	if win < minSnapshotSpanUs || win > snap.TotalDurationUs {
		t.Fatalf("RecommendedWindowUs = %d out of [%d..%d]", win, minSnapshotSpanUs, snap.TotalDurationUs)
	}

	// Slice around the first data burst (starts at 10000us).
	segs := snap.SliceWaveformWindow(10000, 3000)
	if len(segs) == 0 {
		t.Fatalf("expected segments in [10000..13000]us")
	}
	if segs[0].StartUs != 10000 || !segs[0].High || segs[0].DurationUs != 350 {
		t.Fatalf("unexpected first segment in window: %+v", segs[0])
	}

	// Negative start clamps to 0; non-positive span returns nil.
	if got := snap.SliceWaveformWindow(-500, 12000); len(got) == 0 || got[0].StartUs != 0 {
		t.Fatalf("negative start did not clamp to 0: %+v", got)
	}
	if got := snap.SliceWaveformWindow(0, 0); got != nil {
		t.Fatalf("zero span returned %v, want nil", got)
	}
	if got := (SignalSnapshot{}).RecommendedWindowUs(); got != minSnapshotSpanUs {
		t.Fatalf("empty RecommendedWindowUs = %d, want %d", got, minSnapshotSpanUs)
	}
}

func FuzzBuildSignalSnapshot(f *testing.F) {
	f.Add([]byte{0x01, 0x5E, 0xFE, 0x90, 0x04, 0x1A}, 24)
	f.Add([]byte{}, 0)
	f.Add([]byte{0xFF, 0xFF, 0x80, 0x00}, 100)

	f.Fuzz(func(t *testing.T, data []byte, bins int) {
		raw := make([]int32, 0, len(data)/2)
		for i := 0; i+1 < len(data); i += 2 {
			v := int16(uint16(data[i])<<8 | uint16(data[i+1]))
			raw = append(raw, int32(v)*8)
		}
		snap := BuildSignalSnapshot(raw, 433_920_000, "AM650", bins)
		if snap.DutyCyclePct < 0 || snap.DutyCyclePct > 100.1 {
			t.Fatalf("invalid duty cycle: %f", snap.DutyCyclePct)
		}
		_ = snap.SliceWaveformWindow(0, snap.RecommendedWindowUs())
	})
}
