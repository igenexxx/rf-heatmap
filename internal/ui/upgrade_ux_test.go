package ui

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/subfile"
)

type mockClicker struct {
	clicks int
}

func (m *mockClicker) Click(freqHz, durMs int) {
	m.clicks++
}

func TestRollingCodeBadgeAndTwoStepReplayGuard(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	defer app.Close()

	app.Mode = ModeDecode
	kl := subfile.EncodeKeeLoqPulses(0xDEADBEEF, 0x0ABC123, 0x4, 400)
	k, ok := subfile.DecodeOOK(kl)
	if !ok || !k.IsRolling {
		t.Fatalf("expected KeeLoq rolling key, got (%+v, %v)", k, ok)
	}
	app.LastDecoded = &k
	app.LastRolling = app.RollingDetector.Observe(433_920_000, kl)

	// Render rolling-code banner (should not panic).
	c, _ := NewCanvas("")
	app.Render(c)

	// First press of 5 (TX) MUST warn and NOT start TX.
	app.HandleKey(KeyNav5)
	if !strings.Contains(app.ToastText, "ROLLING CODE") {
		t.Fatalf("expected rolling code warning toast, got %q", app.ToastText)
	}
	// Second press within 3s forces TX.
	app.HandleKey(KeyNav5)
	if !app.TXBusy && !strings.Contains(app.ToastText, "TX") {
		t.Fatalf("expected 2nd press to trigger TX, toast=%q", app.ToastText)
	}

	// 7 (CLR) clears decoded key and rolling report.
	app.HandleKey(KeyNav7)
	if app.LastDecoded != nil || app.LastRolling.IsRolling {
		t.Fatal("expected CLR to reset LastDecoded and LastRolling")
	}
}

func TestSensorsSubViewLifecycleAndSIM(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	defer app.Close()

	app.Mode = ModeDecode
	app.ActiveFreq = 868_350_000
	app.Preset = cc1101.PresetAM270

	// Press R on DECODE -> enters SENSORS and switches to TPMS 433 GFSK99.
	if quit := app.HandleKey(KeyRank); quit {
		t.Fatal("KeyRank should not quit")
	}
	if !app.ShowSensors || app.Preset != cc1101.PresetGFSK999 || app.ActiveFreq != 433_920_000 {
		t.Fatalf("unexpected sensor entry state: show=%v preset=%v freq=%d", app.ShowSensors, app.Preset, app.ActiveFreq)
	}

	// Advance SIM ticks so real sensors.Decode populates SensorEntries.
	for range 25 {
		app.Step()
	}
	if len(app.SensorEntries) == 0 {
		t.Fatal("expected SIM mode to decode synthetic sensor frames")
	}

	// Toggle units and cycle profile + render.
	app.HandleKey(KeyNav5)
	if !app.ImperialUnits {
		t.Fatal("expected ImperialUnits=true after KeyNav5")
	}
	app.HandleKey(KeyNav6)
	if app.SensorProfile != 1 {
		t.Fatalf("expected SensorProfile=1, got %d", app.SensorProfile)
	}
	c, _ := NewCanvas("")
	app.Render(c)

	// Esc exits SENSORS without quitting app and restores prevFreqHz & prevPreset.
	if quit := app.HandleKey(KeyQuit); quit {
		t.Fatal("Esc in SENSORS must NOT quit app")
	}
	if app.ShowSensors || app.Preset != cc1101.PresetAM270 || app.ActiveFreq != 868_350_000 {
		t.Fatalf("expected preset/freq restore on exit: show=%v preset=%v freq=%d", app.ShowSensors, app.Preset, app.ActiveFreq)
	}
}

func TestFoxhuntGeigerSubViewAndEscIsolation(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	defer app.Close()

	clicker := &mockClicker{}
	app.Clicker = clicker
	app.Mode = ModeHeatmap

	// Press G -> enters FOXHUNT.
	if quit := app.HandleKey(KeyFoxhunt); quit {
		t.Fatal("KeyFoxhunt should not quit")
	}
	if !app.ShowFoxhunt {
		t.Fatal("expected ShowFoxhunt=true")
	}
	for range 12 {
		app.Step()
	}
	if clicker.clicks == 0 {
		t.Fatal("expected GeigerClicker to fire in SIM foxhunt")
	}

	// Mute clicker, reset peak, nudge freq, render.
	app.HandleKey(KeyNav5)
	if !app.GeigerMuted {
		t.Fatal("expected GeigerMuted=true")
	}
	app.HandleKey(KeyNav4)
	app.HandleKey(KeyRight)
	c, _ := NewCanvas("")
	app.Render(c)

	// Esc exits FOXHUNT back to ModeHeatmap without quitting app.
	if quit := app.HandleKey(KeyQuit); quit {
		t.Fatal("Esc in FOXHUNT must NOT quit app")
	}
	if app.ShowFoxhunt || app.Mode != ModeHeatmap {
		t.Fatalf("expected return to ModeHeatmap, got show=%v mode=%v", app.ShowFoxhunt, app.Mode)
	}
}

func TestTrimmerSubViewAndAtomicSave(t *testing.T) {
	tmp := t.TempDir()
	capDir := filepath.Join(tmp, "captures")
	dbDir := filepath.Join(tmp, "databases")
	app := NewApp(nil, capDir)
	defer app.Close()
	app.SetLibraryStore(library.NewStore(capDir, dbDir, nil))

	app.Mode = ModePulse
	app.CapturedRaw = []int32{-45000, 35, -30000, 350, -1050, 1050, -350, 350, -1050, -45000}

	// Press T -> enters TRIM.
	if quit := app.HandleKey(KeyTrim); quit {
		t.Fatal("KeyTrim should not quit")
	}
	if !app.ShowTrimmer || app.StartIdx != 3 || app.EndIdx != 9 {
		t.Fatalf("unexpected trimmer init: show=%v [%d..%d)", app.ShowTrimmer, app.StartIdx, app.EndIdx)
	}

	// Adjust repeat count, toggle handle, render, save atomically.
	app.HandleKey(KeyUp)
	if app.Repeats != 2 {
		t.Fatalf("expected Repeats=2, got %d", app.Repeats)
	}
	app.HandleKey(KeyNav4)
	app.HandleKey(KeyNav5)
	c, _ := NewCanvas("")
	app.Render(c)

	app.HandleKey(KeyNav7)
	entries, err := os.ReadDir(capDir)
	if err != nil || len(entries) == 0 {
		t.Fatalf("expected trimmed .sub saved in capturesDir, err=%v count=%d", err, len(entries))
	}

	// Esc exits TRIM without quitting app.
	if quit := app.HandleKey(KeyQuit); quit {
		t.Fatal("Esc in TRIM must NOT quit app")
	}
	if app.ShowTrimmer || app.Mode != ModePulse {
		t.Fatalf("expected return to ModePulse, got show=%v mode=%v", app.ShowTrimmer, app.Mode)
	}
}
