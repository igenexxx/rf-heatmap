package ui

import (
	"fmt"

	"rf-heatmap/internal/config"
)

const settingsRowCount = 5

type settingsRow struct {
	label string
	val   string
	col   uint16
}

func (a *App) handleSettingsKey(act KeyAction) {
	switch act {
	case KeyUp:
		a.SettingsIdx = max(0, a.SettingsIdx-1)
	case KeyDown:
		a.SettingsIdx = min(settingsRowCount-1, a.SettingsIdx+1)
	case KeyLeft:
		a.adjustSquelchKSetting(-0.5)
	case KeyRight:
		a.adjustSquelchKSetting(0.5)
	case KeyEnter, KeySpace, KeyNav4:
		a.toggleSelectedSetting()
	case KeyNav5:
		a.saveSettingsExplicit()
	case KeyNav6:
		a.ApplySettings(config.DefaultSettings())
		a.persistSettings()
		a.SetToast("Defaults restored")
	case KeyNav7:
		a.ShowHelp = true
		a.HelpPage = 0
	}
}

func (a *App) adjustSquelchKSetting(delta float64) {
	if a.SettingsIdx != 3 {
		return
	}
	a.Settings.SquelchK = min(config.MaxSquelchK, max(config.MinSquelchK, a.Settings.SquelchK+delta))
	a.ApplySettings(a.Settings)
	a.persistSettings()
	a.SetToast(fmt.Sprintf("Squelch K: %.1f", a.Settings.SquelchK))
}

func (a *App) toggleSelectedSetting() {
	switch a.SettingsIdx {
	case 0:
		a.Settings.MCPEnabled = !a.Settings.MCPEnabled
		a.SetToast(toggleToast("MCP Server", a.Settings.MCPEnabled, "ENABLED", "DISABLED"))
	case 1:
		a.Settings.AutoSquelch = !a.Settings.AutoSquelch
		a.ApplySettings(a.Settings)
		a.SetToast(toggleToast("Auto Squelch", a.Settings.AutoSquelch, "ON", "OFF"))
	case 2:
		a.Settings.AutoLock = !a.Settings.AutoLock
		a.ApplySettings(a.Settings)
		a.SetToast(toggleToast("Auto-Lock", a.Settings.AutoLock, "ON", "OFF"))
	case 3:
		nextK := a.Settings.SquelchK + 0.5
		if nextK > config.MaxSquelchK+0.01 {
			nextK = config.MinSquelchK
		}
		a.Settings.SquelchK = nextK
		a.ApplySettings(a.Settings)
		a.SetToast(fmt.Sprintf("Squelch K: %.1f", a.Settings.SquelchK))
	case 4:
		a.ResetMLBaselines()
		a.SetToast("ML Baseline reset")
		return
	}
	a.persistSettings()
}

// ResetMLBaselines clears online noise baselines and live ML trackers for a fresh RF environment.
func (a *App) ResetMLBaselines() {
	if a.SweepBaseline != nil {
		a.SweepBaseline.Reset()
	}
	if a.LiveBaseline != nil {
		a.LiveBaseline.Reset()
	}
	if a.Foxhunt != nil {
		a.Foxhunt.Reset()
	}
	if a.Activity != nil {
		a.Activity.Reset()
	}
	a.HasBurst = false
	a.HasPrediction = false
	a.HasMatch = false
}

func toggleToast(name string, on bool, onStr, offStr string) string {
	if on {
		return name + " " + onStr
	}
	return name + " " + offStr
}

func (a *App) saveSettingsExplicit() {
	path := a.ConfigPath
	if path == "" {
		path = config.DefaultPath()
	}
	if err := config.Save(path, a.Settings); err != nil {
		a.SetToast("Save err!")
		return
	}
	a.SetToast("Settings saved")
}

func (a *App) persistSettings() {
	if a.ConfigPath == "" {
		return
	}
	if err := config.Save(a.ConfigPath, a.Settings); err != nil {
		a.SetToast("Save err!")
	}
}

func (a *App) drawSettings(c *Canvas) {
	c.DrawText(6, 21, "Settings (Opt-In Services)", ColCyan, 1)
	c.FillRect(6, 31, 308, 90, ColPanel)
	c.DrawRect(6, 31, 308, 90, ColBorder)

	for i, row := range a.buildSettingsRows() {
		y := 34 + i*17
		if i == a.SettingsIdx {
			c.FillRect(8, y-1, 304, 15, ColNavActive)
			c.DrawRect(8, y-1, 304, 15, ColCyan)
		}
		c.DrawText(14, y+3, row.label, ColText, 1)
		c.DrawText(306-len(row.val)*6, y+3, row.val, row.col, 1)
	}
	a.drawSettingsFooter(c)
}

func (a *App) buildSettingsRows() [settingsRowCount]settingsRow {
	mcpVal, mcpCol := "DISABLED", ColAmber
	if a.Settings.MCPEnabled {
		mcpVal, mcpCol = "ENABLED", ColGreen
	}
	asqVal, asqCol := "OFF", ColMuted
	if a.Settings.AutoSquelch {
		asqVal, asqCol = "ON", ColGreen
	}
	lckVal, lckCol := "OFF", ColMuted
	if a.Settings.AutoLock {
		lckVal, lckCol = "ON", ColGreen
	}
	return [settingsRowCount]settingsRow{
		{"MCP Server (SSH)", mcpVal, mcpCol},
		{"Auto Squelch", asqVal, asqCol},
		{"Auto-Lock", lckVal, lckCol},
		{"Squelch K", fmt.Sprintf("%.1f sigma", a.Settings.SquelchK), ColCyan},
		{"Reset ML Baseline", "ACTION", ColAmber},
	}
}

func (a *App) drawSettingsFooter(c *Canvas) {
	c.FillRect(6, 124, 308, 16, ColPanelAlt)
	c.DrawRect(6, 124, 308, 16, ColBorder)
	hintCol := ColMuted
	if a.Settings.MCPEnabled {
		hintCol = ColGreen
	}
	c.DrawTextCentered(ScreenW/2, 128, "ssh <dev> rf-heatmap --mcp", hintCol, 1)
}
