package ui

import (
	"fmt"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/subfile"
)

const (
	autoCaptureMinPulses   = 24
	autoCaptureTriggerDBm  = -72
	autoCaptureLockWindow  = 900 * time.Millisecond
	autoCaptureIdleFlushMs = 300 * time.Millisecond
)

var readQuickFreqsHz = []uint32{
	433_920_000,
	868_350_000,
	315_000_000,
	915_000_000,
}

var autoScanFreqsHz = []uint32{
	433_920_000,
	868_350_000,
	315_000_000,
	915_000_000,
	433_420_000,
	868_800_000,
}

func (a *App) enterReadWizard() {
	a.resetReadWizard(true)
}

func (a *App) enterReadWizardLocked(freqHz uint32) {
	a.ActiveFreq = freqHz
	a.resetReadWizard(false)
}

func (a *App) resetReadWizard(autoScan bool) {
	a.ReadCaptured = false
	a.ReadAutoScan = autoScan
	a.ReadAutoLocked = false
	a.ReadNoiseWarn = false
	a.ReadRollWarn = false
	a.Recording = false
	a.Paused = false
	a.CapturedRaw = a.CapturedRaw[:0]
	a.LastDecoded = nil
	a.HasPrediction = false
	a.LastRolling = subfile.RollingReport{}
	a.rollingTxUntil = time.Time{}
	a.noiseSaveWarnUntil = time.Time{}
	a.readLockUntil = time.Time{}
	a.readLastPulseAt = time.Time{}
	a.ReadFreqIdx = nearestReadFreqIdx(a.ActiveFreq)
	a.SwitchMode(ModeRead)
}

func nearestReadFreqIdx(freq uint32) int {
	best, bestDiff := 0, int64(1<<60)
	for i, f := range readQuickFreqsHz {
		d := int64(freq) - int64(f)
		if d < 0 {
			d = -d
		}
		if d < bestDiff {
			best, bestDiff = i, d
		}
	}
	return best
}

func (a *App) stepReadWizard() {
	if a.ReadAutoScan && !a.ReadAutoLocked && !a.Recording {
		a.stepReadAutoScan()
		return
	}
	a.stepLiveChannel()
	a.checkReadAutoFinalize()
}

func (a *App) stepReadAutoScan() {
	var bestFreq uint32 = autoScanFreqsHz[0]
	var bestRSSI int32 = -120
	if a.Radio != nil {
		for _, f := range autoScanFreqsHz {
			_ = a.Radio.StartReceive(f)
			time.Sleep(sweepSettleDelay)
			if v, err := a.Radio.ReadRSSIDBm(); err == nil && v > bestRSSI {
				bestRSSI = v
				bestFreq = f
			}
		}
	} else {
		bestFreq = autoScanFreqsHz[int(a.tick)%len(autoScanFreqsHz)]
		bestRSSI = defaultFloorDBm
	}
	a.LiveRSSI = bestRSSI
	a.ActiveFreq = bestFreq
	thresh := max(a.EffectiveSquelchDBm(), autoCaptureTriggerDBm)
	if bestRSSI >= thresh {
		a.ReadAutoLocked = true
		a.readLockUntil = time.Now().Add(autoCaptureLockWindow)
		a.readLastPulseAt = time.Time{}
		a.CapturedRaw = a.CapturedRaw[:0]
		a.SetToast(fmt.Sprintf("Signal @ %.2fM - capturing!", float64(bestFreq)/1e6))
	}
}

func (a *App) checkReadAutoFinalize() {
	if a.Recording {
		return
	}
	now := time.Now()
	hasPulses := len(a.CapturedRaw) >= autoCaptureMinPulses
	idleDone := hasPulses && !a.readLastPulseAt.IsZero() && now.Sub(a.readLastPulseAt) >= autoCaptureIdleFlushMs
	lockExpired := a.ReadAutoLocked && now.After(a.readLockUntil)

	if !idleDone && !lockExpired {
		return
	}
	if !hasPulses {
		a.ReadAutoLocked = false
		return
	}
	a.analyzeCapturedRaw()
	if a.LastDecoded != nil || (a.HasPrediction && !a.LastPrediction.IsNoise) {
		a.ReadCaptured = true
		a.ReadAutoLocked = false
		a.SetToast("Auto-captured! 4=SEND 5=SAVE")
		return
	}
	// Reject noise burst silently and keep listening/scanning.
	a.CapturedRaw = a.CapturedRaw[:0]
	a.HasPrediction = false
	a.ReadAutoLocked = false
	a.readLastPulseAt = time.Time{}
}

func (a *App) handleReadKey(act KeyAction) {
	if a.ReadNoiseWarn {
		if act == KeyEnter || act == KeyNav5 {
			a.ReadNoiseWarn = false
			a.saveReadAndOpenCaptures(true)
			return
		}
		a.ReadNoiseWarn = false
		return
	}
	if a.ReadRollWarn {
		if act == KeyEnter || act == KeyNav4 {
			a.ReadRollWarn = false
			a.replayReadCapture(true)
			return
		}
		a.ReadRollWarn = false
		return
	}
	if !a.ReadCaptured {
		a.handleReadListeningKey(act)
		return
	}
	a.handleReadCapturedKey(act)
}

func (a *App) handleReadListeningKey(act KeyAction) {
	switch act {
	case KeyLeft:
		a.cycleReadFreq(-1)
	case KeyRight, KeyNav4, KeyBand:
		a.cycleReadFreq(1)
	case KeyNav5, KeyMod:
		a.cyclePreset()
	case KeyNav6, KeySpace, KeyEnter:
		a.toggleReadManualRec()
	case KeyNav7:
		if a.Settings.ExpertMode {
			a.SwitchMode(ModePulse)
		}
	case KeyBack, KeyNav8:
		a.goHome()
	}
}

func (a *App) handleReadCapturedKey(act KeyAction) {
	switch act {
	case KeyEnter, KeyNav4:
		a.replayReadCapture(false)
	case KeyNav5:
		a.saveReadAndOpenCaptures(false)
	case KeyNav6, KeySpace:
		a.resetReadWizard(a.ReadAutoScan)
		a.SetToast("Ready - press remote button")
	case KeyNav7, KeyBand:
		a.cycleReadFreq(1)
		a.resetReadWizard(a.ReadAutoScan)
	case KeyBack, KeyNav8:
		a.goHome()
	}
}

func (a *App) cycleReadFreq(delta int) {
	// Cycle: AUTO -> 433.92 -> 868.35 -> 315.00 -> 915.00 -> AUTO.
	n := len(readQuickFreqsHz)
	if a.ReadAutoScan {
		a.ReadAutoScan = false
		a.ReadAutoLocked = false
		if delta > 0 {
			a.ReadFreqIdx = 0
		} else {
			a.ReadFreqIdx = n - 1
		}
		a.ActiveFreq = readQuickFreqsHz[a.ReadFreqIdx]
		a.SetToast(fmt.Sprintf("Fixed: %.2f MHz", float64(a.ActiveFreq)/1e6))
		return
	}
	next := a.ReadFreqIdx + delta
	if next < 0 || next >= n {
		a.ReadAutoScan = true
		a.ReadAutoLocked = false
		a.SetToast("Auto-Scan: ALL ISM freqs")
		return
	}
	a.ReadFreqIdx = next
	a.ActiveFreq = readQuickFreqsHz[a.ReadFreqIdx]
	if a.LiveBaseline != nil {
		a.LiveBaseline.Reset()
	}
	a.SetToast(fmt.Sprintf("Fixed: %.2f MHz", float64(a.ActiveFreq)/1e6))
}

func (a *App) toggleReadManualRec() {
	if !a.Recording {
		a.ReadAutoScan = false
		a.ReadAutoLocked = false
		a.Recording = true
		a.CapturedRaw = a.CapturedRaw[:0]
		a.SetToast("Recording RAW... press 6 STOP")
		return
	}
	a.Recording = false
	if len(a.CapturedRaw) == 0 && a.SimMode {
		a.CapturedRaw = []int32{-10500, 350, -1050, 1050, -350, 350, -1050, -10500}
	}
	a.analyzeCapturedRaw()
	a.ReadCaptured = true
}

func (a *App) replayReadCapture(force bool) {
	if !force && a.isRollingActive() {
		a.ReadRollWarn = true
		return
	}
	if a.LastDecoded != nil {
		a.transmitDecodedKey(a.LastDecoded)
		return
	}
	a.triggerReplay()
}

func (a *App) saveReadAndOpenCaptures(force bool) {
	if !force && a.HasPrediction && a.LastPrediction.IsNoise {
		a.ReadNoiseWarn = true
		return
	}
	a.noiseSaveWarnUntil = time.Now().Add(5 * time.Second)
	if a.LastDecoded != nil {
		a.saveDecodedKey()
	} else {
		a.saveCurrentRaw()
	}
	a.refreshSavedFiles()
	if len(a.SavedFiles) > 0 {
		a.SavedIdx = len(a.SavedFiles) - 1
	}
	a.SwitchMode(ModeSaved)
}

func (a *App) drawRead(c *Canvas) {
	if !a.ReadCaptured {
		a.drawReadListening(c)
		return
	}
	a.drawReadCaptured(c)
}

func (a *App) readNavLabels() [5]string {
	if !a.ReadCaptured {
		s4 := "FREQ"
		if a.ReadAutoScan {
			s4 = "AUTO"
		}
		s6 := "REC"
		if a.Recording {
			s6 = "STOP"
		}
		s7 := ""
		if a.Settings.ExpertMode {
			s7 = "LAB"
		}
		return [5]string{s4, "MOD", s6, s7, "MENU"}
	}
	return [5]string{"SEND", "SAVE", "AGAIN", "FREQ", "MENU"}
}

func (a *App) drawReadListening(c *Canvas) {
	border, title := ColCyan, "STEP 1: PRESS REMOTE BUTTON"
	if a.Recording {
		border, title = ColRed, "RECORDING RAW PULSES..."
	} else if a.ReadAutoLocked {
		border, title = ColGreen, "SIGNAL FOUND! CAPTURING..."
	} else if a.ReadAutoScan {
		title = "AUTO-CAPTURE: PRESS REMOTE"
	}
	c.FillRect(6, 22, 308, 114, ColPanel)
	c.DrawRect(6, 22, 308, 114, border)
	c.DrawTextCentered(ScreenW/2, 34, title, border, 1)

	cfg := fmt.Sprintf("Tuned: %.2f MHz  |  %s", float64(a.ActiveFreq)/1e6, cc1101.GetPresetSpec(a.Preset).Name)
	if a.ReadAutoScan && !a.ReadAutoLocked {
		cfg = fmt.Sprintf("Scanning 433/868/315/915M (%s)", cc1101.GetPresetSpec(a.Preset).Name)
	}
	c.DrawTextCentered(ScreenW/2, 52, cfg, ColText, 1)
	c.DrawTextCentered(ScreenW/2, 66, "Auto-locks & saves prompt on signal", ColMuted, 1)

	c.DrawText(22, 88, fmt.Sprintf("RSSI %4d dBm", a.LiveRSSI), ColCyan, 1)
	barX, barY, barW, barH := 112, 87, 184, 10
	c.FillRect(barX, barY, barW, barH, ColPanelAlt)
	c.DrawRect(barX, barY, barW, barH, ColBorder)
	fill := rssiToHeight(a.LiveRSSI, barW-2)
	fillCol := ColGreen
	if a.LiveRSSI < a.EffectiveSquelchDBm() {
		fillCol = ColMuted
	}
	if fill > 0 {
		c.FillRect(barX+1, barY+1, fill, barH-2, fillCol)
	}

	hint := "4 AUTO/FREQ: switch Auto-Scan or Fixed"
	if a.SimMode {
		hint = "SIM mode: press 6 REC then 6 STOP"
	}
	c.DrawTextCentered(ScreenW/2, 112, hint, ColAmber, 1)
}

func (a *App) drawReadCaptured(c *Canvas) {
	border, title := ColGreen, "STEP 2: SIGNAL CAPTURED!"
	if a.isRollingActive() {
		border, title = ColAmber, "CAPTURED (ROLLING CODE!)"
	} else if a.HasPrediction && a.LastPrediction.IsNoise {
		border, title = ColAmber, "CAPTURED (LOOKS LIKE NOISE)"
	}
	c.FillRect(6, 22, 308, 114, ColPanel)
	c.DrawRect(6, 22, 308, 114, border)
	c.DrawText(14, 30, title, border, 1)

	line1, line2 := a.formatReadCaptureLines()
	c.DrawText(14, 50, truncate(line1, 46), ColCyan, 1)
	c.DrawText(14, 65, truncate(line2, 48), ColText, 1)

	if a.isRollingActive() {
		c.DrawText(14, 83, "Warning: Replay may desync rolling fob!", ColAmber, 1)
	} else {
		meta := fmt.Sprintf("%.2f MHz | %s", float64(a.ActiveFreq)/1e6, cc1101.GetPresetSpec(a.Preset).Name)
		c.DrawText(14, 83, meta, ColMuted, 1)
	}

	c.HLine(12, 100, 296, ColBorder)
	c.DrawTextCentered(ScreenW/2, 110, "[Enter/4] TEST SEND   [5] SAVE .SUB", ColGreen, 1)

	if a.ReadNoiseWarn {
		drawConfirmBanner(c, "Looks like noise! Save anyway? [Enter=YES]")
	} else if a.ReadRollWarn {
		drawConfirmBanner(c, "Rolling code! Replay anyway? [Enter=YES]")
	}
}

func (a *App) formatReadCaptureLines() (string, string) {
	if a.LastDecoded != nil {
		d := a.LastDecoded
		l1 := fmt.Sprintf("Protocol: %s (%d-bit, TE=%dus)", d.Protocol, d.Bits, d.TE)
		l2 := fmt.Sprintf("Key: %s", d.KeyHex)
		return l1, l2
	}
	if a.HasPrediction {
		lbl, conf := a.LastPrediction.Label, a.LastPrediction.Confidence
		if a.LastPrediction.ProtocolLabel != "" && !a.LastPrediction.IsNoise {
			lbl, conf = a.LastPrediction.ProtocolLabel, a.LastPrediction.ProtocolConfidence
		}
		return fmt.Sprintf("Detected: %s (%.0f%%)", lbl, conf*100), fmt.Sprintf("RAW capture: %d pulses", len(a.CapturedRaw))
	}
	return "RAW Sub-GHz Signal", fmt.Sprintf("%d pulses captured", len(a.CapturedRaw))
}
