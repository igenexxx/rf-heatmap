package rfml

import "math"

const (
	// DefaultKalmanQ is the process-noise variance (allows smooth tracking while walking).
	DefaultKalmanQ = 1.2
	// DefaultKalmanR is the CC1101 RSSI measurement-noise variance (~±4-6 dBm jitter).
	DefaultKalmanR = 18.0

	trendEpsilonDBm = 0.45
	velAlpha        = 0.35
)

// KalmanState holds the smoothed RSSI estimate and Foxhunt proximity trend.
type KalmanState struct {
	FilteredDBm  float64 `json:"filtered_dbm"`
	Velocity     float64 `json:"velocity_dbm"`
	Trend        string  `json:"trend"`         // "CLOSER", "FARTHER", "STEADY"
	ProximityPct int     `json:"proximity_pct"` // 0..100 mapped across [-105, -30] dBm
	Initialized  bool    `json:"initialized"`
}

// KalmanRSSI implements a zero-allocation 1D scalar Kalman filter for CC1101 RSSI Foxhunt.
type KalmanRSSI struct {
	q           float64
	r           float64
	x           float64 // Current filtered dBm estimate
	p           float64 // Estimate error covariance
	vel         float64 // Smoothed dBm velocity per step
	initialized bool
}

// NewKalmanRSSI creates a 1D Kalman filter with sanitized process (q) and measurement (r) noise.
func NewKalmanRSSI(q, r float64) *KalmanRSSI {
	if q <= 0 || math.IsNaN(q) || math.IsInf(q, 0) {
		q = DefaultKalmanQ
	}
	if r <= 0 || math.IsNaN(r) || math.IsInf(r, 0) {
		r = DefaultKalmanR
	}
	return &KalmanRSSI{q: q, r: r, p: r}
}

// Reset clears filter state (call when changing carrier frequency or ISM band).
func (k *KalmanRSSI) Reset() {
	k.x = 0
	k.p = k.r
	k.vel = 0
	k.initialized = false
}

// Update ingests one raw CC1101 RSSI sample (dBm) and returns the smoothed KalmanState.
func (k *KalmanRSSI) Update(rssiDBm int32) KalmanState {
	z := clampFloat(float64(rssiDBm), minValidRSSIDBm, maxValidRSSIDBm)
	if !k.initialized {
		k.x = z
		k.p = k.r
		k.vel = 0
		k.initialized = true
		return k.state()
	}

	prev := k.x
	// Predict step: covariance grows by process noise Q.
	pPred := k.p + k.q
	// Update step: compute Kalman gain K and refine estimate.
	gain := pPred / (pPred + k.r)
	k.x = k.x + gain*(z-k.x)
	k.p = (1.0 - gain) * pPred

	// Smoothed slope for CLOSER / FARTHER indicator.
	k.vel = (1.0-velAlpha)*k.vel + velAlpha*(k.x-prev)
	return k.state()
}

// State returns the current KalmanState without advancing the filter.
func (k *KalmanRSSI) State() KalmanState {
	return k.state()
}

func (k *KalmanRSSI) state() KalmanState {
	trend := "STEADY"
	if k.initialized {
		if k.vel >= trendEpsilonDBm {
			trend = "CLOSER"
		} else if k.vel <= -trendEpsilonDBm {
			trend = "FARTHER"
		}
	}
	return KalmanState{
		FilteredDBm:  math.Round(k.x*10) / 10,
		Velocity:     math.Round(k.vel*100) / 100,
		Trend:        trend,
		ProximityPct: rssiToProximityPct(k.x),
		Initialized:  k.initialized,
	}
}

func rssiToProximityPct(dbm float64) int {
	t := (dbm + 105.0) / 75.0
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return 100
	}
	return int(math.Round(t * 100.0))
}
