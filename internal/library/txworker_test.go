package library

import (
	"context"
	"errors"
	"testing"
	"time"

	"rf-heatmap/internal/subfile"
)

func TestTXWorkerSuccessAndCancellation(t *testing.T) {
	w := NewTXWorker(&SimTransmitter{FixedDelay: 10 * time.Millisecond})
	defer w.Close()

	sf := &subfile.SubFile{
		FreqHz:   433_920_000,
		Preset:   "FuriHalSubGhzPresetOok650Async",
		Protocol: "Princeton",
		Bit:      24,
		Key:      "00 00 00 00 00 A5 5A 01",
		TE:       350,
	}

	if !w.Submit(TXRequest{Entry: EntryRef{Name: "ok.sub"}, SubFile: sf, Index: 2, IsProbe: true}) {
		t.Fatal("Submit failed")
	}

	deadline := time.Now().Add(time.Second)
	for {
		if res, ok := w.PollResult(); ok {
			if res.Err != nil || res.Canceled || res.Index != 2 || !res.IsProbe {
				t.Fatalf("unexpected TXResult: %+v", res)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for TXResult")
		}
		time.Sleep(2 * time.Millisecond)
	}

	// Test immediate mid-TX cancellation.
	slow := NewTXWorker(&SimTransmitter{FixedDelay: 5 * time.Second})
	defer slow.Close()
	if !slow.Submit(TXRequest{Entry: EntryRef{Name: "slow.sub"}, SubFile: sf, Index: 0}) {
		t.Fatal("Submit slow failed")
	}
	time.Sleep(15 * time.Millisecond)
	slow.Cancel()

	deadline = time.Now().Add(time.Second)
	for {
		if res, ok := slow.PollResult(); ok {
			if !res.Canceled || !errors.Is(res.Err, context.Canceled) {
				t.Fatalf("expected canceled TXResult, got %+v", res)
			}
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("timed out waiting for canceled TXResult")
		}
		time.Sleep(2 * time.Millisecond)
	}
}
