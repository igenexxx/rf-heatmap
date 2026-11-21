package ui

import (
	"strings"
	"testing"

	"rf-heatmap/internal/rfml"
)

func TestHeatmapRoomBaselineAndEMIDiff(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	app.BaselinesDir = t.TempDir()

	// Pressing D before any baseline exists must not enable DiffMode.
	app.HandleKey(KeyMark)
	if app.DiffMode {
		t.Fatal("expected DiffMode=false when no baseline exists")
	}
	if !strings.Contains(app.ToastText, "No baseline") {
		t.Fatalf("unexpected toast when missing baseline: %q", app.ToastText)
	}

	// Run quiet sweeps and capture baseline via B (KeyBand in HEATMAP).
	for range 6 {
		app.Step()
	}
	bandBefore := app.BandIdx
	app.HandleKey(KeyBand)
	if app.BandIdx != bandBefore {
		t.Fatalf("B in HEATMAP must capture baseline without cycling band: before=%d after=%d", bandBefore, app.BandIdx)
	}
	if app.ActiveBaseline == nil || app.ActiveBaseline.Name != liveBaselineName {
		t.Fatalf("expected ActiveBaseline %q, got %+v", liveBaselineName, app.ActiveBaseline)
	}

	// Verify baseline was persisted to disk and can be reloaded by a fresh App.
	loaded, err := rfml.LoadRoomBaseline(app.BaselinesDir, liveBaselineName)
	if err != nil || len(loaded.MeanDBm) != maxCols {
		t.Fatalf("LoadRoomBaseline failed: loaded=%+v err=%v", loaded, err)
	}

	// Toggle DIFF mode ON via D (KeyMark).
	app.HandleKey(KeyMark)
	if !app.DiffMode {
		t.Fatal("expected DiffMode=true after pressing D")
	}

	// Inject a strong +20 dB new emitter on bin 10 and verify classification & rendering.
	app.ActiveBaseline.MeanDBm[10] = -104
	app.ActiveBaseline.StdDBm[10] = 2.0
	app.SweepRSSI[10] = -70
	app.updateHeatmapDiff(maxCols)
	if len(app.DiffBins) != maxCols || app.DiffBins[10].Class != rfml.DiffNewEmitter {
		t.Fatalf("expected bin 10 DiffNewEmitter, got %+v", app.DiffBins[10])
	}

	c, err := NewCanvas("")
	if err != nil {
		t.Fatalf("NewCanvas failed: %v", err)
	}
	app.Render(c)

	// Toggle DIFF mode OFF.
	app.HandleKey(KeyMark)
	if app.DiffMode {
		t.Fatal("expected DiffMode=false after second D press")
	}
}
