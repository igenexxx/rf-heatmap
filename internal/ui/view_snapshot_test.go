package ui

import (
	"os"
	"path/filepath"
	"testing"

	"rf-heatmap/internal/subfile"
)

func TestSignalSnapshotFlowFromReadSavedAndPulse(t *testing.T) {
	tmpDir := t.TempDir()
	keySub := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   433_920_000,
		Preset:   "FuriHalSubGhzPresetOok650Async",
		Protocol: "Princeton",
		Bit:      24,
		Key:      "00 00 00 00 00 A5 5A 01",
		TE:       350,
	}
	if err := os.WriteFile(filepath.Join(tmpDir, "gate.sub"), []byte(keySub.Format()), 0o644); err != nil {
		t.Fatalf("write sample sub: %v", err)
	}

	app := NewApp(nil, tmpDir)
	defer app.Close()
	c, _ := NewCanvas("")

	// 1. From Read & Replay Step 2 (7 SNAP).
	app.enterReadWizard()
	app.CapturedRaw = []int32{
		-10850, 350, -1050, 1050, -350, 350, -1050, 1050, -350,
		350, -1050, 1050, -350, 350, -1050, 1050, -350, 350, -10850,
	}
	app.analyzeCapturedRaw()
	app.ReadCaptured = true
	app.HandleKey(KeyNav7)
	if !app.ShowSnapshot {
		t.Fatalf("expected ShowSnapshot=true after KeyNav7 on Read Step 2")
	}
	if app.SnapData.TotalPulses != len(app.CapturedRaw) {
		t.Fatalf("SnapData.TotalPulses = %d, want %d", app.SnapData.TotalPulses, len(app.CapturedRaw))
	}

	// Render SPLIT, WAVE, and HIST layouts plus zoom/pan/TX.
	for _, wantLayout := range []SnapshotLayout{SnapshotLayoutSplit, SnapshotLayoutWave, SnapshotLayoutHist} {
		if app.SnapLayout != wantLayout {
			t.Fatalf("SnapLayout = %v, want %v", app.SnapLayout, wantLayout)
		}
		app.Render(c)
		app.HandleKey(KeyNav4)
	}
	app.HandleKey(KeyNav5) // zoom 2x
	if app.SnapZoomIdx != 1 {
		t.Fatalf("SnapZoomIdx = %d, want 1", app.SnapZoomIdx)
	}
	app.HandleKey(KeyRight) // pan right
	if app.SnapPanPct != snapPanStep {
		t.Fatalf("SnapPanPct = %d, want %d", app.SnapPanPct, snapPanStep)
	}
	app.HandleKey(KeyLeft) // pan back
	if app.SnapPanPct != 0 {
		t.Fatalf("SnapPanPct = %d, want 0", app.SnapPanPct)
	}
	app.HandleKey(KeyNav6) // TX preview
	app.HandleKey(KeyBack) // Back to ModeRead
	if app.ShowSnapshot || app.Mode != ModeRead {
		t.Fatalf("expected return to ModeRead, got show=%v mode=%v", app.ShowSnapshot, app.Mode)
	}

	// 2. From ModeSaved on a Key .sub file (synthesizes pulses automatically via V).
	app.SwitchMode(ModeSaved)
	app.HandleKey(KeyInspect)
	if !app.ShowSnapshot || app.SnapData.TotalPulses == 0 {
		t.Fatalf("expected ShowSnapshot=true with synthesized pulses for Key .sub, got show=%v pulses=%d",
			app.ShowSnapshot, app.SnapData.TotalPulses)
	}
	app.Render(c)

	// 3. Jump from Snapshot directly into RAW Trimmer (7 TRIM) and back to Snapshot (V).
	app.HandleKey(KeyNav7)
	if !app.ShowTrimmer || app.ShowSnapshot {
		t.Fatalf("expected transition Snapshot -> Trimmer, got trim=%v snap=%v", app.ShowTrimmer, app.ShowSnapshot)
	}
	app.HandleKey(KeyInspect)
	if !app.ShowSnapshot || app.ShowTrimmer {
		t.Fatalf("expected transition Trimmer -> Snapshot, got trim=%v snap=%v", app.ShowTrimmer, app.ShowSnapshot)
	}
	app.HandleKey(KeyNav8)
	if app.ShowSnapshot || app.Mode != ModeSaved {
		t.Fatalf("expected return to ModeSaved, got show=%v mode=%v", app.ShowSnapshot, app.Mode)
	}

	// 4. From ModePulse via V.
	app.SwitchMode(ModePulse)
	app.HandleKey(KeyInspect)
	if !app.ShowSnapshot {
		t.Fatalf("expected ShowSnapshot=true from ModePulse with CapturedRaw")
	}
	app.ExitSnapshot()
	if app.ShowSnapshot || app.Mode != ModePulse {
		t.Fatalf("expected return to ModePulse, got show=%v mode=%v", app.ShowSnapshot, app.Mode)
	}
}
