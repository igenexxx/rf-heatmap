package ui

import (
	"path/filepath"
	"testing"
	"time"

	"rf-heatmap/internal/config"
	"rf-heatmap/internal/subfile"
)

func TestRFLinearHomeReadReplayAndSaveFlow(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	app := NewApp(nil, t.TempDir())
	defer app.Close()
	app.SetConfigPath(cfgPath)
	c, _ := NewCanvas("")

	// Boots into HOME in Simple Mode (5 items, zero scrolling).
	if app.Mode != ModeHome {
		t.Fatalf("initial Mode = %v, want ModeHome", app.Mode)
	}
	if got := len(app.homeMenuItems()); got != 5 {
		t.Fatalf("Simple Mode homeMenuItems = %d, want 5", got)
	}
	app.Render(c)

	// Enter opens Read & Replay in Auto-Scan listening state.
	app.HandleKey(KeyEnter)
	if app.Mode != ModeRead || app.ReadCaptured || !app.ReadAutoScan {
		t.Fatalf("expected ModeRead auto-scan listening, got mode=%v cap=%v auto=%v",
			app.Mode, app.ReadCaptured, app.ReadAutoScan)
	}
	app.Step()
	app.Render(c)

	// 4 AUTO/FREQ cycles: AUTO -> 433.92 -> 868.35 MHz.
	app.HandleKey(KeyNav4)
	if app.ReadAutoScan || app.ActiveFreq != 433_920_000 {
		t.Fatalf("expected Fixed 433.92M after 1st 4 press, got auto=%v freq=%d", app.ReadAutoScan, app.ActiveFreq)
	}
	app.HandleKey(KeyNav4)
	if app.ActiveFreq != 868_350_000 {
		t.Fatalf("ActiveFreq = %d, want 868350000", app.ActiveFreq)
	}

	// 6 REC -> 6 STOP captures RAW in SIM mode and transitions to Step 2.
	app.HandleKey(KeyNav6)
	if !app.Recording {
		t.Fatalf("expected Recording=true after 6 REC")
	}
	app.HandleKey(KeyNav6)
	if !app.ReadCaptured || app.Recording {
		t.Fatalf("expected ReadCaptured=true Recording=false after 6 STOP")
	}
	app.Render(c)

	// 4 SEND replays immediately; 5 SAVE writes .sub and opens My Captures.
	app.HandleKey(KeyNav4)
	app.HandleKey(KeyNav5)
	if app.Mode != ModeSaved || len(app.SavedFiles) == 0 {
		t.Fatalf("expected ModeSaved with >=1 file after 5 SAVE, got mode=%v len=%d", app.Mode, len(app.SavedFiles))
	}
	app.Render(c)

	// Esc returns to HOME; first Esc on HOME shows exit confirmation; Enter confirms exit.
	app.HandleKey(KeyBack)
	if app.Mode != ModeHome {
		t.Fatalf("expected ModeHome after KeyBack from ModeSaved, got %v", app.Mode)
	}
	if exit := app.HandleKey(KeyBack); exit || !app.ConfirmExit {
		t.Fatalf("expected ConfirmExit=true without immediate exit, got exit=%v confirm=%v", exit, app.ConfirmExit)
	}
	app.Render(c)
	if exit := app.HandleKey(KeyDown); exit || app.ConfirmExit {
		t.Fatalf("expected KeyDown to cancel ConfirmExit")
	}
	app.HandleKey(KeyBack)
	if exit := app.HandleKey(KeyEnter); !exit {
		t.Fatalf("expected exit=true after Esc -> Enter on HOME")
	}
}

func TestRFExpertModePersistenceAndGuards(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	app := NewApp(nil, t.TempDir())
	defer app.Close()
	app.SetConfigPath(cfgPath)

	// Toggle Expert Mode via HOME 5 EXP -> unlocks 9 items & persists to config.json.
	app.HandleKey(KeyNav5)
	if !app.Settings.ExpertMode || len(app.homeMenuItems()) != 9 {
		t.Fatalf("expected ExpertMode=true with 9 items, got exp=%v len=%d", app.Settings.ExpertMode, len(app.homeMenuItems()))
	}
	loaded, err := config.Load(cfgPath)
	if err != nil || !loaded.ExpertMode {
		t.Fatalf("expected ExpertMode=true persisted in config.json, got %+v err=%v", loaded, err)
	}

	// Rolling code guard in Read & Replay: first 4 SEND warns, Enter confirms.
	app.enterReadWizard()
	app.LastDecoded = &subfile.DecodedKey{
		Protocol: "KeeLoq", Bits: 66, KeyHex: "01 02 03 04 05 06 07 08", TE: 400, IsRolling: true,
	}
	app.ReadCaptured = true
	app.HandleKey(KeyNav4)
	if !app.ReadRollWarn {
		t.Fatalf("expected ReadRollWarn=true on rolling code replay")
	}
	app.HandleKey(KeyBack)
	if app.ReadRollWarn {
		t.Fatalf("expected ReadRollWarn cleared on KeyBack")
	}

	// Simple Mode Heatmap 6 LOCK opens Read & Replay on locked peak frequency.
	app.Settings.ExpertMode = false
	app.SwitchMode(ModeHeatmap)
	app.PeakDBm = -55
	app.PeakFreqHz = 868_350_000
	app.HandleKey(KeyNav6)
	if app.Mode != ModeRead || app.ActiveFreq != 868_350_000 {
		t.Fatalf("expected Simple Mode Heatmap 6 LOCK -> ModeRead @ 868.35M, got mode=%v freq=%d", app.Mode, app.ActiveFreq)
	}

	// Esc from every screen returns cleanly to HOME.
	for _, m := range []Mode{ModeHeatmap, ModeSaved, ModeSettings, ModePulse, ModeDecode, ModeProbe} {
		app.SwitchMode(m)
		app.HandleKey(KeyBack)
		if app.Mode != ModeHome {
			t.Fatalf("KeyBack from %v went to %v, want ModeHome", m, app.Mode)
		}
	}
}

func TestRFAutoCaptureRejectsNoiseAndCapturesSignal(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	defer app.Close()

	app.enterReadWizard()
	app.ReadAutoLocked = true
	app.ActiveFreq = 433_920_000

	// 1. Noise burst must be rejected silently without freezing Step 2.
	app.CapturedRaw = []int32{
		25, -40, 190, -15, 780, -30, 120, -20, 1450, -35, 95, -18, 620, -22, 310, -45,
		30, -18, 940, -25, 42, -16, 1280, -28, 70, -19, 510, -34, 22, -48, 860, -21,
	}
	app.readLastPulseAt = time.Now().Add(-500 * time.Millisecond)
	app.checkReadAutoFinalize()
	if app.ReadCaptured {
		t.Fatalf("noise burst must NOT auto-capture into Step 2")
	}
	if app.ReadAutoLocked || len(app.CapturedRaw) != 0 {
		t.Fatalf("expected noise burst cleared and auto-scan resumed, got locked=%v len=%d",
			app.ReadAutoLocked, len(app.CapturedRaw))
	}

	// 2. Clean CAME 12-bit keyfob burst must auto-capture and freeze Step 2!
	app.ReadAutoLocked = true
	camePulses := []int32{-11520, 320, -320}
	for i := range 12 {
		if i%2 == 0 {
			camePulses = append(camePulses, -640, 320)
		} else {
			camePulses = append(camePulses, -320, 640)
		}
	}
	camePulses = append(camePulses, -11520)
	app.CapturedRaw = camePulses
	app.readLastPulseAt = time.Now().Add(-500 * time.Millisecond)
	app.checkReadAutoFinalize()
	if !app.ReadCaptured {
		t.Fatalf("expected clean CAME burst to auto-capture into Step 2")
	}
}
