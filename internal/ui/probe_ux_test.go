package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rf-heatmap/internal/config"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/subfile"
)

func TestProbeModeWorkflowRewindAndPromote(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "captures")
	dbDir := filepath.Join(root, "databases")
	cfgPath := filepath.Join(root, "config.json")

	writeSub := func(path, key string) {
		t.Helper()
		sf := &subfile.SubFile{
			FileType: "Flipper SubGhz Key File",
			Version:  1,
			FreqHz:   433_920_000,
			Preset:   "FuriHalSubGhzPresetOok650Async",
			Protocol: "Princeton",
			Bit:      24,
			Key:      key,
			TE:       350,
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(sf.Format()), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	writeSub(filepath.Join(userDir, "own_gate.sub"), "00 00 00 00 00 11 22 33")
	writeSub(filepath.Join(dbDir, "fans", "fan_01_power.sub"), "00 00 00 00 00 AA BB 01")
	writeSub(filepath.Join(dbDir, "fans", "fan_02_low.sub"), "00 00 00 00 00 AA BB 02")
	writeSub(filepath.Join(dbDir, "fans", "fan_03_high.sub"), "00 00 00 00 00 AA BB 03")

	app := NewApp(nil, userDir)
	defer app.Close()
	app.SetConfigPath(cfgPath)
	app.SetLibraryStore(library.NewStore(userDir, dbDir, nil))
	app.TX.Close()
	app.TX = library.NewTXWorker(&library.SimTransmitter{FixedDelay: 5 * time.Millisecond})

	// SAVED must show ONLY the user capture, never external DB files.
	if len(app.SavedFiles) != 1 || app.SavedFiles[0].Name != "own_gate.sub" {
		t.Fatalf("SAVED leaked external files: %+v", app.SavedFiles)
	}

	// Switch to PROBE mode; first pack should be external DB:fans.
	app.Mode = ModeProbe
	app.refreshProbePacks()
	if app.Probe.Pack.ID != "db:fans" || app.Probe.EntryCount() != 3 {
		t.Fatalf("expected db:fans with 3 entries, got pack=%+v len=%d", app.Probe.Pack, app.Probe.EntryCount())
	}

	// Adjust interval down to 0.05s (Left 4 times: 1.0s -> 0.5s -> 0.3s -> 0.1s -> 0.05s) and verify persistence.
	for range 4 {
		app.HandleKey(KeyLeft)
	}
	if app.Probe.IntervalLabel() != "0.05s" {
		t.Fatalf("expected 0.05s interval, got %s", app.Probe.IntervalLabel())
	}
	loaded, err := config.Load(cfgPath)
	if err != nil || loaded.ProbeIntervalMs != 50 {
		t.Fatalf("expected persisted ProbeIntervalMs=50, got %+v err=%v", loaded, err)
	}

	// Start Auto-Probe (KeyNav4), step until first entry transmits and advances.
	app.HandleKey(KeyNav4)
	if app.Probe.Status != library.ProbeRunning {
		t.Fatalf("expected ProbeRunning, got %v", app.Probe.Status)
	}
	for range 20 {
		app.Step()
		if app.Probe.LastSentIdx == 0 && !app.TXBusy {
			break
		}
		time.Sleep(3 * time.Millisecond)
	}
	if app.Probe.LastSentIdx != 0 {
		t.Fatalf("expected LastSentIdx=0 after first probe TX, got %d", app.Probe.LastSentIdx)
	}

	// Hit Space to STOP immediately -> Paused, then walk back up (KeyUp) to #0 and single-TRY (Enter).
	app.HandleKey(KeySpace)
	if app.Probe.Status != library.ProbePaused {
		t.Fatalf("expected ProbePaused on Space, got %v", app.Probe.Status)
	}
	app.HandleKey(KeyUp)
	if app.Probe.CursorIdx != 0 {
		t.Fatalf("expected CursorIdx=0 after rewind, got %d", app.Probe.CursorIdx)
	}
	app.HandleKey(KeyEnter)
	if !strings.Contains(app.ToastText, "TX: fan_01_power.sub") {
		t.Fatalf("expected single-TRY toast, got %q", app.ToastText)
	}

	// Promote winning signal to User Library (KeyNav6).
	app.HandleKey(KeyNav6)
	if !strings.Contains(app.ToastText, "Saved: fan_01_power.sub") {
		t.Fatalf("expected promote toast, got %q", app.ToastText)
	}
	if len(app.SavedFiles) != 2 {
		t.Fatalf("expected 2 User Library entries after promote, got %d", len(app.SavedFiles))
	}

	// Cycle pack (KeyNav7) to GEN:CAME-12 (4096 keys), test B freq override & SAVE to USER.
	app.HandleKey(KeyNav7)
	if app.Probe.Pack.ID != library.PackIDGenCAME12_433 || app.Probe.EntryCount() != 4096 {
		t.Fatalf("expected GEN:CAME-12 (4096), got %+v len=%d", app.Probe.Pack, app.Probe.EntryCount())
	}
	app.HandleKey(KeyBand)
	if app.ActiveFreq != 868_350_000 {
		t.Fatalf("expected KeyBand on GEN:CAME-12 to step to 868.35 MHz, got %d", app.ActiveFreq)
	}
	app.HandleKey(KeyNav6) // Promote came12_000.sub to USER
	if !strings.Contains(app.ToastText, "Saved: came12_000.sub") {
		t.Fatalf("expected generated promote toast, got %q", app.ToastText)
	}
	if len(app.SavedFiles) != 3 {
		t.Fatalf("expected 3 User Library entries after promoting generated key, got %d", len(app.SavedFiles))
	}
	c, _ := NewCanvas("")
	app.Render(c)
}
