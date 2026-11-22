package rfml

import (
	"math"
	"testing"
)

func TestClassifySignalClasses(t *testing.T) {
	// 1. Fixed OOK (Princeton 24-bit repeated 3 times, TE=350us, 433.92 MHz, high SNR).
	fixed := make([]int32, 0, 160)
	for range 3 {
		fixed = append(fixed, -10500)
		for b := 23; b >= 0; b-- {
			if (0xA55A01>>uint(b))&1 == 1 {
				fixed = append(fixed, 1050, -350)
			} else {
				fixed = append(fixed, 350, -1050)
			}
		}
		fixed = append(fixed, 350)
	}

	// 2. Continuous / Jammer (high duty cycle, long carrier pulses, no sync gaps).
	jammer := []int32{2200, -180, 2400, -150, 2300, -160, 2500, -140, 2100, -170, 2400, -150}

	// 3. Noise (high glitch rate, random unstructured widths, low SNR).
	noise := []int32{25, -40, 190, -15, 780, -30, 120, -20, 1450, -35, 95, -18, 620, -22, 310, -45, 980, -28}

	// 4. Sensor / Weather (Oregon/LaCrosse-style 36-bit telemetry, 500us mark / 2000-4000us spaces, 868.35 MHz).
	sensor := make([]int32, 0, 160)
	for range 2 {
		for i := range 36 {
			if (i*5+1)%3 == 0 {
				sensor = append(sensor, 500, -4000)
			} else {
				sensor = append(sensor, 500, -2000)
			}
		}
		sensor = append(sensor, 500, -9000)
	}

	// 5. Rolling / KeyFob (Microchip HCS301 KeeLoq: 11x preamble + 4000us header gap + 66-bit PWM payload).
	rolling := make([]int32, 0, 180)
	for range 11 {
		rolling = append(rolling, 400, -400)
	}
	rolling = append(rolling, 400, -4000)
	const keeloqKey uint64 = 0x1A2B3C4D5E6F7891
	for b := range 66 {
		if (keeloqKey>>uint(b%64))&1 == 1 {
			rolling = append(rolling, 400, -800)
		} else {
			rolling = append(rolling, 800, -400)
		}
	}
	rolling = append(rolling, 400, -15000)

	tests := []struct {
		name    string
		pulses  []int32
		freqHz  uint32
		snrDB   float64
		wantCls SignalClass
	}{
		{"Fixed OOK Princeton", fixed, 433_920_000, 35, ClassFixedOOK},
		{"Rolling KeyFob KeeLoq", rolling, 433_920_000, 34, ClassRollingFob},
		{"Continuous Jammer", jammer, 433_920_000, 40, ClassJammer},
		{"Atmospheric Noise", noise, 433_920_000, 3, ClassNoise},
		{"Weather Sensor 868", sensor, 868_350_000, 24, ClassSensor},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pred, _, _ := ClassifyPulses(tc.pulses, tc.freqHz, tc.snrDB)
			var probSum float32
			for _, p := range pred.Probs {
				probSum += p
			}
			if math.Abs(float64(probSum-1.0)) > 1e-3 {
				t.Fatalf("probabilities do not sum to 1: sum=%v probs=%v", probSum, pred.Probs)
			}
			if pred.Class != tc.wantCls {
				t.Fatalf("got class=%s (%.1f%%) want %s (probs=%v)", pred.Label, pred.Confidence*100, tc.wantCls, pred.Probs)
			}
		})
	}
}

func TestClassifyAdversarialInputs(t *testing.T) {
	// Empty / nil pulses must not panic and should classify as Noise.
	pred, _, _ := ClassifyPulses(nil, 0, math.NaN())
	if pred.Label == "" || pred.Confidence <= 0 {
		t.Fatalf("invalid prediction on nil pulses: %+v", pred)
	}

	// NaN / Inf feature vector must be sanitized cleanly.
	var bad FeatureVector
	for i := range bad {
		bad[i] = float32(math.NaN())
	}
	bad[0] = float32(math.Inf(1))
	bad[1] = float32(math.Inf(-1))
	p := Classify(bad)
	if math.IsNaN(float64(p.Confidence)) || p.Confidence <= 0 || p.Confidence > 1.01 {
		t.Fatalf("bad confidence on NaN/Inf features: %+v", p)
	}
	if SignalClass(99).String() != "Unknown" {
		t.Fatal("expected Unknown for out-of-range SignalClass")
	}
}

func FuzzClassifyPulses(f *testing.F) {
	f.Add([]byte{10, 20, 30, 80, 120, 200}, uint32(433_920_000), float64(20))
	f.Fuzz(func(t *testing.T, data []byte, freqHz uint32, snr float64) {
		raw := make([]int32, len(data))
		for i, b := range data {
			v := int32(b) * 25
			if i%2 == 1 {
				v = -v
			}
			raw[i] = v
		}
		pred, _, _ := ClassifyPulses(raw, freqHz, snr)
		if math.IsNaN(float64(pred.Confidence)) || pred.Confidence <= 0 || pred.Confidence > 1.01 {
			t.Fatalf("invalid confidence: %+v", pred)
		}
	})
}

func BenchmarkClassifyMLP(b *testing.B) {
	feat := FeatureVector{0.5, 0.7, 0.23, 0.35, 0.6, 0.8, 0.05, 0.45, 0.55, 0.4, 0.7, 0.01}
	b.ReportAllocs()
	for b.Loop() {
		_ = Classify(feat)
	}
}
