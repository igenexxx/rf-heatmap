package rfml

import (
	"math"
	"testing"
	"time"
)

func TestKalmanRSSI(t *testing.T) {
	// Sanitization of invalid Q/R.
	kf := NewKalmanRSSI(math.NaN(), -5)
	if st := kf.State(); st.Initialized || st.Trend != "STEADY" {
		t.Fatalf("unexpected initial state: %+v", st)
	}

	// Steady noisy signal around -80 dBm (±6 dBm) should converge near -80 dBm.
	var st KalmanState
	for i := range 20 {
		v := int32(-80)
		if i%2 == 0 {
			v = -74
		} else {
			v = -86
		}
		st = kf.Update(v)
	}
	if math.Abs(st.FilteredDBm-(-80.0)) > 4.0 {
		t.Fatalf("expected filtered ~-80 dBm, got %.1f", st.FilteredDBm)
	}

	// Approaching transmitter: rising RSSI -> CLOSER.
	kf.Reset()
	for v := int32(-95); v <= -50; v += 3 {
		st = kf.Update(v)
	}
	if st.Trend != "CLOSER" || st.ProximityPct < 50 {
		t.Fatalf("expected CLOSER with high proximity, got %+v", st)
	}

	// Walking away: falling RSSI -> FARTHER.
	for v := int32(-50); v >= -95; v -= 3 {
		st = kf.Update(v)
	}
	if st.Trend != "FARTHER" {
		t.Fatalf("expected FARTHER, got %+v", st)
	}
}

func TestPeriodicityTracker(t *testing.T) {
	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)

	t.Run("quiet channel", func(t *testing.T) {
		pt := NewPeriodicityTracker(0)
		var rep ActivityReport
		for i := range 25 {
			rep = pt.Observe(t0.Add(time.Duration(i)*100*time.Millisecond), false)
		}
		if rep.Kind != "QUIET" || rep.DutyRatio != 0 {
			t.Fatalf("expected QUIET, got %+v", rep)
		}
	})

	t.Run("continuous CW jammer", func(t *testing.T) {
		pt := NewPeriodicityTracker(DefaultMinBurstGap)
		var rep ActivityReport
		for i := range 25 {
			rep = pt.Observe(t0.Add(time.Duration(i)*100*time.Millisecond), true)
		}
		if rep.Kind != "CW" || rep.DutyRatio < 0.99 {
			t.Fatalf("expected CW, got %+v", rep)
		}
	})

	t.Run("periodic 15s weather beacon", func(t *testing.T) {
		pt := NewPeriodicityTracker(DefaultMinBurstGap)
		var rep ActivityReport
		for b := range 4 {
			ts := t0.Add(time.Duration(b) * 15 * time.Second)
			rep = pt.Observe(ts, true)
			rep = pt.Observe(ts.Add(200*time.Millisecond), false)
		}
		if rep.Kind != "BEACON" || math.Abs(rep.PeriodSec-15.0) > 0.2 || rep.CV > 0.05 {
			t.Fatalf("expected BEACON 15.0s, got %+v", rep)
		}
	})

	t.Run("irregular manual remote presses", func(t *testing.T) {
		pt := NewPeriodicityTracker(DefaultMinBurstGap)
		offsets := []time.Duration{0, 1 * time.Second, 9 * time.Second, 11 * time.Second}
		var rep ActivityReport
		for _, off := range offsets {
			rep = pt.Observe(t0.Add(off), true)
			rep = pt.Observe(t0.Add(off+150*time.Millisecond), false)
		}
		if rep.Kind != "BURST" {
			t.Fatalf("expected irregular BURST, got %+v", rep)
		}
		pt.Reset()
		if pt.Report().Kind != "QUIET" {
			t.Fatal("expected QUIET after Reset")
		}
	})
}

func FuzzKalmanRSSI(f *testing.F) {
	f.Add([]byte{100, 110, 90, 150, 200})
	f.Fuzz(func(t *testing.T, raw []byte) {
		kf := NewKalmanRSSI(DefaultKalmanQ, DefaultKalmanR)
		for _, b := range raw {
			st := kf.Update(int32(b) - 180)
			if math.IsNaN(st.FilteredDBm) || st.ProximityPct < 0 || st.ProximityPct > 100 {
				t.Fatalf("invalid KalmanState: %+v", st)
			}
		}
	})
}

func BenchmarkFoxhuntStep(b *testing.B) {
	kf := NewKalmanRSSI(DefaultKalmanQ, DefaultKalmanR)
	pt := NewPeriodicityTracker(DefaultMinBurstGap)
	now := time.Now()
	b.ReportAllocs()
	i := 0
	for b.Loop() {
		_ = kf.Update(-82 + int32(i%7))
		_ = pt.Observe(now.Add(time.Duration(i)*50*time.Millisecond), i%10 == 0)
		i++
	}
}
