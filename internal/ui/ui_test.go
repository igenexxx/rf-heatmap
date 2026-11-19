package ui

import "testing"

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
