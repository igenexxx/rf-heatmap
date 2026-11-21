package ui

import "fmt"

func (a *App) handleDecodeKey(act KeyAction) {
	switch act {
	case KeyNav4, KeySpace:
		a.Paused = !a.Paused
	case KeyNav5, KeyEnter:
		if a.LastDecoded != nil {
			a.SetToast("TX " + a.LastDecoded.Protocol)
		} else {
			a.SetToast("No decoded key yet")
		}
	case KeyNav6:
		a.saveDecodedKey()
	case KeyNav7:
		a.DecodedLog = nil
		a.LastDecoded = nil
		a.SetToast("Log cleared")
	}
}

func (a *App) triggerReplay() {
	if len(a.CapturedRaw) == 0 && a.LastDecoded == nil {
		a.SetToast("Nothing to TX")
		return
	}
	a.SetToast(fmt.Sprintf("TX %.2f MHz", float64(a.ActiveFreq)/1e6))
}

func (a *App) drawDecode(c *Canvas) {
	a.drawDecodeBanner(c)
	a.drawDecodeLog(c)
}

func (a *App) drawDecodeBanner(c *Canvas) {
	c.FillRect(6, 21, 308, 42, ColPanel)
	c.DrawRect(6, 21, 308, 42, ColCyan)
	if a.LastDecoded != nil {
		d := a.LastDecoded
		c.DrawText(12, 26, fmt.Sprintf("%s  (%d-bit, TE=%dus)", d.Protocol, d.Bits, d.TE), ColGreen, 1)
		if d.Denoised {
			c.FillRect(244, 24, 64, 11, ColPanelAlt)
			c.DrawRect(244, 24, 64, 11, ColMagenta)
			c.DrawTextCentered(276, 26, "ML-CLEAN", ColMagenta, 1)
		}
		c.DrawText(12, 41, d.KeyHex, ColText, 1)
		return
	}
	if a.HasPrediction {
		predCol := ColCyan
		if a.LastPrediction.IsNoise {
			predCol = ColAmber
		}
		c.DrawText(12, 26, fmt.Sprintf("ML: %s (%.0f%%)", a.LastPrediction.Label, a.LastPrediction.Confidence*100), predCol, 1)
		c.DrawText(12, 41, fmt.Sprintf("TE:%.0fus  LONG:%.0fus  JIT:%.0f%%  GL:%d",
			a.LastCluster.ShortUs, a.LastCluster.LongUs, a.LastCluster.JitterRatio*100, a.LastCluster.GlitchesRemoved), ColMuted, 1)
		return
	}
	if a.LastCluster.Valid {
		c.DrawText(12, 26, fmt.Sprintf("ML: %s", a.LastCluster.ModulationHint), ColMagenta, 1)
		c.DrawText(12, 41, fmt.Sprintf("TE:%.0fus  LONG:%.0fus  JIT:%.0f%%  GL:%d",
			a.LastCluster.ShortUs, a.LastCluster.LongUs, a.LastCluster.JitterRatio*100, a.LastCluster.GlitchesRemoved), ColCyan, 1)
		return
	}
	c.DrawTextCentered(ScreenW/2, 33, "Listening for OOK frames...", ColMuted, 1)
	c.DrawTextCentered(ScreenW/2, 46, "Princeton / CAME / Linear (K-Means)", ColCyan, 1)
}

func (a *App) drawDecodeLog(c *Canvas) {
	c.DrawText(6, 68, "Recent Decoded Keys:", ColMuted, 1)
	c.FillRect(6, 78, 308, 62, ColPanel)
	c.DrawRect(6, 78, 308, 62, ColBorder)
	for i, k := range a.DecodedLog {
		if i >= 5 {
			break
		}
		y := 83 + i*11
		line := fmt.Sprintf("%-9s %2db  %s", k.Protocol, k.Bits, k.KeyHex)
		if len(line) > 48 {
			line = line[:48]
		}
		c.DrawText(12, y, line, ColText, 1)
	}
}
