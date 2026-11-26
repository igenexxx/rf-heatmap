package ui

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/config"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

const (
	pulseFreqStepHz        = 50_000
	pulseMinSquelchDBm     = -105
	pulseMaxSquelchDBm     = -30
	pulseSquelchStepDBm    = 2
	pulseSquelchKStep      = 0.5
	pulseFPMinSimilarity   = 0.88
	pulseAnomalyHighlightZ = 3.5
	pulseScopeX            = 6
	pulseScopeY            = 59
	pulseScopeW            = 308
	pulseScopeH            = 80
	maxDecodedLogEntries   = 6
)

func (a *App) handlePulseKey(act KeyAction) {
	switch act {
	case KeyUp:
		a.adjustSquelch(pulseSquelchKStep, pulseSquelchStepDBm)
	case KeyDown:
		a.adjustSquelch(-pulseSquelchKStep, -pulseSquelchStepDBm)
	case KeyLeft:
		a.nudgeActiveFreq(-pulseFreqStepHz)
	case KeyRight:
		a.nudgeActiveFreq(pulseFreqStepHz)
	case KeyNav4, KeySpace:
		a.toggleRecording()
	case KeyNav5, KeyEnter:
		a.triggerReplay()
	case KeyNav6:
		a.saveCurrentRaw()
	case KeyNav7:
		a.cyclePreset()
	case KeyFoxhunt:
		a.EnterFoxhunt(ModePulse)
	case KeyTrim:
		a.EnterTrimmerFromPulse()
	case KeyInspect:
		a.EnterSnapshotFromPulse()
	}
}

func (a *App) adjustSquelch(deltaK float64, deltaDBm int32) {
	if a.AutoSquelch && a.LiveBaseline != nil {
		nextK := min(config.MaxSquelchK, max(config.MinSquelchK, a.LiveBaseline.SquelchK()+deltaK))
		a.Settings.SquelchK = nextK
		a.LiveBaseline.SetSquelchK(nextK)
		if a.SweepBaseline != nil {
			a.SweepBaseline.SetSquelchK(nextK)
		}
		return
	}
	a.SquelchDBm = min(pulseMaxSquelchDBm, max(pulseMinSquelchDBm, a.SquelchDBm+deltaDBm))
}

func (a *App) nudgeActiveFreq(deltaHz int64) {
	a.ActiveFreq = uint32(int64(a.ActiveFreq) + deltaHz)
	if a.LiveBaseline != nil {
		a.LiveBaseline.Reset()
	}
}

func (a *App) toggleRecording() {
	a.Recording = !a.Recording
	if a.Recording {
		a.CapturedRaw = a.CapturedRaw[:0]
		a.LastCluster = rfml.PulseClusters{}
		a.HasPrediction = false
		a.SetToast("Recording RAW...")
		return
	}
	a.analyzeCapturedRaw()
}

func (a *App) analyzeCapturedRaw() {
	snr := float64(a.LiveRSSI - a.EffectiveSquelchDBm() + 10)
	a.LastPrediction, _, a.LastCluster = rfml.ClassifyPulses(a.CapturedRaw, a.ActiveFreq, snr)
	a.HasPrediction = len(a.CapturedRaw) > 0
	if k, ok := subfile.DecodeOOK(a.CapturedRaw); ok {
		if a.RollingDetector != nil {
			if rep := a.RollingDetector.Observe(a.ActiveFreq, a.CapturedRaw); rep.IsRolling {
				a.LastRolling = rep
				k.IsRolling = true
			}
		}
		a.LastDecoded = &k
	}
	a.HasMatch = false
	if a.FPIndex != nil && len(a.CapturedRaw) > 0 {
		emb := rfml.ComputeEmbeddingFromRaw(a.CapturedRaw, a.ActiveFreq, cc1101.GetPresetSpec(a.Preset).Name)
		if m, ok := a.FPIndex.Match(emb, a.ActiveFreq, pulseFPMinSimilarity); ok {
			a.LastMatch = m
			a.HasMatch = true
			a.SetToast(fmt.Sprintf("Match: %s (%.0f%%)", truncate(m.Name, 14), m.Similarity*100))
			return
		}
	}
	a.SetToast(fmt.Sprintf("%s (%.0f%%)", a.LastPrediction.Label, a.LastPrediction.Confidence*100))
}

func (a *App) stepLiveChannel() {
	if a.Radio != nil {
		// Never strobe SIDLE/StartReceive while live GDO0 RX capture is active,
		// otherwise continuous pulse reception is interrupted every 55 ms tick.
		if !a.rxActive {
			_ = a.Radio.StartReceive(a.ActiveFreq)
		}
		if v, err := a.Radio.ReadRSSIDBm(); err == nil {
			a.LiveRSSI = v
		}
		if off, err := a.Radio.ReadFreqOffsetHz(); err == nil {
			a.FreqOffsetHz = off
			a.HasFreqOffset = true
		}
	} else {
		a.LiveRSSI = a.sampleSimLiveChannel()
	}
	a.updateLiveTrackers()
}

func (a *App) sampleSimLiveChannel() int32 {
	// Realistic bursty OOK remote signal every ~18 ticks.
	base := int32(-104 + rand.Intn(5))
	if a.tick%18 >= 4 {
		return base
	}
	base = -54 + int32(rand.Intn(8))
	if a.Recording {
		a.CapturedRaw = append(a.CapturedRaw, 350, -1050, 1050, -350, 350, -1050)
	}
	if a.tick%18 == 1 {
		k := subfile.DecodedKey{
			Protocol: "Princeton",
			Bits:     24,
			KeyHex:   fmt.Sprintf("00 00 00 00 00 A5 5A %02X", uint8(a.tick)),
			TE:       350,
		}
		a.LastDecoded = &k
		a.DecodedLog = append([]subfile.DecodedKey{k}, a.DecodedLog...)
		if len(a.DecodedLog) > maxDecodedLogEntries {
			a.DecodedLog = a.DecodedLog[:maxDecodedLogEntries]
		}
	}
	return base
}

func (a *App) updateLiveTrackers() {
	if a.LiveBaseline != nil {
		a.LiveBaseline.UpdateBin(0, a.LiveRSSI)
	}
	if a.Foxhunt != nil {
		a.LastKalman = a.Foxhunt.Update(a.LiveRSSI)
	}
	if a.Activity != nil {
		a.LastActivity = a.Activity.Observe(time.Now(), a.LiveRSSI >= a.EffectiveSquelchDBm())
	}
	a.RSSIHistory[a.RssiHistHead] = a.LiveRSSI
	a.RssiHistHead = (a.RssiHistHead + 1) % len(a.RSSIHistory)
}

func (a *App) drawPulse(c *Canvas) {
	effSq := a.EffectiveSquelchDBm()
	a.drawPulseTopCard(c, effSq)
	a.drawPulseScope(c, effSq)
}

func (a *App) drawPulseTopCard(c *Canvas, effSq int32) {
	c.FillRect(6, 21, 308, 34, ColPanel)
	c.DrawRect(6, 21, 308, 34, ColBorder)

	c.DrawText(12, 26, fmt.Sprintf("%.2f MHz", float64(a.ActiveFreq)/1e6), ColCyan, 2)

	if a.AutoSquelch && a.LiveBaseline != nil && a.LiveBaseline.Ready(0) {
		z := a.LiveBaseline.AnomalyScore(0, a.LiveRSSI)
		badgeCol := ColCyan
		if z >= pulseAnomalyHighlightZ {
			badgeCol = ColMagenta
		}
		c.DrawText(142, 25, "AUTO SQ", badgeCol, 1)
		c.DrawText(142, 34, fmt.Sprintf("Z:%+.1f", z), badgeCol, 1)
	}

	rssiColor := ColMuted
	if a.LiveRSSI >= effSq {
		rssiColor = ColGreen
	}
	c.DrawText(206, 26, fmt.Sprintf("%4ddBm", a.LiveRSSI), rssiColor, 2)
	if a.HasFreqOffset {
		c.DrawText(12, 44, fmt.Sprintf("ASQ:%ddBm RAW:%d", effSq, len(a.CapturedRaw)), ColMuted, 1)
		drawFreqDriftBadge(c, 236, 44, a.FreqOffsetHz)
		return
	}
	if a.AutoSquelch && a.LiveBaseline != nil {
		c.DrawText(12, 44, fmt.Sprintf("ASQ:%ddBm(k%.1f) RAW:%d [S:MAN F/X:K]", effSq, a.LiveBaseline.SquelchK(), len(a.CapturedRaw)), ColMuted, 1)
		return
	}
	c.DrawText(12, 44, fmt.Sprintf("SQ:%ddBm  RAW:%d  [S:AUTO F/X:SQ]", a.SquelchDBm, len(a.CapturedRaw)), ColMuted, 1)
}

func drawFreqDriftBadge(c *Canvas, x, y int, offsetHz int32) {
	col := ColCyan
	if offsetHz > 5000 || offsetHz < -5000 {
		col = ColAmber
	}
	c.DrawText(x, y, fmt.Sprintf("DF:%+.1fkHz", float64(offsetHz)/1000.0), col, 1)
}

func (a *App) drawPulseScope(c *Canvas, effSq int32) {
	c.FillRect(pulseScopeX, pulseScopeY, pulseScopeW, pulseScopeH, ColPanel)
	c.DrawRect(pulseScopeX, pulseScopeY, pulseScopeW, pulseScopeH, ColBorder)

	a.drawPulseFoxhuntStrip(c)
	a.drawPulseThresholds(c, effSq)

	bars := len(a.RSSIHistory)
	barW := (pulseScopeW - 8) / bars
	for i := range bars {
		idx := (a.RssiHistHead + i) % bars
		v := a.RSSIHistory[idx]
		h := rssiToHeight(v, pulseScopeH-6)
		col := ThermalColor(v)
		if v >= effSq {
			col = ColGreen
		}
		if h > 0 {
			c.FillRect(pulseScopeX+4+i*barW, pulseScopeY+pulseScopeH-3-h, max(2, barW-1), h, col)
		}
	}
}

func (a *App) drawPulseFoxhuntStrip(c *Canvas) {
	if a.LastKalman.Initialized {
		trendCol, arrow := ColMuted, "="
		switch a.LastKalman.Trend {
		case "CLOSER":
			trendCol, arrow = ColGreen, "^"
		case "FARTHER":
			trendCol, arrow = ColAmber, "v"
		}
		c.DrawText(pulseScopeX+6, pulseScopeY+3, fmt.Sprintf("FOX:%.0fdBm %s%s %d%%",
			a.LastKalman.FilteredDBm, arrow, a.LastKalman.Trend, a.LastKalman.ProximityPct), trendCol, 1)
	}
	if a.LastActivity.Label == "" {
		return
	}
	actCol := ColMuted
	switch a.LastActivity.Kind {
	case "BEACON":
		actCol = ColCyan
	case "CW":
		actCol = ColMagenta
	case "BURST":
		actCol = ColGreen
	}
	c.DrawText(pulseScopeX+226, pulseScopeY+3, a.LastActivity.Label, actCol, 1)
}

func (a *App) drawPulseThresholds(c *Canvas, effSq int32) {
	if a.LiveBaseline != nil && a.LiveBaseline.Ready(0) {
		floorH := rssiToHeight(int32(math.Round(a.LiveBaseline.MeanDBm(0))), pulseScopeH-6)
		floorY := pulseScopeY + pulseScopeH - 3 - floorH
		for x := pulseScopeX + 2; x < pulseScopeX+pulseScopeW-2; x += 6 {
			c.HLine(x, floorY, 2, ColMuted)
		}
	}
	sqLineCol := ColAmber
	if a.AutoSquelch {
		sqLineCol = ColCyan
	}
	sqH := rssiToHeight(effSq, pulseScopeH-6)
	sqY := pulseScopeY + pulseScopeH - 3 - sqH
	for x := pulseScopeX + 2; x < pulseScopeX+pulseScopeW-2; x += 4 {
		c.HLine(x, sqY, 2, sqLineCol)
	}
}
