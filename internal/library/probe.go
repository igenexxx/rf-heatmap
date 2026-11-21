package library

import (
	"fmt"
	"time"
)

// ProbeStatus represents the Auto-Probe execution state.
type ProbeStatus int

const (
	ProbeIdle ProbeStatus = iota
	ProbeRunning
	ProbePaused
	ProbeDone
)

// String returns the short LCD badge label for ProbeStatus.
func (s ProbeStatus) String() string {
	switch s {
	case ProbeRunning:
		return "RUN"
	case ProbePaused:
		return "PAUSE"
	case ProbeDone:
		return "DONE"
	default:
		return "IDLE"
	}
}

const maxRecentSent = 12

// IntervalPresets defines the SSOT discrete intervals available in the PROBE UI.
var IntervalPresets = []time.Duration{
	50 * time.Millisecond,
	100 * time.Millisecond,
	300 * time.Millisecond,
	500 * time.Millisecond,
	1000 * time.Millisecond,
	1500 * time.Millisecond,
	2000 * time.Millisecond,
	3000 * time.Millisecond,
	5000 * time.Millisecond,
}

// ProbeSession manages the Auto-Probe cursor, interval timing, pause/rewind state,
// and recent transmitted entry markers for human reaction-time backtracking.
type ProbeSession struct {
	Pack        PackInfo
	Catalog     Catalog
	CursorIdx   int
	LastSentIdx int
	RecentSent  []int
	IntervalIdx int
	Status      ProbeStatus
	ActiveTX    bool
	NextDueAt   time.Time
}

// NewProbeSession creates an idle ProbeSession with the default 1.0s interval.
func NewProbeSession(defaultIntervalMs int) *ProbeSession {
	ps := &ProbeSession{
		LastSentIdx: -1,
		IntervalIdx: 4, // 1000ms
		Status:      ProbeIdle,
	}
	ps.SetIntervalMs(defaultIntervalMs)
	return ps
}

// SetCatalog loads a pack and its entries slice, resetting cursor and probe history.
func (p *ProbeSession) SetCatalog(pack PackInfo, entries []EntryRef) {
	p.SetCatalogSource(pack, SliceCatalog(entries))
}

// SetCatalogSource loads a pack and any O(1) Catalog (disk or virtual generator).
func (p *ProbeSession) SetCatalogSource(pack PackInfo, cat Catalog) {
	p.Pack = pack
	p.Catalog = cat
	p.CursorIdx = 0
	p.LastSentIdx = -1
	p.RecentSent = nil
	p.Status = ProbeIdle
	p.ActiveTX = false
	p.NextDueAt = time.Time{}
}

// EntryCount returns the number of entries in the active catalog.
func (p *ProbeSession) EntryCount() int {
	if p.Catalog == nil {
		return 0
	}
	return p.Catalog.Len()
}

// EntryAt returns the entry at idx from the active catalog.
func (p *ProbeSession) EntryAt(idx int) (EntryRef, bool) {
	if p.Catalog == nil {
		return EntryRef{}, false
	}
	return p.Catalog.EntryAt(idx)
}

// Interval returns the active delay between consecutive auto-probe transmissions.
func (p *ProbeSession) Interval() time.Duration {
	if p.IntervalIdx < 0 || p.IntervalIdx >= len(IntervalPresets) {
		return time.Second
	}
	return IntervalPresets[p.IntervalIdx]
}

// IntervalLabel returns a compact formatted interval label (e.g. "0.05s", "0.1s", "1.0s").
func (p *ProbeSession) IntervalLabel() string {
	d := p.Interval()
	if d < 100*time.Millisecond {
		return fmt.Sprintf("%.2fs", d.Seconds())
	}
	return fmt.Sprintf("%.1fs", d.Seconds())
}

// SetIntervalMs snaps ms to the nearest supported IntervalPresets entry.
func (p *ProbeSession) SetIntervalMs(ms int) {
	if ms <= 0 {
		p.IntervalIdx = 4
		return
	}
	target := time.Duration(ms) * time.Millisecond
	bestIdx := 0
	bestDiff := absDuration(IntervalPresets[0] - target)
	for i := 1; i < len(IntervalPresets); i++ {
		if d := absDuration(IntervalPresets[i] - target); d < bestDiff {
			bestDiff = d
			bestIdx = i
		}
	}
	p.IntervalIdx = bestIdx
}

// StepInterval adjusts the probe interval up (+1) or down (-1) within bounds.
func (p *ProbeSession) StepInterval(delta int) time.Duration {
	next := p.IntervalIdx + delta
	if next < 0 {
		next = 0
	}
	if next >= len(IntervalPresets) {
		next = len(IntervalPresets) - 1
	}
	p.IntervalIdx = next
	return p.Interval()
}

// Start begins or resumes Auto-Probe from the current CursorIdx immediately.
func (p *ProbeSession) Start(now time.Time) bool {
	n := p.EntryCount()
	if n == 0 {
		return false
	}
	if p.CursorIdx < 0 || p.CursorIdx >= n {
		p.CursorIdx = 0
	}
	p.Status = ProbeRunning
	p.NextDueAt = now
	return true
}

// Pause halts Auto-Probe and preserves CursorIdx, LastSentIdx, and RecentSent for rewinding.
func (p *ProbeSession) Pause() {
	if p.Status == ProbeRunning {
		p.Status = ProbePaused
	}
	p.ActiveTX = false
	p.NextDueAt = time.Time{}
}

// MoveCursor moves CursorIdx by delta (clamped to list bounds).
func (p *ProbeSession) MoveCursor(delta int) {
	n := p.EntryCount()
	if n == 0 {
		p.CursorIdx = 0
		return
	}
	next := p.CursorIdx + delta
	if next < 0 {
		next = 0
	}
	if next >= n {
		next = n - 1
	}
	p.CursorIdx = next
}

// CurrentEntry returns the entry under CursorIdx, if any.
func (p *ProbeSession) CurrentEntry() (EntryRef, bool) {
	return p.EntryAt(p.CursorIdx)
}

// PollDue returns the next entry to transmit if Status == ProbeRunning, no TX is active,
// and NextDueAt has been reached.
func (p *ProbeSession) PollDue(now time.Time) (EntryRef, int, bool) {
	n := p.EntryCount()
	if p.Status != ProbeRunning || p.ActiveTX || n == 0 {
		return EntryRef{}, -1, false
	}
	if p.CursorIdx >= n {
		p.Status = ProbeDone
		return EntryRef{}, -1, false
	}
	if !p.NextDueAt.IsZero() && now.Before(p.NextDueAt) {
		return EntryRef{}, -1, false
	}
	idx := p.CursorIdx
	ref, ok := p.EntryAt(idx)
	if !ok {
		return EntryRef{}, -1, false
	}
	return ref, idx, true
}

// OnTXStarted marks an in-flight transmission at idx and records it in RecentSent.
func (p *ProbeSession) OnTXStarted(idx int) {
	p.ActiveTX = true
	p.LastSentIdx = idx
	p.recordRecent(idx)
}

// OnTXDone handles TX completion; if still Running, advances CursorIdx and schedules NextDueAt.
func (p *ProbeSession) OnTXDone(idx int, now time.Time) {
	p.ActiveTX = false
	if p.Status != ProbeRunning {
		return
	}
	if idx+1 >= p.EntryCount() {
		p.Status = ProbeDone
		p.NextDueAt = time.Time{}
		return
	}
	p.CursorIdx = idx + 1
	p.NextDueAt = now.Add(p.Interval())
}

// IsRecentSent reports whether idx is in the recent transmission history (excluding LastSentIdx).
func (p *ProbeSession) IsRecentSent(idx int) bool {
	if idx == p.LastSentIdx {
		return false
	}
	for _, r := range p.RecentSent {
		if r == idx {
			return true
		}
	}
	return false
}

func (p *ProbeSession) recordRecent(idx int) {
	filtered := make([]int, 0, maxRecentSent)
	for _, r := range p.RecentSent {
		if r != idx {
			filtered = append(filtered, r)
		}
	}
	filtered = append(filtered, idx)
	if len(filtered) > maxRecentSent {
		filtered = filtered[len(filtered)-maxRecentSent:]
	}
	p.RecentSent = filtered
}

func absDuration(d time.Duration) time.Duration {
	if d < 0 {
		return -d
	}
	return d
}
