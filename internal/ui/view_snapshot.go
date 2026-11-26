package ui

import (
	"fmt"
	"math"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

var snapshotZoomLevels = [4]int{1, 2, 4, 8}

const (
	snapBoxX     = 6
	snapBoxW     = 308
	snapSplitWY  = 30
	snapSplitWH  = 50
	snapSplitHY  = 83
	snapSplitHH  = 57
	snapFullY    = 30
	snapFullH    = 110
	snapPanStep  = 10
	snapWavePadX = 4
)

// EnterSnapshotFromRead opens the Signal Snapshot sub-view for the captured signal in Read & Replay.
func (a *App) EnterSnapshotFromRead() {
	raw, proto := a.resolveActiveCapturePulses()
	if len(raw) == 0 {
		a.SetToast("No signal captured yet")
		return
	}
	name := fmt.Sprintf("cap_%.2fM.sub", float64(a.ActiveFreq)/1e6)
	a.openSnapshot(name, proto, a.ActiveFreq, cc1101.GetPresetSpec(a.Preset).SubFileName, raw, ModeRead)
}

// EnterSnapshotFromPulse opens the Signal Snapshot sub-view for the live Pulse Scope capture buffer.
func (a *App) EnterSnapshotFromPulse() {
	raw, proto := a.resolveActiveCapturePulses()
	if len(raw) == 0 {
		a.SetToast("Record RAW first (4)")
		return
	}
	name := fmt.Sprintf("pulse_%.2fM.sub", float64(a.ActiveFreq)/1e6)
	a.openSnapshot(name, proto, a.ActiveFreq, cc1101.GetPresetSpec(a.Preset).SubFileName, raw, ModePulse)
}

// EnterSnapshotFromSaved opens the Signal Snapshot sub-view for the selected .sub file (RAW or Key).
func (a *App) EnterSnapshotFromSaved() {
	if len(a.SavedFiles) == 0 || a.SavedIdx >= len(a.SavedFiles) {
		a.SetToast("No saved file")
		return
	}
	entry := a.SavedFiles[a.SavedIdx]
	if entry.File == nil {
		a.SetToast("Empty .sub entry")
		return
	}
	raw, err := subfile.EncodePulses(entry.File)
	if err != nil || len(raw) == 0 {
		a.SetToast("No pulse data in .sub")
		return
	}
	proto := entry.File.Protocol
	if proto == "" {
		proto = "RAW"
	}
	a.openSnapshot(entry.Name, proto, entry.File.FreqHz, entry.File.Preset, raw, ModeSaved)
}

// EnterSnapshotFromTrimmer opens the Signal Snapshot sub-view for the current Trimmer slice.
func (a *App) EnterSnapshotFromTrimmer() {
	if len(a.RawSource) == 0 {
		a.SetToast("No RAW source")
		return
	}
	res, err := subfile.TrimAndRepeatRaw(a.RawSource, a.StartIdx, a.EndIdx, a.Repeats, 10000)
	if err != nil || len(res.Pulses) == 0 {
		a.SetToast("Trim slice empty")
		return
	}
	a.ShowTrimmer = false
	a.openSnapshot(a.SourceName, "TRIM", a.SourceFreq, a.SourcePreset, res.Pulses, a.trimReturnMod)
}

func (a *App) resolveActiveCapturePulses() ([]int32, string) {
	if len(a.CapturedRaw) > 0 {
		proto := "RAW"
		if a.LastDecoded != nil {
			proto = fmt.Sprintf("%s %db", a.LastDecoded.Protocol, a.LastDecoded.Bits)
		} else if a.HasPrediction && a.LastPrediction.ProtocolLabel != "" && !a.LastPrediction.IsNoise {
			proto = a.LastPrediction.ProtocolLabel
		}
		return a.CapturedRaw, proto
	}
	if a.LastDecoded != nil {
		sf := &subfile.SubFile{
			Protocol: a.LastDecoded.Protocol,
			Bit:      a.LastDecoded.Bits,
			Key:      a.LastDecoded.KeyHex,
			TE:       a.LastDecoded.TE,
		}
		if pulses, err := subfile.EncodePulses(sf); err == nil && len(pulses) > 0 {
			return pulses, fmt.Sprintf("%s %db", a.LastDecoded.Protocol, a.LastDecoded.Bits)
		}
	}
	return nil, ""
}

func (a *App) openSnapshot(name, proto string, freq uint32, preset string, raw []int32, ret Mode) {
	a.ShowSnapshot = true
	a.SnapName = name
	a.SnapProtocol = proto
	a.SnapFreqHz = freq
	a.SnapPreset = preset
	a.SnapRaw = append([]int32(nil), raw...)
	a.SnapData = rfml.BuildSignalSnapshot(a.SnapRaw, freq, preset, rfml.DefaultSnapshotHistBins)
	a.SnapLayout = SnapshotLayoutSplit
	a.SnapZoomIdx = 0
	a.SnapPanPct = 0
	a.snapReturnMode = ret
}

// ExitSnapshot closes the Signal Snapshot sub-view and restores the originating tab.
func (a *App) ExitSnapshot() {
	if !a.ShowSnapshot {
		return
	}
	a.ShowSnapshot = false
	a.Mode = a.snapReturnMode
}

func (a *App) snapshotNavLabels() [5]string {
	viewLbl := "SPLIT"
	switch a.SnapLayout {
	case SnapshotLayoutWave:
		viewLbl = "WAVE"
	case SnapshotLayoutHist:
		viewLbl = "HIST"
	}
	zoomLbl := fmt.Sprintf("Z %dx", snapshotZoomLevels[a.SnapZoomIdx%len(snapshotZoomLevels)])
	return [5]string{viewLbl, zoomLbl, "TX", "TRIM", "BACK"}
}

func (a *App) handleSnapshotKey(act KeyAction) {
	switch act {
	case KeyQuit, KeyBack, KeyNav8:
		a.ExitSnapshot()
	case KeyNav4, KeySpace:
		a.SnapLayout = (a.SnapLayout + 1) % snapshotLayoutCount
	case KeyNav5:
		a.SnapZoomIdx = (a.SnapZoomIdx + 1) % len(snapshotZoomLevels)
	case KeyUp:
		a.SnapZoomIdx = min(len(snapshotZoomLevels)-1, a.SnapZoomIdx+1)
	case KeyDown:
		a.SnapZoomIdx = max(0, a.SnapZoomIdx-1)
	case KeyLeft:
		a.SnapPanPct = max(0, a.SnapPanPct-snapPanStep)
	case KeyRight:
		a.SnapPanPct = min(100, a.SnapPanPct+snapPanStep)
	case KeyNav6, KeyEnter:
		a.txSnapshotSignal()
	case KeyNav7, KeyTrim:
		if len(a.SnapRaw) >= 4 {
			ret := a.snapReturnMode
			a.ShowSnapshot = false
			a.openTrimmer(a.SnapName, a.SnapFreqHz, a.SnapPreset, a.SnapRaw, ret)
		}
	}
}

func (a *App) txSnapshotSignal() {
	if len(a.SnapRaw) == 0 {
		a.SetToast("No pulses to TX")
		return
	}
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File",
		Version:  1,
		FreqHz:   a.SnapFreqHz,
		Preset:   a.SnapPreset,
		Protocol: "RAW",
		RawData:  a.SnapRaw,
	}
	ref := library.EntryRef{Name: a.SnapName, File: sf}
	a.submitTX(ref, sf, -1, false)
}

func (a *App) drawSnapshot(c *Canvas) {
	a.drawSnapshotSummaryStrip(c)
	switch a.SnapLayout {
	case SnapshotLayoutWave:
		a.drawSnapshotWaveform(c, snapBoxX, snapFullY, snapBoxW, snapFullH, true)
	case SnapshotLayoutHist:
		a.drawSnapshotHistogram(c, snapBoxX, snapFullY, snapBoxW, snapFullH, true)
	default:
		a.drawSnapshotWaveform(c, snapBoxX, snapSplitWY, snapBoxW, snapSplitWH, false)
		a.drawSnapshotHistogram(c, snapBoxX, snapSplitHY, snapBoxW, snapSplitHH, false)
	}
}

func (a *App) drawSnapshotSummaryStrip(c *Canvas) {
	s := a.SnapData
	left := truncate(a.SnapProtocol, 14)
	if left == "" {
		left = "RAW"
	}
	c.DrawText(6, 20, fmt.Sprintf("%s %dp", left, s.TotalPulses), ColCyan, 1)
	if s.Clusters.Valid {
		mid := fmt.Sprintf("S:%.0f L:%.0fus", s.Clusters.ShortUs, s.Clusters.LongUs)
		c.DrawText(122, 20, mid, ColAmber, 1)
	} else {
		c.DrawText(122, 20, fmt.Sprintf("%.1fms", s.TotalDurationMs), ColMuted, 1)
	}
	c.DrawText(252, 20, fmt.Sprintf("DC:%2.0f%%", s.DutyCyclePct), ColGreen, 1)
}

func (a *App) drawSnapshotWaveform(c *Canvas, x, y, w, h int, detailed bool) {
	c.FillRect(x, y, w, h, ColPanel)
	c.DrawRect(x, y, w, h, ColBorder)

	s := a.SnapData
	zoom := snapshotZoomLevels[a.SnapZoomIdx%len(snapshotZoomLevels)]
	winStart, winSpan := a.snapshotWindowBounds(zoom)
	segs := s.SliceWaveformWindow(winStart, winSpan)

	hdr := fmt.Sprintf("WAVE %dx  %.1f-%.1fms", zoom, float64(winStart)/1000.0, float64(winStart+winSpan)/1000.0)
	c.DrawText(x+4, y+3, hdr, ColText, 1)
	c.DrawText(x+w-56, y+3, "HI", ColGreen, 1)
	c.DrawText(x+w-40, y+3, "LO", ColCyan, 1)
	c.DrawText(x+w-24, y+3, "SYN", ColAmber, 1)

	plotX := x + snapWavePadX
	plotW := w - snapWavePadX*2
	highY := y + 15
	lowY := y + h - 7
	if detailed {
		lowY = y + h - 20
		c.DrawText(x+4, y+h-11, fmt.Sprintf("Z/C:Pan(%d%%) F/X:Zoom  Total:%.1fms  Frames:%d", a.SnapPanPct, s.TotalDurationMs, s.FramesEstimated), ColMuted, 1)
	}
	if lowY <= highY+4 || len(segs) == 0 || winSpan <= 0 {
		return
	}

	// Subtle baseline guides for HIGH and LOW logic levels.
	for gx := plotX; gx < plotX+plotW; gx += 6 {
		c.HLine(gx, highY, 2, ColBorder)
		c.HLine(gx, lowY, 2, ColBorder)
	}
	drawWaveformTrace(c, segs, winStart, winSpan, plotX, plotW, highY, lowY)
}

func (a *App) snapshotWindowBounds(zoom int) (int64, int64) {
	s := a.SnapData
	baseWin := s.RecommendedWindowUs()
	span := max(int64(400), baseWin/int64(max(1, zoom)))
	if s.TotalDurationUs <= span {
		return 0, max(int64(400), s.TotalDurationUs)
	}
	maxStart := s.TotalDurationUs - span
	start := (maxStart * int64(a.SnapPanPct)) / 100
	return start, span
}

func drawWaveformTrace(c *Canvas, segs []rfml.WaveformSegment, winStart, winSpan int64, plotX, plotW, highY, lowY int) {
	prevY := -1
	for _, seg := range segs {
		s0 := max(winStart, seg.StartUs)
		s1 := min(winStart+winSpan, seg.StartUs+int64(seg.DurationUs))
		if s1 <= s0 {
			continue
		}
		x0 := plotX + int(((s0-winStart)*int64(plotW))/winSpan)
		x1 := plotX + int(((s1-winStart)*int64(plotW))/winSpan)
		if x1 <= x0 {
			x1 = x0 + 1
		}
		x1 = min(plotX+plotW, x1)

		lvlY := lowY
		col := ColCyan
		if seg.High {
			lvlY = highY
			col = ColGreen
			c.FillRect(x0, highY+1, max(1, x1-x0), max(1, lowY-highY-1), ColPanelAlt)
		}
		if seg.IsSync {
			col = ColAmber
			c.FillRect(x0, lowY-3, max(1, x1-x0), 4, ColAmber)
		}
		if prevY >= 0 && prevY != lvlY {
			c.VLine(x0, min(prevY, lvlY), absInt(lvlY-prevY)+1, ColCyan)
		}
		c.HLine(x0, lvlY, max(1, x1-x0), col)
		prevY = lvlY
	}
}

func (a *App) drawSnapshotHistogram(c *Canvas, x, y, w, h int, detailed bool) {
	c.FillRect(x, y, w, h, ColPanel)
	c.DrawRect(x, y, w, h, ColBorder)

	s := a.SnapData
	c.DrawText(x+4, y+3, "HISTOGRAM (us)", ColText, 1)
	rightHdr := fmt.Sprintf("SYN:%d", s.SyncGaps)
	if s.Clusters.Valid {
		rightHdr = fmt.Sprintf("R:%.1fx J:%.0f%%", s.Clusters.LongShortRatio, s.Clusters.JitterRatio*100)
	}
	c.DrawText(x+w-len(rightHdr)*6-4, y+3, rightHdr, ColMuted, 1)

	if len(s.Histogram) == 0 || s.HistPeakCount == 0 {
		c.DrawTextCentered(x+w/2, y+h/2-4, "No timing data", ColMuted, 1)
		return
	}

	plotX := x + 6
	plotW := w - 12
	plotTop := y + 14
	footerH := 12
	if detailed {
		footerH = 26
	}
	plotBot := y + h - footerH
	plotH := plotBot - plotTop
	if plotH < 8 {
		return
	}

	c.HLine(plotX, plotBot, plotW, ColBorder)
	drawHistogramBars(c, s, plotX, plotW, plotBot, plotH)
	drawHistogramCentroids(c, s, plotX, plotW, plotTop, plotBot)
	drawHistogramAxis(c, s, x, y, w, h, plotBot, detailed)
}

func drawHistogramBars(c *Canvas, s rfml.SignalSnapshot, plotX, plotW, plotBot, plotH int) {
	n := len(s.Histogram)
	slotW := max(2, plotW/n)
	barW := max(1, slotW-2)
	for i, bin := range s.Histogram {
		if bin.TotalCount == 0 {
			continue
		}
		bx := plotX + (i*plotW)/n + 1
		totH := max(2, int(math.Round((float64(bin.TotalCount)/float64(s.HistPeakCount))*float64(plotH-2))))
		hiH := int(math.Round((float64(bin.HighCount) / float64(bin.TotalCount)) * float64(totH)))
		loH := totH - hiH
		if loH > 0 {
			c.FillRect(bx, plotBot-loH, barW, loH, ColCyan)
		}
		if hiH > 0 {
			c.FillRect(bx, plotBot-totH, barW, hiH, ColGreen)
		}
	}
}

func drawHistogramCentroids(c *Canvas, s rfml.SignalSnapshot, plotX, plotW, plotTop, plotBot int) {
	n := len(s.Histogram)
	if n == 0 {
		return
	}
	drawMarker := func(idx int, label string, col uint16) {
		if idx < 0 || idx >= n {
			return
		}
		mx := plotX + (idx*plotW)/n + max(1, plotW/(2*n))
		for py := plotTop; py < plotBot; py += 4 {
			c.VLine(mx, py, 2, col)
		}
		c.DrawText(min(plotX+plotW-6, mx+2), plotTop, label, col, 1)
	}
	drawMarker(s.ShortBinIdx, "S", ColAmber)
	if s.LongBinIdx != s.ShortBinIdx {
		drawMarker(s.LongBinIdx, "L", ColMagenta)
	}
}

func drawHistogramAxis(c *Canvas, s rfml.SignalSnapshot, x, y, w, h, plotBot int, detailed bool) {
	axisY := plotBot + 2
	c.DrawText(x+6, axisY, fmt.Sprintf("%dus", s.HistMinUs), ColMuted, 1)
	midTxt := fmt.Sprintf("med:%dus", s.MedianDataUs)
	if s.Clusters.Valid {
		midTxt = fmt.Sprintf("S:%.0f L:%.0f", s.Clusters.ShortUs, s.Clusters.LongUs)
	}
	c.DrawTextCentered(x+w/2, axisY, midTxt, ColText, 1)
	maxTxt := fmt.Sprintf("%dus", s.HistMaxUs)
	c.DrawText(x+w-len(maxTxt)*6-6, axisY, maxTxt, ColMuted, 1)

	if detailed {
		meta := fmt.Sprintf("Mod:%s  Glitch:%d  Sync:%d (%.1fms)",
			truncate(s.Clusters.ModulationHint, 18), s.Clusters.GlitchesRemoved, s.SyncGaps, float64(s.MaxSyncUs)/1000.0)
		c.DrawText(x+6, y+h-11, truncate(meta, 49), ColCyan, 1)
	}
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
