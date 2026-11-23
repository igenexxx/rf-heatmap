package ui

import (
	"path/filepath"
	"testing"

	"rf-heatmap/internal/config"
)

func TestSettingsTXPowerSelectorAndPersistence(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	app := NewApp(nil, t.TempDir())
	defer app.Close()
	app.SetConfigPath(cfgPath)
	app.Mode = ModeSettings
	app.SettingsIdx = 1 // TX Power in Simple Mode (row 1)

	if app.Settings.TXPowerDBm != config.DefaultTXPowerDBm {
		t.Fatalf("default TXPowerDBm = %d, want %d", app.Settings.TXPowerDBm, config.DefaultTXPowerDBm)
	}

	// Left steps down from +10 dBm -> +5 dBm.
	app.HandleKey(KeyLeft)
	if app.Settings.TXPowerDBm != 5 {
		t.Fatalf("after KeyLeft TXPowerDBm = %d, want 5", app.Settings.TXPowerDBm)
	}
	// Right steps back to +10 dBm, Enter wraps to -30 dBm.
	app.HandleKey(KeyRight)
	if app.Settings.TXPowerDBm != 10 {
		t.Fatalf("after KeyRight TXPowerDBm = %d, want 10", app.Settings.TXPowerDBm)
	}
	app.HandleKey(KeyEnter)
	if app.Settings.TXPowerDBm != -30 {
		t.Fatalf("after wrap KeyEnter TXPowerDBm = %d, want -30", app.Settings.TXPowerDBm)
	}

	loaded, err := config.Load(cfgPath)
	if err != nil || loaded.TXPowerDBm != -30 {
		t.Fatalf("persisted TXPowerDBm = %d (err=%v), want -30", loaded.TXPowerDBm, err)
	}

	// Render SET with TX Power highlighted.
	c, _ := NewCanvas("")
	app.Render(c)
}

func TestFreqDriftBadgeRendersInPulseAndDecode(t *testing.T) {
	app := NewApp(nil, t.TempDir())
	defer app.Close()
	app.FreqOffsetHz = 6348 // > 5 kHz amber highlight
	app.HasFreqOffset = true

	c, _ := NewCanvas("")
	app.Mode = ModePulse
	app.Render(c)

	app.Mode = ModeDecode
	app.Render(c)
}
