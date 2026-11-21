package rfml

import "testing"

func TestGeigerCadenceAndFoxhuntTracker(t *testing.T) {
	// Below floor + 4 dB -> silent.
	if hz, ms := GeigerCadence(-101, -105); hz != 0 || ms != 0 {
		t.Fatalf("expected silent Geiger at floor+4, got (%.1fHz, %dms)", hz, ms)
	}
	// Strong signal >= -35 dBm -> 20 Hz / 50 ms.
	if hz, ms := GeigerCadence(-30, -105); hz != 20.0 || ms != 50 {
		t.Fatalf("expected max Geiger 20Hz/50ms, got (%.1fHz, %dms)", hz, ms)
	}
	// Monotonic progression.
	prevHz := 0.0
	for rssi := int32(-100); rssi <= -35; rssi += 5 {
		hz, ms := GeigerCadence(rssi, -105)
		if hz < prevHz || ms < 50 || ms > 1000 {
			t.Fatalf("non-monotonic Geiger at %d dBm: hz=%.1f prev=%.1f ms=%d", rssi, hz, prevHz, ms)
		}
		prevHz = hz
	}

	ft := NewFoxhuntTracker(DefaultKalmanQ, DefaultKalmanR)
	ft.Reset(433_920_000)
	if ft.LockedFreq() != 433_920_000 {
		t.Fatalf("LockedFreq = %d", ft.LockedFreq())
	}
	s1 := ft.Update(-80, -105)
	s2 := ft.Update(-55, -105)
	s3 := ft.Update(-70, -105)
	if s1.PeakDBm != -80 || s2.PeakDBm != -55 || s3.PeakDBm != -55 {
		t.Fatalf("peak-hold tracking broken: %d -> %d -> %d", s1.PeakDBm, s2.PeakDBm, s3.PeakDBm)
	}
	ft.ResetPeak()
	s4 := ft.Update(-90, -105)
	if s4.PeakDBm != -90 {
		t.Fatalf("ResetPeak failed: peak=%d", s4.PeakDBm)
	}

	// Zero allocations on hot path.
	allocs := testing.AllocsPerRun(200, func() {
		_ = ft.Update(-62, -105)
	})
	if allocs != 0 {
		t.Fatalf("FoxhuntTracker.Update allocated %.1f times/op, want 0", allocs)
	}
}

func BenchmarkFoxhuntTrackerUpdate(b *testing.B) {
	ft := NewFoxhuntTracker(DefaultKalmanQ, DefaultKalmanR)
	ft.Reset(433_920_000)
	var sink FoxhuntSample
	for b.Loop() {
		sink = ft.Update(-64, -105)
	}
	_ = sink
}
