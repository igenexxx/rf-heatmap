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
	if a.Mode == ModeHome && !a.ShowFoxhunt && !a.ShowTrimmer && !a.ShowSnapshot && !a.ShowSensors && !a.ShowSources {
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
	case a.ShowSnapshot:
		title = "SIGNAL SNAPSHOT"
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
		freq := a.ActiveFreq
		if a.ShowSnapshot && a.SnapFreqHz > 0 {
			freq = a.SnapFreqHz
		}
		freqStr := fmt.Sprintf("%.2fM", float64(freq)/1e6)
		c.DrawText(134, 5, freqStr, ColText, 1)
	}
}

func (a *App) showHeaderFreq() bool {
	if a.ShowSnapshot || a.ShowFoxhunt || a.ShowSensors {
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
