package ui

import (
	"fmt"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/subfile"
)

func (a *App) handleDecodeKey(act KeyAction) {
	switch act {
	case KeyRank:
		a.EnterSensors()
	case KeyNav4, KeySpace:
		a.Paused = !a.Paused
	case KeyNav5, KeyEnter:
		if a.LastDecoded == nil {
			a.SetToast("No decoded key yet")
			return
		}
		if a.isRollingActive() && time.Now().After(a.rollingTxUntil) {
			a.rollingTxUntil = time.Now().Add(3 * time.Second)
			a.SetToast("ROLLING CODE! TX may desync - 5 again")
			return
		}
		a.rollingTxUntil = time.Time{}
		a.transmitDecodedKey(a.LastDecoded)
	case KeyNav6:
		a.saveDecodedKey()
	case KeyNav7:
		a.DecodedLog = nil
		a.LastDecoded = nil
		a.LastRolling = subfile.RollingReport{}
		a.rollingTxUntil = time.Time{}
		if a.RollingDetector != nil {
			a.RollingDetector.Reset()
		}
		a.SetToast("Log cleared")
	}
}

func (a *App) isRollingActive() bool {
	return (a.LastDecoded != nil && a.LastDecoded.IsRolling) || a.LastRolling.IsRolling
}

func (a *App) triggerReplay() {
	if len(a.CapturedRaw) > 0 {
		sf := &subfile.SubFile{
			FileType: "Flipper SubGhz RAW File",
			Version:  1,
			FreqHz:   a.ActiveFreq,
			Preset:   cc1101.GetPresetSpec(a.Preset).SubFileName,
			Protocol: "RAW",
			RawData:  append([]int32(nil), a.CapturedRaw...),
		}
		ref := library.EntryRef{Name: fmt.Sprintf("raw_%.2fM.sub", float64(a.ActiveFreq)/1e6), File: sf}
		a.submitTX(ref, sf, -1, false)
		return
	}
	if a.LastDecoded != nil {
		a.transmitDecodedKey(a.LastDecoded)
		return
	}
	a.SetToast("Nothing to TX")
}

func (a *App) transmitDecodedKey(d *subfile.DecodedKey) {
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   a.ActiveFreq,
		Preset:   cc1101.GetPresetSpec(a.Preset).SubFileName,
		Protocol: d.Protocol,
		Bit:      d.Bits,
		Key:      d.KeyHex,
		TE:       d.TE,
	}
	ref := library.EntryRef{Name: d.Protocol + ".sub", File: sf}
	a.submitTX(ref, sf, -1, false)
}

func (a *App) drawDecode(c *Canvas) {
	a.drawDecodeBanner(c)
	a.drawDecodeLog(c)
}

func (a *App) drawDecodeBanner(c *Canvas) {
	c.FillRect(6, 21, 308, 42, ColPanel)
	c.DrawRect(6, 21, 308, 42, ColCyan)
	if a.LastDecoded != nil {
		a.drawDecodedBannerContent(c, a.LastDecoded)
		if a.HasFreqOffset {
			drawFreqDriftBadge(c, 236, 52, a.FreqOffsetHz)
		}
		return
	}
	if a.HasFreqOffset {
		drawFreqDriftBadge(c, 236, 52, a.FreqOffsetHz)
	}
	if a.HasPrediction {
		predCol := ColCyan
		if a.LastPrediction.IsNoise {
			predCol = ColAmber
		}
		label := a.LastPrediction.Label
		conf := a.LastPrediction.Confidence
		if a.LastPrediction.ProtocolLabel != "" && !a.LastPrediction.IsNoise {
			label = a.LastPrediction.ProtocolLabel
			conf = a.LastPrediction.ProtocolConfidence
		}
		c.DrawText(12, 26, fmt.Sprintf("ML: %s (%.0f%%)", truncate(label, 22), conf*100), predCol, 1)
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
	c.DrawTextCentered(ScreenW/2, 46, "OOK / KeeLoq / Somfy [R:SENSORS]", ColCyan, 1)
}

func (a *App) drawDecodedBannerContent(c *Canvas, d *subfile.DecodedKey) {
	rolling := d.IsRolling || a.LastRolling.IsRolling
	titleCol := ColGreen
	if rolling {
		titleCol = ColAmber
	}
	c.DrawText(12, 26, fmt.Sprintf("%s (%db TE=%dus)", d.Protocol, d.Bits, d.TE), titleCol, 1)

	mlX := 244
	if rolling {
		c.FillRect(248, 24, 60, 11, ColPanelAlt)
		c.DrawRect(248, 24, 60, 11, ColAmber)
		c.DrawTextCentered(278, 26, "ROLLING", ColAmber, 1)
		mlX = 180
	}
	if d.Denoised {
		cleanTag := "ML-CLEAN"
		if d.DenoisedBy == "pulsenet" {
			cleanTag = "PN-CLEAN"
		}
		c.FillRect(mlX, 24, 64, 11, ColPanelAlt)
		c.DrawRect(mlX, 24, 64, 11, ColMagenta)
		c.DrawTextCentered(mlX+32, 26, cleanTag, ColMagenta, 1)
	}
	if rolling {
		c.DrawText(12, 40, formatRollingBannerLine(d, a.LastRolling), ColText, 1)
		c.DrawText(12, 52, "! Rolling code - replay may desync", ColAmber, 1)
		return
	}
	line2 := d.KeyHex
	if d.TriState != "" {
		line2 = fmt.Sprintf("%s [%s]", d.KeyHex, d.TriState)
	}
	c.DrawText(12, 41, truncate(line2, 48), ColText, 1)
}

func formatRollingBannerLine(d *subfile.DecodedKey, rep subfile.RollingReport) string {
	if d.EncryptedHex != "" {
		return fmt.Sprintf("SER:%s HOP:%s BTN:%X", d.SerialHex, d.EncryptedHex, d.Button)
	}
	if d.SerialHex != "" && d.Counter > 0 {
		return fmt.Sprintf("SER:%s CNT:%04X BTN:%X", d.SerialHex, d.Counter, d.Button)
	}
	if rep.IsRolling && rep.ChangingBits > 0 {
		return fmt.Sprintf("HOP DETECTED (static:%db diff:%db)", rep.StaticBits, rep.ChangingBits)
	}
	return d.KeyHex
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
