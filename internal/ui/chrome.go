package ui

import (
	"fmt"
	"math"
)

func pauseLabel(paused bool) string {
	if paused {
		return "RUN"
	}
	return "HOLD"
}

func recLabel(rec bool) string {
	if rec {
		return "STOP"
	}
	return "REC"
}

func rssiToHeight(dbm int32, maxH int) int {
	t := float64(dbm+110) / 75.0
	if t < 0 {
		return 0
	}
	if t > 1 {
		t = 1
	}
	return int(math.Round(t * float64(maxH)))
}

func truncate(s string, maxLen int) string {
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen-2] + ".."
}

func (a *App) drawHeader(c *Canvas) {
	c.FillRect(0, 0, ScreenW, 17, ColPanel)
	c.HLine(0, 17, ScreenW, ColBorder)
	a.drawHeaderTitle(c)
	a.drawHeaderBadges(c)
}

func (a *App) drawHeaderTitle(c *Canvas) {
	if a.Mode == ModeHome && !a.ShowFoxhunt && !a.ShowTrimmer && !a.ShowSensors && !a.ShowSources {
		c.FillRect(4, 3, 56, 11, ColCyan)
		c.DrawText(7, 5, "RF-HEAT", ColBg, 1)
		modeLbl, modeCol := "SIMPLE", ColMuted
		if a.Settings.ExpertMode {
			modeLbl, modeCol = "EXPERT", ColMagenta
		}
		c.DrawText(66, 5, modeLbl, modeCol, 1)
		return
	}
	title := a.Mode.Title()
	switch {
	case a.ShowFoxhunt:
		title = "FOXHUNT"
	case a.ShowTrimmer:
		title = "RAW TRIMMER"
	case a.ShowSensors:
		title = "SENSORS"
	case a.ShowSources:
		title = "DB SOURCES"
	}
	c.DrawText(6, 5, "< "+truncate(title, 18), ColCyan, 1)
	if a.showHeaderFreq() {
		freqStr := fmt.Sprintf("%.2fM", float64(a.ActiveFreq)/1e6)
		c.DrawText(134, 5, freqStr, ColText, 1)
	}
}

func (a *App) showHeaderFreq() bool {
	if a.ShowFoxhunt || a.ShowSensors {
		return true
	}
	if a.ShowTrimmer || a.ShowSources {
		return false
	}
	return a.Mode == ModeRead || a.Mode == ModeHeatmap || a.Mode == ModePulse || a.Mode == ModeDecode
}

func (a *App) drawHeaderBadges(c *Canvas) {
	if a.Settings.ExpertMode && a.Mode != ModeHome {
		drawStatusPill(c, 198, 28, "EXP", ColMagenta)
	}
	if a.Mode == ModeHeatmap && a.DiffMode && !a.ShowFoxhunt && !a.ShowTrimmer && !a.ShowSensors {
		drawStatusPill(c, 230, 36, "DIFF", ColRed)
	} else if a.Mode == ModePulse || a.Mode == ModeHeatmap {
		if a.AutoSquelch {
			drawStatusPill(c, 230, 36, "ASQ", ColGreen)
		}
	}
	if a.SimMode {
		drawStatusPill(c, 272, 44, "SIM", ColAmber)
		return
	}
	drawStatusPill(c, 272, 44, "CC1101", ColGreen)
}

func drawConfirmBanner(c *Canvas, msg string) {
	c.FillRect(14, 121, 292, 18, ColPanel)
	c.DrawRect(14, 121, 292, 18, ColAmber)
	c.DrawTextCentered(ScreenW/2, 126, truncate(msg, 46), ColAmber, 1)
}

func drawStatusPill(c *Canvas, x, w int, label string, col uint16) {
	c.FillRect(x, 3, w, 11, ColPanelAlt)
	c.DrawRect(x, 3, w, 11, col)
	c.DrawTextCentered(x+w/2, 5, label, col, 1)
}

func (a *App) drawNavBar(c *Canvas, labels [5]string) {
	c.FillRect(0, 143, ScreenW, 27, ColNavBg)
	c.HLine(0, 143, ScreenW, ColBorder)
	keys := [5]string{"4", "5", "6", "7", "8"}
	for i, cx := range NavKeyCenters {
		bx := cx - 25
		by := 146
		bw := 50
		bh := 21
		c.FillRect(bx, by, bw, bh, ColPanelAlt)
		c.DrawRect(bx, by, bw, bh, ColBorder)
		// Keycap badge.
		c.FillRect(bx+2, by+2, 9, 9, ColCyan)
		c.DrawText(bx+4, by+3, keys[i], ColBg, 1)
		// Action label centered at Y=153..160.
		c.DrawTextCentered(cx+4, 154, labels[i], ColText, 1)
	}
}

type helpPageContent struct {
	Title string
	Lines []string
}

var helpPages = []helpPageContent{
	{
		Title: "1/4 HOME MENU & NAVIGATION",
		Lines: []string{
			"F / X : Move up / down in menus",
			"Z / C : Left / Right (Freq / Pages)",
			"Enter : Open item or primary action",
			"Esc   : Step back (Home -> Exit ask)",
			"4..8  : Bottom bar labeled softkeys",
			"5 EXP : Toggle Simple / Expert Mode",
			"H : Help modal   Q : Quit immediately",
		},
	},
	{
		Title: "2/4 READ & REPLAY (AUTO-CAPTURE)",
		Lines: []string{
			"Step 1: Auto-scans 433/868/315/915MHz!",
			"  Press any remote -> auto-locks & grabs",
			"  4 AUTO/FREQ: switch Auto or fixed MHz",
			"Step 2: Captured! Shows protocol/code.",
			"  Enter / 4 SEND : Replay signal",
			"  5 SAVE : Save .sub & open Captures",
			"  6 AGAIN: Listen for next signal",
		},
	},
	{
		Title: "3/4 SPECTRUM, CAPTURES & SENSORS",
		Lines: []string{
			"Spectrum Map: Live waterfall + Peak",
			"  5 BAND  6 LOCK (jump to peak freq)",
			"  7 FOX (Geiger)  B:Base  D:EMI Diff",
			"My Captures: 4 SEND 5 TRIM 6 NEW 7 DEL",
			"Sensors: Live TPMS (433/315) & Weather",
			"  5 UNIT (kPa/PSI)  6 PROF (Profile)",
		},
	},
	{
		Title: "4/4 EXPERT MODE & MCP SERVER",
		Lines: []string{
			"Enable Expert Mode (HOME -> 5 EXP / SET)",
			"Unlocks: Pulse Scope (FREQEST, RAW),",
			"  OOK Decoder, Auto-Probe (GEN:CAME-12",
			"  & DB packs), Foxhunt, full Settings.",
			"PulseNet-RF (28 proto) + SpectrumNet-2D",
			"MCP (opt-in SET): rf-heatmap --mcp",
		},
	},
}

func (a *App) drawHelpModal(c *Canvas) {
	page := helpPages[a.HelpPage%len(helpPages)]
	c.FillRect(10, 18, 300, 128, ColPanel)
	c.DrawRect(10, 18, 300, 128, ColCyan)
	c.FillRect(11, 19, 298, 13, ColNavActive)
	c.DrawTextCentered(ScreenW/2, 22, page.Title, ColText, 1)

	for i, l := range page.Lines {
		c.DrawText(16, 36+i*12, l, ColText, 1)
	}

	c.HLine(12, 133, 296, ColBorder)
	c.DrawTextCentered(ScreenW/2, 136, "[Z/C] Page   [Enter/H] Close", ColAmber, 1)
}
