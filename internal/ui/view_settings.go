package ui

import (
	"context"
	"fmt"

	"rf-heatmap/internal/config"
	"rf-heatmap/internal/dbsync"
)

const settingsVisibleRows = 5

type settingsRow struct {
	id    string
	label string
	val   string
	col   uint16
}

func (a *App) handleSettingsKey(act KeyAction) {
	rows := a.buildSettingsRows()
	if a.SettingsIdx >= len(rows) {
		a.SettingsIdx = max(0, len(rows)-1)
	}
	switch act {
	case KeyUp:
		a.SettingsIdx = max(0, a.SettingsIdx-1)
	case KeyDown:
		a.SettingsIdx = min(len(rows)-1, a.SettingsIdx+1)
	case KeyLeft:
		a.adjustSelectedSetting(rows[a.SettingsIdx].id, -1)
	case KeyRight:
		a.adjustSelectedSetting(rows[a.SettingsIdx].id, 1)
	case KeyEnter, KeySpace, KeyNav4:
		a.toggleSelectedSetting(rows[a.SettingsIdx].id)
	case KeyNav5:
		a.saveSettingsExplicit()
	case KeyNav6:
		if !a.Settings.ExpertMode {
			a.triggerDBSync()
			return
		}
		a.ApplySettings(config.DefaultSettings())
		a.persistSettings()
		a.SetToast("Defaults restored")
	case KeyNav7:
		if a.Settings.ExpertMode {
			a.ShowHelp = true
			a.HelpPage = 0
		}
	case KeyNav8, KeyBack:
		a.goHome()
	}
}

func (a *App) adjustSelectedSetting(id string, dir int) {
	switch id {
	case "squelch_k":
		a.adjustSquelchKSetting(0.5 * float64(dir))
	case "tx_power":
		a.stepTXPowerSetting(dir)
	}
}

func (a *App) stepTXPowerSetting(dir int) {
	levels := config.AllowedTXPowerDBm
	cur := len(levels) - 1
	for i, lvl := range levels {
		if lvl == a.Settings.TXPowerDBm {
			cur = i
			break
		}
	}
	next := (cur + dir + len(levels)) % len(levels)
	a.Settings.TXPowerDBm = levels[next]
	a.ApplySettings(a.Settings)
	a.persistSettings()
	a.SetToast(fmt.Sprintf("TX Power: %+d dBm", a.Settings.TXPowerDBm))
}

func (a *App) adjustSquelchKSetting(delta float64) {
	a.Settings.SquelchK = min(config.MaxSquelchK, max(config.MinSquelchK, a.Settings.SquelchK+delta))
	a.ApplySettings(a.Settings)
	a.persistSettings()
	a.SetToast(fmt.Sprintf("Squelch K: %.1f", a.Settings.SquelchK))
}

func (a *App) toggleSelectedSetting(id string) {
	switch id {
	case "expert":
		a.toggleExpertMode()
		rows := a.buildSettingsRows()
		if a.SettingsIdx >= len(rows) {
			a.SettingsIdx = 0
		}
		return
	case "mcp":
		a.Settings.MCPEnabled = !a.Settings.MCPEnabled
		a.SetToast(toggleToast("MCP Server", a.Settings.MCPEnabled, "ENABLED", "DISABLED"))
	case "asq":
		a.Settings.AutoSquelch = !a.Settings.AutoSquelch
		a.ApplySettings(a.Settings)
		a.SetToast(toggleToast("Auto Squelch", a.Settings.AutoSquelch, "ON", "OFF"))
	case "autolock":
		a.Settings.AutoLock = !a.Settings.AutoLock
		a.ApplySettings(a.Settings)
		a.SetToast(toggleToast("Auto-Lock", a.Settings.AutoLock, "ON", "OFF"))
	case "squelch_k":
		nextK := a.Settings.SquelchK + 0.5
		if nextK > config.MaxSquelchK+0.01 {
			nextK = config.MinSquelchK
		}
		a.Settings.SquelchK = nextK
		a.ApplySettings(a.Settings)
		a.SetToast(fmt.Sprintf("Squelch K: %.1f", a.Settings.SquelchK))
	case "reset_ml":
		a.ResetMLBaselines()
		a.SetToast("ML Baseline reset")
		return
	case "sync":
		a.triggerDBSync()
		return
	case "sources":
		a.openSourcesManage()
		return
	case "tx_power":
		a.stepTXPowerSetting(1)
		return
	}
	a.persistSettings()
}

func (a *App) triggerDBSync() {
	if a.SyncBusy {
		if a.syncCancel != nil {
			a.syncCancel()
		}
		a.SetToast("Cancelling sync...")
		return
	}
	sources, err := dbsync.LoadSources(a.SourcesPath)
	if err != nil {
		a.SetToast("Sources err!")
		return
	}
	if len(sources) == 0 {
		a.SetToast("No DB sources")
		return
	}
	syncer := a.Syncer
	if syncer == nil && a.Store != nil {
		syncer = dbsync.NewSyncer(a.Store.DatabasesDir)
	}
	ctx, cancel := context.WithCancel(context.Background())
	a.syncCancel = cancel
	a.SyncBusy = true
	a.SetToast(fmt.Sprintf("Syncing %d DB(s)...", len(sources)))

	go func() {
		defer cancel()
		reports := syncer.SyncAll(ctx, sources, nil)
		select {
		case a.syncDoneCh <- reports:
		default:
		}
	}()
}

func (a *App) pollSyncResults() {
	if a.syncDoneCh == nil {
		return
	}
	select {
	case reports := <-a.syncDoneCh:
		a.SyncBusy = false
		a.syncCancel = nil
		a.refreshProbePacks()
		a.SetToast(summarizeSyncReports(reports))
	default:
	}
}

func summarizeSyncReports(reports []dbsync.SyncReport) string {
	var added, updated, errs int
	for _, r := range reports {
		added += r.Added
		updated += r.Updated
		if r.Err != "" || r.Failed > 0 {
			errs++
		}
	}
	if errs > 0 && added == 0 && updated == 0 {
		return fmt.Sprintf("Sync err (%d)", errs)
	}
	if added == 0 && updated == 0 {
		return "DBs up to date"
	}
	return fmt.Sprintf("Sync: +%d new ~%d upd", added, updated)
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
	rows := a.buildSettingsRows()
	if a.SettingsIdx >= len(rows) {
		a.SettingsIdx = max(0, len(rows)-1)
	}
	c.FillRect(6, 22, 308, 98, ColPanel)
	c.DrawRect(6, 22, 308, 98, ColBorder)

	start := max(0, a.SettingsIdx-(settingsVisibleRows-1))
	visible := min(settingsVisibleRows, len(rows)-start)
	for r := range visible {
		i := start + r
		row := rows[i]
		y := 26 + r*19
		if i == a.SettingsIdx {
			c.FillRect(8, y-1, 304, 17, ColNavActive)
			c.DrawRect(8, y-1, 304, 17, ColCyan)
		}
		c.DrawText(14, y+4, row.label, ColText, 1)
		c.DrawText(306-len(row.val)*6, y+4, row.val, row.col, 1)
	}
	a.drawSettingsFooter(c)
}

func (a *App) buildSettingsRows() []settingsRow {
	expVal, expCol := "OFF (Simple)", ColCyan
	if a.Settings.ExpertMode {
		expVal, expCol = "ON (Full Lab)", ColMagenta
	}
	mcpVal, mcpCol := "OFF (safe)", ColAmber
	if a.Settings.MCPEnabled {
		mcpVal, mcpCol = "ON (--mcp)", ColGreen
	}
	asqVal, asqCol := "OFF", ColMuted
	if a.Settings.AutoSquelch {
		asqVal, asqCol = "ON", ColGreen
	}
	lckVal, lckCol := "OFF", ColMuted
	if a.Settings.AutoLock {
		lckVal, lckCol = "ON", ColGreen
	}
	syncVal, syncCol := "Run Now", ColCyan
	if a.SyncBusy {
		syncVal, syncCol = "SYNCING", ColMagenta
	}
	txCol := ColCyan
	if a.Settings.TXPowerDBm >= 10 {
		txCol = ColAmber
	}
	if !a.Settings.ExpertMode {
		return []settingsRow{
			{"expert", "Expert Mode", expVal, expCol},
			{"tx_power", "TX Power", fmt.Sprintf("%+d dBm", a.Settings.TXPowerDBm), txCol},
			{"mcp", "MCP Server (SSH)", mcpVal, mcpCol},
			{"sync", "Sync DB Sources", syncVal, syncCol},
			{"asq", "Auto Squelch", asqVal, asqCol},
		}
	}
	return []settingsRow{
		{"expert", "Expert Mode", expVal, expCol},
		{"mcp", "MCP Server (SSH)", mcpVal, mcpCol},
		{"asq", "Auto Squelch", asqVal, asqCol},
		{"autolock", "Auto-Lock", lckVal, lckCol},
		{"squelch_k", "Squelch K", fmt.Sprintf("%.1f sigma", a.Settings.SquelchK), ColCyan},
		{"reset_ml", "Reset ML Baseline", "ACTION", ColAmber},
		{"sync", "Sync DB Sources", syncVal, syncCol},
		{"sources", "Manage DB Sources", "OPEN>", ColCyan},
		{"tx_power", "TX Power (PATABLE)", fmt.Sprintf("%+d dBm", a.Settings.TXPowerDBm), txCol},
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
