package rfml

import (
	"math"
	"testing"
)

func TestNewBinBaselineSanitization(t *testing.T) {
	tests := []struct {
		name     string
		cfg      BaselineConfig
		wantBins int
		wantK    float64
	}{
		{
			name:     "zero config gets safe defaults",
			cfg:      BaselineConfig{},
			wantBins: 1,
			wantK:    defaultSquelchK,
		},
		{
			name: "negative and NaN fields sanitized",
			cfg: BaselineConfig{
				Bins:      -5,
				Alpha:     math.NaN(),
				MinStdDBm: -1,
				FreezeZ:   math.Inf(1),
				SquelchK:  math.NaN(),
				LockZ:     0,
			},
			wantBins: 1,
			wantK:    defaultSquelchK,
		},
		{
			name: "bins clamped to MaxBaselineBins and SquelchK clamped",
			cfg: BaselineConfig{
				Bins:     999,
				SquelchK: 99.0,
			},
			wantBins: MaxBaselineBins,
			wantK:    maxSquelchK,
		},
		{
			name: "low SquelchK clamped to min",
			cfg: BaselineConfig{
				Bins:     36,
				SquelchK: 0.2,
			},
			wantBins: 36,
			wantK:    minSquelchK,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			b := NewBinBaseline(tc.cfg)
			if b.Bins() != tc.wantBins {
				t.Fatalf("Bins()=%d want %d", b.Bins(), tc.wantBins)
			}
			if b.SquelchK() != tc.wantK {
				t.Fatalf("SquelchK()=%v want %v", b.SquelchK(), tc.wantK)
			}
		})
	}
}

func TestBinBaselineWarmupAndBounds(t *testing.T) {
	b := NewBinBaseline(DefaultBaselineConfig(4))

	// Nil/empty sweeps and out-of-bounds queries must never panic.
	b.Update(nil)
	b.Update([]int32{})
	b.UpdateBin(-1, -90)
	b.UpdateBin(99, -90)
	if b.Ready(-1) || b.Ready(99) || b.Ready(0) {
		t.Fatal("expected Ready=false before warmup or out of bounds")
	}
	if got := b.SmartThreshold(0, -78); got != -78 {
		t.Fatalf("warmup SmartThreshold=%d want fallback -78", got)
	}
	if got := b.AnomalyScore(0, -40); got != 0 {
		t.Fatalf("warmup AnomalyScore=%v want 0", got)
	}
	if _, ok := b.DetectBurst([]int32{-40, -40, -40, -40}); ok {
		t.Fatal("expected no burst detection during warmup")
	}

	// Feed 5 quiet sweeps (-105..-103 dBm) to complete warmup.
	quiet := []int32{-105, -104, -105, -103}
	for range 5 {
		b.Update(quiet)
	}
	if !b.Ready(0) {
		t.Fatal("expected Ready=true after 5 samples")
	}
	// Threshold on quiet ~-105 dBm bin with MinStd=2.0, k=3.5 => ~-98 dBm.
	thr := b.SmartThreshold(0, -78)
	if thr < -102 || thr > -94 {
		t.Fatalf("unexpected quiet SmartThreshold=%d", thr)
	}

	// Reset must revert to warmup state.
	b.Reset()
	if b.Ready(0) || b.MeanDBm(0) != 0 {
		t.Fatalf("expected clean Reset state, ready=%v mean=%v", b.Ready(0), b.MeanDBm(0))
	}
}

func TestStaticInterfererVsRealBurst(t *testing.T) {
	b := NewBinBaseline(DefaultBaselineConfig(36))

	// Simulate an urban environment where bin 10 has a constant -65 dBm interferer,
	// while all other bins sit around -105 dBm.
	for step := range 20 {
		sweep := make([]int32, 36)
		for i := range sweep {
			sweep[i] = -105 + int32(step%3) // -105..-103
		}
		sweep[10] = -65 + int32(step%2) // Constant interferer on bin 10
		b.Update(sweep)
	}

	// Steady sweep with the static interferer on bin 10 should NOT trigger a burst.
	steady := make([]int32, 36)
	for i := range steady {
		steady[i] = -104
	}
	steady[10] = -64
	if evt, ok := b.DetectBurst(steady); ok {
		t.Fatalf("static interferer falsely triggered burst: %+v", evt)
	}

	// Now inject a real -62 dBm remote burst on quiet bin 18 (weaker than bin 10's -64 dBm
	// in absolute terms, but a huge +42 dB anomaly over bin 18's -104 dBm baseline!).
	burstSweep := append([]int32(nil), steady...)
	burstSweep[18] = -62
	evt, ok := b.DetectBurst(burstSweep)
	if !ok {
		t.Fatal("expected real burst on bin 18 to be detected")
	}
	if evt.Bin != 18 {
		t.Fatalf("detected bin=%d want 18 (z=%.2f)", evt.Bin, evt.ZScore)
	}
	if evt.ZScore < 15.0 {
		t.Fatalf("expected high ZScore > 15, got %.2f", evt.ZScore)
	}
}

func TestBurstFreezePreventsNoiseFloorPoisoning(t *testing.T) {
	b := NewBinBaseline(DefaultBaselineConfig(1))
	for range 10 {
		b.UpdateBin(0, -104)
	}
	meanBefore := b.MeanDBm(0)

	// Sustained strong transmission (-45 dBm) for 15 ticks must NOT drag the baseline up.
	for range 15 {
		b.UpdateBin(0, -45)
	}
	meanAfter := b.MeanDBm(0)
	if math.Abs(meanAfter-meanBefore) > 0.01 {
		t.Fatalf("baseline poisoned by burst: before=%.2f after=%.2f", meanBefore, meanAfter)
	}
}

func TestSetSquelchKBounds(t *testing.T) {
	b := NewBinBaseline(DefaultBaselineConfig(1))
	b.SetSquelchK(5.0)
	if b.SquelchK() != 5.0 {
		t.Fatalf("got k=%v want 5.0", b.SquelchK())
	}
	b.SetSquelchK(math.NaN())
	if b.SquelchK() != 5.0 {
		t.Fatalf("NaN mutated k to %v", b.SquelchK())
	}
	b.SetSquelchK(0.1)
	if b.SquelchK() != minSquelchK {
		t.Fatalf("got k=%v want %v", b.SquelchK(), minSquelchK)
	}
	b.SetSquelchK(42.0)
	if b.SquelchK() != maxSquelchK {
		t.Fatalf("got k=%v want %v", b.SquelchK(), maxSquelchK)
	}
}

func FuzzBinBaseline(f *testing.F) {
	f.Add([]byte{150, 151, 152, 200, 148, 149}, uint8(36))
	f.Add([]byte{0, 255, 128}, uint8(1))
	f.Fuzz(func(t *testing.T, raw []byte, bins uint8) {
		b := NewBinBaseline(DefaultBaselineConfig(int(bins)))
		sweep := make([]int32, len(raw))
		for i, v := range raw {
			sweep[i] = int32(v) - 200 // map into [-200, +55] dBm
		}
		for range 6 {
			b.Update(sweep)
		}
		_, _ = b.DetectBurst(sweep)
		for i := range sweep {
			thr := b.SmartThreshold(i, -78)
			if i < b.Bins() && b.Ready(i) && (thr < minThresholdDBm || thr > maxThresholdDBm) {
				t.Fatalf("threshold out of bounds: %d", thr)
			}
		}
	})
}

func BenchmarkBinBaselineUpdate36(b *testing.B) {
	bl := NewBinBaseline(DefaultBaselineConfig(36))
	sweep := make([]int32, 36)
	for i := range sweep {
		sweep[i] = -104 + int32(i%3)
	}
	for range 10 {
		bl.Update(sweep)
	}
	b.ReportAllocs()
	for b.Loop() {
		bl.Update(sweep)
		_, _ = bl.DetectBurst(sweep)
	}
}
