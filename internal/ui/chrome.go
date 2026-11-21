package ui

import (
	"fmt"
	"math"

	"rf-heatmap/internal/cc1101"
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

	c.FillRect(4, 3, 48, 11, ColCyan)
	c.DrawText(7, 5, "RF-HEAT", ColBg, 1)
	drawStatusPill(c, 56, 48, a.Mode.String(), ColCyan)

	freqStr := fmt.Sprintf("%.2fM %s", float64(a.ActiveFreq)/1e6, cc1101.GetPresetSpec(a.Preset).Name)
	c.DrawText(110, 5, freqStr, ColText, 1)

	asqCol := ColMuted
	if a.AutoSquelch {
		asqCol = ColGreen
	}
	drawStatusPill(c, 196, 30, "ASQ", asqCol)

	lckCol := ColMuted
	if a.AutoLock {
		lckCol = ColCyan
	}
	drawStatusPill(c, 230, 34, "LOCK", lckCol)

	if a.SimMode {
		drawStatusPill(c, 272, 44, "SIM", ColAmber)
		return
	}
	drawStatusPill(c, 272, 44, "CC1101", ColGreen)
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
		Title: "1/4 CONTROLS & STATUS",
		Lines: []string{
			"4..8   : Bottom bar quick actions",
			"Tab/8  : Switch mode (5 tabs +SET)",
			"F / X  : Up / Down (Band/Squelch/SET)",
			"Z / C  : Left / Right (Cursor/Freq)",
			"B / M  : Cycle ISM Band / Modulation",
			"S / L  : Toggle Auto Squelch / Lock",
			"Enter  : Lock Peak / Toggle / TX",
			"H / Q  : Toggle Help / Quit safely",
		},
	},
	{
		Title: "2/4 HEATMAP & WATERFALL",
		Lines: []string{
			"Top chart: Live RSSI bars per ch.",
			"Amber ticks: Peak-Hold max signal.",
			"Cyan box: Cursor channel (Z/C move).",
			"Bottom: Thermal Waterfall over time:",
			" Navy(-110) -> Purple -> Orange ->",
			" Yellow(-35dBm) = strongest bursts!",
			"PK=strongest freq, CUR=cursor freq.",
			"Press Enter/6 to LOCK & jump to PULSE",
		},
	},
	{
		Title: "3/4 PULSE, SQUELCH & MOD",
		Lines: []string{
			"SQ (Amber line): Noise gate in dBm.",
			" Green bars = signal above squelch.",
			"AM650/AM270: OOK (keyfobs, gates).",
			" 650kHz=wide catch, 270kHz=low noise.",
			"FM238/FM476: 2-FSK (2.38/47.6kHz dev).",
			"4(REC) captures RAW GDO0 pulses.",
			"5(TX) replays, 6(SAVE) writes .sub",
			" files compatible with Flipper Zero.",
		},
	},
	{
		Title: "4/4 DECODE, SAVED & SET",
		Lines: []string{
			"DECODE listens for OOK fixed codes:",
			" Princeton, CAME, Linear protocols.",
			"SAVED lists .sub files from:",
			" ~/rf-heatmap/captures & ~/subg/files",
			" 4(TX) 5(LOAD) 6(DEL) 7(PURGE)",
			"SET: Opt-in MCP, ML & Reset Baseline",
			" Enable MCP to run over SSH stdio:",
			" ssh <dev> rf-heatmap --mcp",
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
