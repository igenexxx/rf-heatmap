package rfml

import (
	"fmt"
	"math"
	"time"
)

const (
	// DefaultMinBurstGap debounces closely spaced carrier edges into a single burst event.
	DefaultMinBurstGap = 250 * time.Millisecond

	dutyRingSize  = 64
	burstRingSize = 16
	cwDutyThresh  = 0.80
	beaconMaxCV   = 0.15
)

// ActivityReport summarizes temporal transmitter behavior over recent samples.
type ActivityReport struct {
	Kind       string  `json:"kind"`        // "QUIET", "BURST", "BEACON", "CW"
	Label      string  `json:"label"`       // Compact UI badge text
	PeriodSec  float64 `json:"period_sec"`  // Mean Inter-Arrival Time (s) when BEACON
	CV         float64 `json:"cv"`          // Coefficient of variation (std/mean)
	DutyRatio  float64 `json:"duty_ratio"`  // 0..1 fraction of active samples
	BurstCount int     `json:"burst_count"` // Tracked bursts in ring
}

// PeriodicityTracker classifies transmitter behavior (CW/Jammer, Periodic Beacon, Manual Burst, Quiet)
// using fixed-size ring buffers with zero heap allocations on Observe.
type PeriodicityTracker struct {
	minBurstGap time.Duration

	dutyRing  [dutyRingSize]bool
	dutyHead  int
	dutyCount int
	dutyOnes  int

	burstRing  [burstRingSize]time.Time
	burstHead  int
	burstCount int

	prevActive bool
	lastBurst  time.Time
}

// NewPeriodicityTracker creates a tracker with the given rising-edge debounce window.
func NewPeriodicityTracker(minBurstGap time.Duration) *PeriodicityTracker {
	if minBurstGap <= 0 {
		minBurstGap = DefaultMinBurstGap
	}
	return &PeriodicityTracker{minBurstGap: minBurstGap}
}

// Reset clears all tracked duty and burst history.
func (p *PeriodicityTracker) Reset() {
	clear(p.dutyRing[:])
	p.dutyHead = 0
	p.dutyCount = 0
	p.dutyOnes = 0
	clear(p.burstRing[:])
	p.burstHead = 0
	p.burstCount = 0
	p.prevActive = false
	p.lastBurst = time.Time{}
}

// Observe ingests one channel-activity sample (active == true when RSSI >= squelch).
func (p *PeriodicityTracker) Observe(now time.Time, active bool) ActivityReport {
	// Update 64-sample duty ring in O(1).
	if p.dutyCount == dutyRingSize {
		if p.dutyRing[p.dutyHead] {
			p.dutyOnes--
		}
	} else {
		p.dutyCount++
	}
	p.dutyRing[p.dutyHead] = active
	if active {
		p.dutyOnes++
	}
	p.dutyHead = (p.dutyHead + 1) % dutyRingSize

	// Detect debounced rising edge (quiet -> active).
	if active && !p.prevActive {
		if p.lastBurst.IsZero() || now.Sub(p.lastBurst) >= p.minBurstGap {
			p.burstRing[p.burstHead] = now
			p.burstHead = (p.burstHead + 1) % burstRingSize
			if p.burstCount < burstRingSize {
				p.burstCount++
			}
			p.lastBurst = now
		}
	}
	p.prevActive = active

	return p.Report()
}

// Report computes the current ActivityReport without mutating tracker state.
func (p *PeriodicityTracker) Report() ActivityReport {
	var duty float64
	if p.dutyCount > 0 {
		duty = float64(p.dutyOnes) / float64(p.dutyCount)
	}
	rep := ActivityReport{
		Kind:       "QUIET",
		Label:      "QUIET",
		DutyRatio:  math.Round(duty*1000) / 1000,
		BurstCount: p.burstCount,
	}
	// Continuous carrier / jammer check (requires at least 20 samples).
	if p.dutyCount >= 20 && duty >= cwDutyThresh {
		rep.Kind = "CW"
		rep.Label = "CW/JAMMER"
		return rep
	}
	if mean, cv, ok := p.computeBeaconIATStats(); ok {
		rep.PeriodSec = math.Round(mean*10) / 10
		rep.CV = math.Round(cv*1000) / 1000
		if mean >= 0.5 && cv <= beaconMaxCV {
			rep.Kind = "BEACON"
			rep.Label = fmt.Sprintf("BEACON %.1fs", rep.PeriodSec)
			return rep
		}
	}
	if p.burstCount > 0 {
		rep.Kind = "BURST"
		rep.Label = "BURST"
	}
	return rep
}

func (p *PeriodicityTracker) computeBeaconIATStats() (float64, float64, bool) {
	if p.burstCount < 3 {
		return 0, 0, false
	}
	start := (p.burstHead - p.burstCount + burstRingSize) % burstRingSize
	var sum float64
	var iats [burstRingSize]float64
	valid := 0
	for i := range p.burstCount - 1 {
		t0 := p.burstRing[(start+i)%burstRingSize]
		t1 := p.burstRing[(start+i+1)%burstRingSize]
		if dt := t1.Sub(t0).Seconds(); dt > 0 {
			iats[valid] = dt
			sum += dt
			valid++
		}
	}
	if valid < 2 {
		return 0, 0, false
	}
	mean := sum / float64(valid)
	var sqSum float64
	for i := range valid {
		d := iats[i] - mean
		sqSum += d * d
	}
	std := math.Sqrt(sqSum / float64(valid))
	cv := 1.0
	if mean > 0 {
		cv = std / mean
	}
	return mean, cv, true
}
