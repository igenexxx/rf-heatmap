package ui

import (
	"testing"

	"rf-heatmap/internal/library"
)

func TestContextAwareHelpAllScreensAndBounds(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	defer app.Close()
	c, _ := NewCanvas("")

	setupCases := []struct {
		name      string
		setup     func()
		wantTitle string
	}{
		{"home simple", func() { app.Settings.ExpertMode = false; app.goHome() }, "HOME MENU"},
		{"home expert", func() { app.Settings.ExpertMode = true; app.goHome() }, "HOME MENU"},
		{"read listen simple", func() { app.Settings.ExpertMode = false; app.enterReadWizard() }, "READ & REPLAY (LISTEN)"},
		{"read listen expert", func() { app.Settings.ExpertMode = true; app.enterReadWizard() }, "READ & REPLAY (LISTEN)"},
		{"read captured", func() {
			app.enterReadWizard()
			app.ReadCaptured = true
		}, "READ & REPLAY (CAPTURED)"},
		{"snapshot", func() {
			app.CapturedRaw = []int32{-10500, 350, -1050, 1050, -350}
			app.EnterSnapshotFromRead()
		}, "SIGNAL SNAPSHOT"},
		{"trimmer", func() {
			app.ExitSnapshot()
			app.openTrimmer("test.sub", 433_920_000, "AM650", []int32{-10500, 350, -1050, 1050, -350}, ModePulse)
		}, "RAW TRIMMER"},
		{"foxhunt", func() {
			app.ExitTrimmer()
			app.EnterFoxhunt(ModeHeatmap)
		}, "FOXHUNT GEIGER"},
		{"sensors", func() {
			app.ExitFoxhunt()
			app.EnterSensors()
		}, "SENSORS (TPMS & WEATHER)"},
		{"sources", func() {
			app.ExitSensors()
			app.openSourcesManage()
		}, "MANAGE DB SOURCES"},
		{"heatmap simple", func() {
			app.ShowSources = false
			app.Settings.ExpertMode = false
			app.SwitchMode(ModeHeatmap)
		}, "SPECTRUM MAP"},
		{"heatmap expert", func() {
			app.Settings.ExpertMode = true
			app.SwitchMode(ModeHeatmap)
		}, "SPECTRUM MAP"},
		{"saved simple", func() {
			app.Settings.ExpertMode = false
			app.SwitchMode(ModeSaved)
		}, "MY CAPTURES"},
		{"saved expert", func() {
			app.Settings.ExpertMode = true
			app.SwitchMode(ModeSaved)
		}, "MY CAPTURES (EXPERT)"},
		{"pulse", func() { app.SwitchMode(ModePulse) }, "PULSE SCOPE"},
		{"decode", func() { app.SwitchMode(ModeDecode) }, "OOK DECODER"},
		{"probe idle", func() {
			app.SwitchMode(ModeProbe)
			app.Probe.Status = library.ProbeIdle
		}, "AUTO-PROBE"},
		{"probe running", func() {
			app.SwitchMode(ModeProbe)
			app.Probe.Status = library.ProbeRunning
		}, "AUTO-PROBE (RUNNING)"},
		{"settings simple", func() {
			app.Settings.ExpertMode = false
			app.SwitchMode(ModeSettings)
		}, "SETTINGS (SIMPLE)"},
		{"settings expert", func() {
			app.Settings.ExpertMode = true
			app.SwitchMode(ModeSettings)
		}, "SETTINGS (EXPERT)"},
	}

	for _, tc := range setupCases {
		t.Run(tc.name, func(t *testing.T) {
			tc.setup()
			h := app.ActiveScreenHelp()
			if h.ScreenTitle != tc.wantTitle {
				t.Fatalf("ScreenTitle = %q, want %q", h.ScreenTitle, tc.wantTitle)
			}
			if h.Summary[0] == "" {
				t.Fatalf("Summary[0] must not be empty")
			}
			if len(h.Summary[0]) > helpSummaryMaxCh || len(h.Summary[1]) > helpSummaryMaxCh {
				t.Fatalf("summary line exceeds %d chars: %q / %q", helpSummaryMaxCh, h.Summary[0], h.Summary[1])
			}
			if len(h.Keys) == 0 {
				t.Fatalf("Keys must not be empty")
			}
			for _, k := range h.Keys {
				if k.Key == "" || len(k.Key) > 7 {
					t.Fatalf("keycap label %q must be 1..7 chars", k.Key)
				}
				if k.Desc == "" || len(k.Desc) > helpDescMaxCh {
					t.Fatalf("key desc %q must be 1..%d chars", k.Desc, helpDescMaxCh)
				}
			}

			// Open Help, render every page without panic, and close cleanly.
			app.HandleKey(KeyHelp)
			if !app.ShowHelp || app.HelpPage != 0 {
				t.Fatalf("expected ShowHelp=true HelpPage=0")
			}
			pages := h.PageCount()
			for p := range pages {
				if app.HelpPage != p {
					t.Fatalf("HelpPage = %d, want %d", app.HelpPage, p)
				}
				app.Render(c)
				if pages > 1 {
					app.HandleKey(KeyRight)
				}
			}
			if pages > 1 && app.HelpPage != 0 {
				t.Fatalf("expected HelpPage wrap back to 0, got %d", app.HelpPage)
			}
			if pages > 1 {
				app.HandleKey(KeyLeft)
				if app.HelpPage != pages-1 {
					t.Fatalf("expected KeyLeft wrap to %d, got %d", pages-1, app.HelpPage)
				}
			}
			app.HandleKey(KeyBack)
			if app.ShowHelp {
				t.Fatalf("expected KeyBack to close Help modal")
			}
		})
	}
}
