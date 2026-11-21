package library

import (
	"testing"
	"time"
)

func TestProbeSessionAutoStepPauseRewind(t *testing.T) {
	ps := NewProbeSession(1000)
	entries := []EntryRef{
		{Name: "sig_01.sub", PackID: "db:fans", Source: SourceExternal, ReadOnly: true},
		{Name: "sig_02.sub", PackID: "db:fans", Source: SourceExternal, ReadOnly: true},
		{Name: "sig_03.sub", PackID: "db:fans", Source: SourceExternal, ReadOnly: true},
		{Name: "sig_04.sub", PackID: "db:fans", Source: SourceExternal, ReadOnly: true},
	}
	ps.SetCatalog(PackInfo{ID: "db:fans", Name: "fans", Source: SourceExternal, ReadOnly: true}, entries)

	t0 := time.Date(2026, 11, 21, 12, 0, 0, 0, time.UTC)
	if !ps.Start(t0) || ps.Status != ProbeRunning {
		t.Fatalf("expected ProbeRunning after Start, got %v", ps.Status)
	}

	// Step 0 fires immediately.
	ref, idx, ok := ps.PollDue(t0)
	if !ok || idx != 0 || ref.Name != "sig_01.sub" {
		t.Fatalf("PollDue(t0) = (%q, %d, %v), want (sig_01.sub, 0, true)", ref.Name, idx, ok)
	}
	ps.OnTXStarted(0)
	// While ActiveTX is true, PollDue must return false.
	if _, _, due := ps.PollDue(t0.Add(5 * time.Second)); due {
		t.Fatal("PollDue must not fire while ActiveTX is true")
	}
	ps.OnTXDone(0, t0.Add(50*time.Millisecond))
	if ps.CursorIdx != 1 {
		t.Fatalf("expected CursorIdx=1 after TX 0 done, got %d", ps.CursorIdx)
	}

	// Before interval elapses, PollDue returns false.
	if _, _, due := ps.PollDue(t0.Add(500 * time.Millisecond)); due {
		t.Fatal("PollDue fired before interval elapsed")
	}
	// After 1000ms interval, Step 1 fires.
	t1 := t0.Add(1050 * time.Millisecond)
	_, idx, ok = ps.PollDue(t1)
	if !ok || idx != 1 {
		t.Fatalf("expected step 1 due at t1, got idx=%d ok=%v", idx, ok)
	}
	ps.OnTXStarted(1)
	ps.OnTXDone(1, t1.Add(50*time.Millisecond))

	// User reacts and hits STOP -> Pause preserves LastSentIdx=1 and RecentSent=[0, 1].
	ps.Pause()
	if ps.Status != ProbePaused {
		t.Fatalf("expected ProbePaused, got %v", ps.Status)
	}
	if ps.LastSentIdx != 1 || !ps.IsRecentSent(0) || ps.IsRecentSent(1) {
		t.Fatalf("unexpected recent history: last=%d recent=%v", ps.LastSentIdx, ps.RecentSent)
	}

	// User walks back up to index 0 to test which signal triggered the device.
	ps.MoveCursor(-2)
	if ps.CursorIdx != 0 {
		t.Fatalf("expected CursorIdx=0 after rewind, got %d", ps.CursorIdx)
	}
	ps.MoveCursor(-5) // clamped at 0
	if ps.CursorIdx != 0 {
		t.Fatalf("expected CursorIdx clamped at 0, got %d", ps.CursorIdx)
	}

	// Resume from index 3 and reach ProbeDone.
	ps.MoveCursor(3)
	ps.Start(t1)
	ps.OnTXStarted(3)
	ps.OnTXDone(3, t1.Add(50*time.Millisecond))
	if ps.Status != ProbeDone {
		t.Fatalf("expected ProbeDone after last item, got %v", ps.Status)
	}
}

func TestProbeSessionIntervals(t *testing.T) {
	ps := NewProbeSession(0)
	if ps.Interval() != time.Second {
		t.Fatalf("default interval = %v, want 1s", ps.Interval())
	}
	ps.SetIntervalMs(250)
	if ps.Interval() != 300*time.Millisecond {
		t.Fatalf("snapped 250ms = %v, want 300ms", ps.Interval())
	}
	ps.StepInterval(-10)
	if ps.Interval() != 50*time.Millisecond || ps.IntervalLabel() != "0.05s" {
		t.Fatalf("clamped min = %v (%s), want 0.05s", ps.Interval(), ps.IntervalLabel())
	}
	ps.StepInterval(100)
	if ps.Interval() != 5*time.Second || ps.IntervalLabel() != "5.0s" {
		t.Fatalf("clamped max = %v (%s), want 5.0s", ps.Interval(), ps.IntervalLabel())
	}
}
