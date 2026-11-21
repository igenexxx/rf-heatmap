package rfml

import (
	"math"
	"testing"
)

func TestDenoisePulses(t *testing.T) {
	tests := []struct {
		name         string
		raw          []int32
		minGlitch    int32
		wantLen      int
		wantGlitches int
		wantFirst    int32
	}{
		{
			name:         "nil slice",
			raw:          nil,
			wantLen:      0,
			wantGlitches: 0,
		},
		{
			name:         "merges internal dropout glitch into surrounding high pulse",
			raw:          []int32{500, -25, 525, -350},
			minGlitch:    60,
			wantLen:      2,
			wantGlitches: 1,
			wantFirst:    1050,
		},
		{
			name:         "strips zero pulses and merges same sign",
			raw:          []int32{0, 350, 0, 350, -1050},
			minGlitch:    60,
			wantLen:      2,
			wantGlitches: 2,
			wantFirst:    700,
		},
		{
			name:         "preserves clean alternating waveform",
			raw:          []int32{-10500, 350, -1050, 1050, -350},
			minGlitch:    60,
			wantLen:      5,
			wantGlitches: 0,
			wantFirst:    -10500,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got, g := DenoisePulses(tc.raw, tc.minGlitch)
			if len(got) != tc.wantLen {
				t.Fatalf("len=%d want %d (got=%v)", len(got), tc.wantLen, got)
			}
			if g != tc.wantGlitches {
				t.Fatalf("glitches=%d want %d", g, tc.wantGlitches)
			}
			if tc.wantLen > 0 && got[0] != tc.wantFirst {
				t.Fatalf("first=%d want %d", got[0], tc.wantFirst)
			}
		})
	}
}

func TestClusterTimingsAndQuantize(t *testing.T) {
	// Build a noisy Princeton-like waveform (TE=350, long=1050, sync=-10500) with ±25% jitter + glitches.
	noisy := []int32{-10500}
	offsets := []int32{-60, 55, -40, 70, -50, 45}
	for i := range 24 {
		off := offsets[i%len(offsets)]
		if i%2 == 0 {
			// 1:3 bit with a 20us EMI glitch inside every 6th long gap.
			noisy = append(noisy, 350+off)
			if i%6 == 0 {
				noisy = append(noisy, -500, 20, -530+off)
			} else {
				noisy = append(noisy, -(1050 + off))
			}
		} else {
			noisy = append(noisy, 1050+off, -(350 + off))
		}
	}
	noisy = append(noisy, 350, -10800)

	cl := ClusterTimings(noisy, DefaultMinGlitchUs)
	if !cl.Valid {
		t.Fatalf("expected valid clusters, got %+v", cl)
	}
	if cl.ShortUs < 310 || cl.ShortUs > 390 {
		t.Fatalf("ShortUs=%.1f want ~350", cl.ShortUs)
	}
	if cl.LongUs < 960 || cl.LongUs > 1140 {
		t.Fatalf("LongUs=%.1f want ~1050", cl.LongUs)
	}
	if cl.SyncGaps != 2 || cl.SyncUs < 10000 {
		t.Fatalf("unexpected sync stats: gaps=%d us=%.1f", cl.SyncGaps, cl.SyncUs)
	}
	if cl.GlitchesRemoved != 4 {
		t.Fatalf("GlitchesRemoved=%d want 4", cl.GlitchesRemoved)
	}
	if cl.ModulationHint != "OOK PWM 1:3" {
		t.Fatalf("ModulationHint=%q want OOK PWM 1:3", cl.ModulationHint)
	}

	q := QuantizePulses(noisy, cl, DefaultMinGlitchUs)
	if len(q) != 51 {
		t.Fatalf("quantized len=%d want 51", len(q))
	}
	if q[0] != -10500 {
		t.Fatalf("sync gap altered: got %d want -10500", q[0])
	}
	// Verify every data pulse snapped cleanly to ±ShortUs or ±LongUs.
	shortI := int32(math.Round(cl.ShortUs))
	longI := int32(math.Round(cl.LongUs))
	for i := 1; i < len(q)-1; i++ {
		mag := int32(absInt64(q[i]))
		if mag != shortI && mag != longI {
			t.Fatalf("q[%d]=%d not quantized to %d or %d", i, q[i], shortI, longI)
		}
	}
}

func TestClusterTimingsEdgeCases(t *testing.T) {
	// Empty / too short.
	if cl := ClusterTimings(nil, 0); cl.Valid {
		t.Fatalf("expected invalid for nil: %+v", cl)
	}
	if cl := ClusterTimings([]int32{300, -300}, 60); cl.Valid {
		t.Fatalf("expected invalid for <6 data pulses: %+v", cl)
	}
	// Uniform pulses (Manchester-like).
	uniform := []int32{400, -400, 400, -400, 400, -400, 400, -400}
	cl := ClusterTimings(uniform, 60)
	if !cl.Valid || cl.ModulationHint != "Uniform / Manchester" {
		t.Fatalf("expected Uniform / Manchester, got %+v", cl)
	}
	// Quantize on invalid clusters returns nil.
	if q := QuantizePulses(uniform, PulseClusters{}, 60); q != nil {
		t.Fatalf("expected nil for invalid clusters, got %v", q)
	}
	// Extreme MinInt32 must not overflow or panic.
	_ = ClusterTimings([]int32{math.MinInt32, 300, -300, 900, -300, 300, -900, 300}, 60)
}

func FuzzClusterTimings(f *testing.F) {
	f.Add([]byte{10, 20, 30, 100, 200, 5, 100, 200})
	f.Fuzz(func(t *testing.T, data []byte) {
		raw := make([]int32, len(data))
		for i, b := range data {
			v := int32(b) * 20
			if i%2 == 1 {
				v = -v
			}
			raw[i] = v
		}
		cl := ClusterTimings(raw, DefaultMinGlitchUs)
		if math.IsNaN(cl.ShortUs) || math.IsNaN(cl.LongUs) || math.IsNaN(cl.JitterRatio) {
			t.Fatalf("NaN in clusters: %+v", cl)
		}
		if cl.Valid {
			_ = QuantizePulses(raw, cl, DefaultMinGlitchUs)
		}
	})
}

func BenchmarkClusterAndQuantize500(b *testing.B) {
	raw := make([]int32, 0, 500)
	for i := range 250 {
		if i%25 == 0 {
			raw = append(raw, -10500)
			continue
		}
		if i%2 == 0 {
			raw = append(raw, 340+int32(i%30), -(1030 + int32(i%40)))
		} else {
			raw = append(raw, 1040-int32(i%35), -(360 - int32(i%25)))
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		cl := ClusterTimings(raw, DefaultMinGlitchUs)
		_ = QuantizePulses(raw, cl, DefaultMinGlitchUs)
	}
}
