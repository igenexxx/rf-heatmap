package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

func TestSettingsResetMLBaseline(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	app.Mode = ModeSettings

	// Warm up baselines and trackers so we can verify ResetMLBaselines clears them.
	for range 10 {
		app.SweepBaseline.UpdateBin(0, -90)
		app.LiveBaseline.UpdateBin(0, -90)
		app.Foxhunt.Update(-70)
	}
	app.Activity.Observe(time.Now(), true)
	app.HasBurst = true
	app.HasPrediction = true
	app.HasMatch = true

	// Navigate down to row 4 (Reset ML Baseline) and trigger it.
	for range 4 {
		app.HandleKey(KeyDown)
	}
	if app.SettingsIdx != 4 {
		t.Fatalf("SettingsIdx = %d, want 4", app.SettingsIdx)
	}
	app.HandleKey(KeyEnter)

	if app.HasBurst || app.HasPrediction || app.HasMatch {
		t.Fatalf("expected ML flags cleared after reset, got burst=%v pred=%v match=%v",
			app.HasBurst, app.HasPrediction, app.HasMatch)
	}
	if app.SweepBaseline.Ready(0) || app.LiveBaseline.Ready(0) {
		t.Fatalf("expected SweepBaseline and LiveBaseline to be reset")
	}
	if !strings.Contains(app.ToastText, "ML Baseline reset") {
		t.Fatalf("ToastText = %q, want 'ML Baseline reset'", app.ToastText)
	}
	// Render settings screen to verify 5-row layout does not panic.
	c, err := NewCanvas("")
	if err != nil {
		t.Fatalf("NewCanvas: %v", err)
	}
	app.Render(c)
}

func TestSavedPurgeNoiseTwoStepConfirmation(t *testing.T) {
	dir := t.TempDir()
	// Create a synthetic noise RAW capture (erratic high-jitter short spikes).
	noisePulses := make([]int32, 0, 80)
	for i := range 40 {
		noisePulses = append(noisePulses, int32(45+(i*37)%290), -int32(55+(i*53)%310))
	}
	noiseSub := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File", Version: 1, FreqHz: 433_920_000,
		Preset: "FuriHalSubGhzPresetOok650Async", Protocol: "RAW", RawData: noisePulses,
	}
	// Verify our test fixture is indeed classified as Noise by rfml.
	pred, _, _ := rfml.ClassifyPulses(noisePulses, 433_920_000, 8.0)
	if !pred.IsNoise {
		t.Fatalf("expected noise fixture to classify as Noise, got %+v", pred)
	}

	// Create a valid Princeton Key capture that MUST NOT be purged.
	keySub := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File", Version: 1, FreqHz: 433_920_000,
		Preset: "FuriHalSubGhzPresetOok650Async", Protocol: "Princeton",
		Bit: 24, Key: "00 00 00 00 00 A5 5A 01", TE: 350,
	}

	noisePath := filepath.Join(dir, "raw_noise.sub")
	keyPath := filepath.Join(dir, "home_gate.sub")
	if err := os.WriteFile(noisePath, []byte(noiseSub.Format()), 0o644); err != nil {
		t.Fatalf("write noise fixture: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte(keySub.Format()), 0o644); err != nil {
		t.Fatalf("write key fixture: %v", err)
	}

	app := NewApp(nil, dir)
	app.Mode = ModeSaved
	app.refreshSavedFiles()

	// 1st press of KeyNav7 (PURGE) should only warn and NOT delete.
	app.HandleKey(KeyNav7)
	if _, err := os.Stat(noisePath); err != nil {
		t.Fatalf("expected noise file to still exist after 1st press: %v", err)
	}
	if !strings.Contains(app.ToastText, "Purge 1 noise?") {
		t.Fatalf("ToastText = %q, want confirmation prompt", app.ToastText)
	}

	// 2nd press within 3s confirms and deletes only the noise capture.
	app.HandleKey(KeyNav7)
	if _, err := os.Stat(noisePath); !os.IsNotExist(err) {
		t.Fatalf("expected noise file to be deleted after 2nd press, err=%v", err)
	}
	if _, err := os.Stat(keyPath); err != nil {
		t.Fatalf("expected valid key file to remain intact: %v", err)
	}
	if !strings.Contains(app.ToastText, "Purged 1 noise") {
		t.Fatalf("ToastText = %q, want 'Purged 1 noise'", app.ToastText)
	}

	// 3rd press with no noise remaining reports 0.
	app.HandleKey(KeyNav7)
	if !strings.Contains(app.ToastText, "No noise captures") {
		t.Fatalf("ToastText = %q, want 'No noise captures'", app.ToastText)
	}
}
