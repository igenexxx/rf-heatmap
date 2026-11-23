package ui

import "fmt"

const homeVisibleRows = 5

type homeMenuItem struct {
	Tag      string
	TagCol   uint16
	Title    string
	Subtitle string
	Action   func(*App)
}

func (a *App) homeMenuItems() []homeMenuItem {
	capSub := fmt.Sprintf("%d saved .sub files", len(a.SavedFiles))
	if len(a.SavedFiles) == 0 {
		capSub = "Replay & trim your captures"
	}
	items := []homeMenuItem{
		{Tag: "RX", TagCol: ColCyan, Title: "Read & Replay", Subtitle: "Capture, test & save .sub", Action: (*App).enterReadWizard},
		{Tag: "MY", TagCol: ColGreen, Title: "My Captures", Subtitle: capSub, Action: func(app *App) {
			app.refreshSavedFiles()
			app.SwitchMode(ModeSaved)
		}},
		{Tag: "HM", TagCol: ColAmber, Title: "Spectrum Map", Subtitle: "Live waterfall & peak lock", Action: func(app *App) {
			app.SwitchMode(ModeHeatmap)
		}},
		{Tag: "SN", TagCol: ColCyan, Title: "Sensors", Subtitle: "TPMS tires & weather", Action: func(app *App) {
			app.EnterSensors()
		}},
		{Tag: "ST", TagCol: ColMuted, Title: "Settings", Subtitle: "Expert, TX power & MCP", Action: func(app *App) {
			app.SettingsIdx = 0
			app.SwitchMode(ModeSettings)
		}},
	}
	if a.Settings.ExpertMode {
		items = append(items,
			homeMenuItem{Tag: "PL", TagCol: ColMagenta, Title: "Pulse Scope", Subtitle: "RSSI scope, FREQEST & RAW", Action: func(app *App) {
				app.SwitchMode(ModePulse)
			}},
			homeMenuItem{Tag: "DC", TagCol: ColMagenta, Title: "OOK Decoder", Subtitle: "Protocol log & Rolling guard", Action: func(app *App) {
				app.SwitchMode(ModeDecode)
			}},
			homeMenuItem{Tag: "PR", TagCol: ColAmber, Title: "Auto-Probe", Subtitle: "GEN:CAME-12 & DB packs", Action: func(app *App) {
				app.refreshProbePacks()
				app.SwitchMode(ModeProbe)
			}},
			homeMenuItem{Tag: "FH", TagCol: ColMagenta, Title: "Foxhunt Geiger", Subtitle: "Kalman RSSI signal finder", Action: func(app *App) {
				app.EnterFoxhunt(ModeHome)
			}},
		)
	}
	return items
}

func (a *App) clampHomeCursor(n int) {
	if n <= 0 {
		a.HomeCursor = 0
		a.HomeScroll = 0
		return
	}
	a.HomeCursor = min(max(a.HomeCursor, 0), n-1)
	if a.HomeCursor < a.HomeScroll {
		a.HomeScroll = a.HomeCursor
	}
	if a.HomeCursor >= a.HomeScroll+homeVisibleRows {
		a.HomeScroll = a.HomeCursor - homeVisibleRows + 1
	}
}

func (a *App) handleHomeKey(act KeyAction) bool {
	if a.ConfirmExit {
		if act == KeyEnter || act == KeyBack || act == KeyNav8 {
			a.stopActiveProbe()
			return true
		}
		a.ConfirmExit = false
		return false
	}
	items := a.homeMenuItems()
	switch act {
	case KeyUp:
		a.HomeCursor = (a.HomeCursor + len(items) - 1) % len(items)
		a.clampHomeCursor(len(items))
	case KeyDown:
		a.HomeCursor = (a.HomeCursor + 1) % len(items)
		a.clampHomeCursor(len(items))
	case KeyEnter, KeyNav4:
		items[min(a.HomeCursor, len(items)-1)].Action(a)
	case KeyNav5:
		a.toggleExpertMode()
	case KeyNav6:
		a.enterReadWizard()
	case KeyNav7:
		a.refreshSavedFiles()
		a.SwitchMode(ModeSaved)
	case KeyBack, KeyNav8:
		a.ConfirmExit = true
	}
	return false
}

func (a *App) toggleExpertMode() {
	a.Settings.ExpertMode = !a.Settings.ExpertMode
	a.clampHomeCursor(len(a.homeMenuItems()))
	a.persistSettings()
	if a.Settings.ExpertMode {
		a.SetToast("Expert Mode ON (+4 tools)")
	} else {
		a.SetToast("Simple Mode ON")
	}
}

func (a *App) drawHome(c *Canvas) {
	items := a.homeMenuItems()
	a.clampHomeCursor(len(items))

	c.DrawText(8, 21, "SELECT ACTION (F/X Move, Enter Open)", ColMuted, 1)
	if len(items) > homeVisibleRows {
		ind := fmt.Sprintf("%d/%d", a.HomeCursor+1, len(items))
		c.DrawText(290, 21, ind, ColMuted, 1)
	}
	for row := range homeVisibleRows {
		idx := a.HomeScroll + row
		if idx >= len(items) {
			break
		}
		a.drawHomeRow(c, idx, 32+row*22, items[idx])
	}
	if a.ConfirmExit {
		drawConfirmBanner(c, "Exit RF Heatmap? [Enter/Esc=YES]")
	}
}

func (a *App) homeNavLabels() [5]string {
	expLbl := "EXP-"
	if a.Settings.ExpertMode {
		expLbl = "EXP+"
	}
	return [5]string{"OPEN", expLbl, "READ", "SEND", "QUIT"}
}

func (a *App) drawHomeRow(c *Canvas, idx, y int, it homeMenuItem) {
	focused := idx == a.HomeCursor
	bg, border, titleCol := ColPanel, ColBorder, ColText
	if focused {
		bg, border, titleCol = ColPanelAlt, ColCyan, ColCyan
	}
	c.FillRect(6, y, 308, 20, bg)
	c.DrawRect(6, y, 308, 20, border)
	if focused {
		c.FillRect(6, y, 3, 20, ColCyan)
	}
	c.FillRect(12, y+4, 18, 12, ColBg)
	c.DrawRect(12, y+4, 18, 12, it.TagCol)
	c.DrawTextCentered(21, y+6, it.Tag, it.TagCol, 1)

	c.DrawText(36, y+6, truncate(it.Title, 16), titleCol, 1)
	c.DrawText(142, y+6, truncate(it.Subtitle, 26), ColMuted, 1)
}
