package ui

import (
	"path/filepath"
	"testing"

	"rf-heatmap/internal/config"
)

func TestThermalColor(t *testing.T) {
	cold := ThermalColor(-120)
	mid := ThermalColor(-72)
	hot := ThermalColor(-30)
	if cold == mid || mid == hot || cold == hot {
		t.Fatalf("expected distinct thermal colors: cold=%04X mid=%04X hot=%04X", cold, mid, hot)
	}
}

func TestCanvasDrawingAndBounds(t *testing.T) {
	c, err := NewCanvas("")
	if err != nil {
		t.Fatalf("NewCanvas failed: %v", err)
	}
	c.Clear(ColBg)
	if got := c.PixelAt(10, 10); got != ColBg {
		t.Fatalf("got %04X want %04X", got, ColBg)
	}
	// Out-of-bounds draws must not panic.
	c.FillRect(-10, -10, 400, 300, ColCyan)
	if got := c.PixelAt(319, 169); got != ColCyan {
		t.Fatalf("expected clipped FillRect to color (319,169)")
	}
	c.DrawText(5, 5, "RF-HEAT 433.92M!", ColAmber, 1)
}

func TestMapLinuxKeyCode(t *testing.T) {
	tests := []struct {
		code uint16
		want KeyAction
	}{
		{codeEsc, KeyQuit},
		{codeQ, KeyQuit},
		{codeF, KeyUp},
		{codeX, KeyDown},
		{codeZ, KeyLeft},
		{codeC, KeyRight},
		{code4, KeyNav4},
		{code8, KeyNav8},
		{codeTab, KeyTab},
		{codeB, KeyBand},
		{codeM, KeyMod},
		{codeS, KeyAutoSquelch},
		{codeL, KeyAutoLock},
		{0xFFFF, KeyNone},
	}
	for _, tc := range tests {
		if got := MapLinuxKeyCode(tc.code); got != tc.want {
			t.Fatalf("MapLinuxKeyCode(%d)=%v want %v", tc.code, got, tc.want)
		}
	}
}

func TestAppStateTransitions(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	if app.Mode != ModeHeatmap || !app.SimMode {
		t.Fatalf("unexpected initial app state: %+v", app)
	}
	app.Step()

	// Tab cycles through all 4 modes.
	app.HandleKey(KeyTab)
	if app.Mode != ModePulse {
		t.Fatalf("expected ModePulse, got %v", app.Mode)
	}
	app.HandleKey(KeyNav4) // Start recording
	if !app.Recording {
		t.Fatal("expected Recording=true")
	}
	app.Step()
	app.HandleKey(KeyNav6) // Save RAW .sub

	app.HandleKey(KeyTab)
	if app.Mode != ModeDecode {
		t.Fatalf("expected ModeDecode, got %v", app.Mode)
	}
	app.HandleKey(KeyTab)
	if app.Mode != ModeSaved {
		t.Fatalf("expected ModeSaved, got %v", app.Mode)
	}
	if len(app.SavedFiles) == 0 {
		t.Fatal("expected saved .sub file to appear in ModeSaved")
	}

	c, _ := NewCanvas("")
	app.Render(c)

	// Help modal paging.
	app.HandleKey(KeyHelp)
	if !app.ShowHelp || app.HelpPage != 0 {
		t.Fatalf("expected ShowHelp=true HelpPage=0, got %v %d", app.ShowHelp, app.HelpPage)
	}
	app.HandleKey(KeyRight)
	if app.HelpPage != 1 {
		t.Fatalf("expected HelpPage=1, got %d", app.HelpPage)
	}
	app.Render(c)
	app.HandleKey(KeyHelp)
	if app.ShowHelp {
		t.Fatal("expected Help modal to close on KeyHelp")
	}

	if !app.HandleKey(KeyQuit) {
		t.Fatal("expected KeyQuit to return true")
	}
}

func TestSmartSquelchAndAutoLock(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	if !app.AutoSquelch || !app.AutoLock {
		t.Fatalf("expected AutoSquelch and AutoLock enabled by default")
	}

	// Run enough ticks in ModeHeatmap to warm up baseline and detect simulated bursts.
	for range 20 {
		app.Step()
	}
	if app.Mode != ModeHeatmap {
		t.Fatalf("Auto-Lock must not switch tabs, got mode=%v", app.Mode)
	}
	if !app.SweepBaseline.Ready(0) {
		t.Fatal("expected SweepBaseline to be warmed up after 20 ticks")
	}

	// Band switch (5 BAND) must reset baseline.
	app.HandleKey(KeyNav5)
	if app.SweepBaseline.Ready(0) {
		t.Fatal("expected SweepBaseline.Reset() on band change")
	}

	// Toggle Auto-Lock and Auto-Squelch via S / L keys.
	app.HandleKey(KeyAutoLock)
	if app.AutoLock {
		t.Fatal("expected AutoLock=false after KeyAutoLock")
	}
	app.HandleKey(KeyAutoSquelch)
	if app.AutoSquelch {
		t.Fatal("expected AutoSquelch=false after KeyAutoSquelch")
	}
	app.HandleKey(KeyAutoSquelch)
	if !app.AutoSquelch {
		t.Fatal("expected AutoSquelch=true after second KeyAutoSquelch")
	}

	// In ModePulse with AutoSquelch ON, F/X adjusts sensitivity k and LiveBaseline warms up.
	app.HandleKey(KeyTab)
	if app.Mode != ModePulse {
		t.Fatalf("expected ModePulse, got %v", app.Mode)
	}
	kBefore := app.LiveBaseline.SquelchK()
	app.HandleKey(KeyUp)
	if app.LiveBaseline.SquelchK() <= kBefore {
		t.Fatalf("expected KeyUp to increase SquelchK: before=%v after=%v", kBefore, app.LiveBaseline.SquelchK())
	}
	for range 10 {
		app.Step()
	}
	if !app.LiveBaseline.Ready(0) {
		t.Fatal("expected LiveBaseline ready after 10 ticks")
	}
	eff := app.EffectiveSquelchDBm()
	if eff < -120 || eff > -20 {
		t.Fatalf("unexpected EffectiveSquelchDBm=%d", eff)
	}

	// Render both Heatmap and Pulse with Smart Squelch active to verify zero panics.
	c, _ := NewCanvas("")
	app.Render(c)
	app.Mode = ModeHeatmap
	for range 10 {
		app.Step()
	}
	app.Render(c)
}

func TestSettingsModeAndPersistence(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	app := NewApp(nil, t.TempDir())
	app.SetConfigPath(cfgPath)

	// Tab through all 6 modes: HEATMAP -> PULSE -> DECODE -> SAVED -> PROBE -> SET -> HEATMAP.
	wantModes := []Mode{ModePulse, ModeDecode, ModeSaved, ModeProbe, ModeSettings, ModeHeatmap}
	for _, want := range wantModes {
		app.HandleKey(KeyTab)
		if app.Mode != want {
			t.Fatalf("expected mode %v, got %v", want, app.Mode)
		}
	}
	if ModeSettings.String() != "SET" {
		t.Fatalf("ModeSettings.String() = %q, want SET", ModeSettings.String())
	}

	// Jump to SET via KeyNav8 cycling.
	for app.Mode != ModeSettings {
		app.HandleKey(KeyNav8)
	}

	// Row 0: toggle MCP Server ON and verify auto-save.
	if app.Settings.MCPEnabled {
		t.Fatal("MCPEnabled should default to false")
	}
	app.HandleKey(KeyEnter)
	if !app.Settings.MCPEnabled {
		t.Fatal("expected MCPEnabled=true after Enter on row 0")
	}
	loaded, err := config.Load(cfgPath)
	if err != nil || !loaded.MCPEnabled {
		t.Fatalf("expected auto-saved MCPEnabled=true, got %+v err=%v", loaded, err)
	}

	// Render with MCP enabled.
	c, _ := NewCanvas("")
	app.Render(c)

	// Row 1: Auto Squelch toggle.
	app.HandleKey(KeyDown)
	if app.SettingsIdx != 1 {
		t.Fatalf("SettingsIdx = %d, want 1", app.SettingsIdx)
	}
	app.HandleKey(KeySpace)
	if app.AutoSquelch || app.Settings.AutoSquelch {
		t.Fatal("expected AutoSquelch=false after toggle")
	}

	// Row 2: Auto-Lock toggle.
	app.HandleKey(KeyDown)
	app.HandleKey(KeyNav4)
	if app.AutoLock || app.Settings.AutoLock {
		t.Fatal("expected AutoLock=false after toggle")
	}

	// Row 3: Squelch K step & wrap 2.0..6.0, plus Left/Right.
	app.HandleKey(KeyDown)
	if app.SettingsIdx != 3 {
		t.Fatalf("SettingsIdx = %d, want 3", app.SettingsIdx)
	}
	// Step past 6.0 to wrap back to 2.0.
	app.ApplySettings(config.Settings{MCPEnabled: true, AutoSquelch: true, AutoLock: true, SquelchK: 6.0})
	app.HandleKey(KeyNav4)
	if app.Settings.SquelchK != config.MinSquelchK {
		t.Fatalf("expected SquelchK wrap to %.1f, got %.1f", config.MinSquelchK, app.Settings.SquelchK)
	}
	app.HandleKey(KeyRight)
	if app.Settings.SquelchK != 2.5 {
		t.Fatalf("expected SquelchK=2.5 after KeyRight, got %.1f", app.Settings.SquelchK)
	}
	app.HandleKey(KeyLeft)
	if app.Settings.SquelchK != 2.0 {
		t.Fatalf("expected SquelchK=2.0 after KeyLeft, got %.1f", app.Settings.SquelchK)
	}

	// Explicit save (KeyNav5).
	app.HandleKey(KeyNav5)
	loaded, err = config.Load(cfgPath)
	if err != nil || loaded.SquelchK != 2.0 {
		t.Fatalf("explicit save failed: loaded=%+v err=%v", loaded, err)
	}

	// Reset to defaults (KeyNav6) -> MCPEnabled must be false again.
	app.HandleKey(KeyNav6)
	if app.Settings.MCPEnabled || !app.AutoSquelch || !app.AutoLock || app.Settings.SquelchK != config.DefaultSquelchK {
		t.Fatalf("expected defaults restored, got %+v", app.Settings)
	}
	app.Render(c)

	// Help button (KeyNav7).
	app.HandleKey(KeyNav7)
	if !app.ShowHelp {
		t.Fatal("expected KeyNav7 in ModeSettings to open Help modal")
	}
	app.HandleKey(KeyEnter) // close help

	// Move cursor back up to 0.
	app.HandleKey(KeyUp)
	app.HandleKey(KeyUp)
	app.HandleKey(KeyUp)
	app.HandleKey(KeyUp) // clamp at 0
	if app.SettingsIdx != 0 {
		t.Fatalf("expected SettingsIdx=0, got %d", app.SettingsIdx)
	}
}
