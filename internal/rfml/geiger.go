package rfml

import "math"

const (
	geigerFloorMarginDB = 4
	geigerMaxRSSIDBm    = -35
	geigerMinHz         = 1.0
	geigerMaxHz         = 20.0
)

// FoxhuntSample is a zero-allocation snapshot of live RSSI, peak-hold, Kalman state, and Geiger cadence.
type FoxhuntSample struct {
	RawDBm           int32       `json:"raw_dbm"`
	PeakDBm          int32       `json:"peak_dbm"`
	FloorDBm         int32       `json:"floor_dbm"`
	GeigerHz         float64     `json:"geiger_hz"`
	GeigerIntervalMs int         `json:"geiger_interval_ms"`
	Kalman           KalmanState `json:"kalman"`
}

// FoxhuntTracker wraps KalmanRSSI with peak-hold and monotonic Geiger cadence calculation.
type FoxhuntTracker struct {
	kalman     KalmanRSSI
	peakDBm    int32
	peakValid  bool
	lockedFreq uint32
}

// NewFoxhuntTracker initializes a zero-allocation FoxhuntTracker.
func NewFoxhuntTracker(q, r float64) *FoxhuntTracker {
	kf := NewKalmanRSSI(q, r)
	return &FoxhuntTracker{
		kalman:  *kf,
		peakDBm: -120,
	}
}

// LockedFreq returns the currently locked frequency in Hz.
func (t *FoxhuntTracker) LockedFreq() uint32 {
	return t.lockedFreq
}

// Reset clears Kalman filter and peak-hold state for a new carrier frequency.
func (t *FoxhuntTracker) Reset(freqHz uint32) {
	t.kalman.Reset()
	t.peakDBm = -120
	t.peakValid = false
	t.lockedFreq = freqHz
}

// ResetPeak clears the peak-hold marker and Kalman state while keeping the locked frequency.
func (t *FoxhuntTracker) ResetPeak() {
	t.kalman.Reset()
	t.peakDBm = -120
	t.peakValid = false
}

// Update ingests one raw RSSI sample and noise floor, returning a FoxhuntSample with 0 allocs/op.
func (t *FoxhuntTracker) Update(rssiDBm, floorDBm int32) FoxhuntSample {
	clamped := clampRSSI(rssiDBm)
	floor := clampRSSI(floorDBm)
	ks := t.kalman.Update(clamped)
	if !t.peakValid || clamped > t.peakDBm {
		t.peakDBm = clamped
		t.peakValid = true
	}
	hz, intervalMs := GeigerCadence(int32(math.Round(ks.FilteredDBm)), floor)
	return FoxhuntSample{
		RawDBm:           clamped,
		PeakDBm:          t.peakDBm,
		FloorDBm:         floor,
		GeigerHz:         hz,
		GeigerIntervalMs: intervalMs,
		Kalman:           ks,
	}
}

// GeigerCadence maps live RSSI relative to floorDBm into a monotonic click rate (0..20 Hz)
// and tick interval in milliseconds (0 when silent, 50..1000 ms when active).
func GeigerCadence(rssiDBm, floorDBm int32) (hz float64, intervalMs int) {
	clamped := clampRSSI(rssiDBm)
	floor := clampRSSI(floorDBm)
	thresh := floor + geigerFloorMarginDB
	if clamped <= thresh {
		return 0, 0
	}
	span := float64(geigerMaxRSSIDBm - thresh)
	if span <= 1 {
		return geigerMaxHz, int(math.Round(1000.0 / geigerMaxHz))
	}
	t := float64(clamped-thresh) / span
	if t > 1.0 {
		t = 1.0
	}
	hz = geigerMinHz + t*(geigerMaxHz-geigerMinHz)
	hz = math.Round(hz*10) / 10
	intervalMs = int(math.Round(1000.0 / hz))
	if intervalMs < 50 {
		intervalMs = 50
	}
	if intervalMs > 1000 {
		intervalMs = 1000
	}
	return hz, intervalMs
}

func clampRSSI(dbm int32) int32 {
	if dbm < -120 {
		return -120
	}
	if dbm > 0 {
		return 0
	}
	return dbm
}
