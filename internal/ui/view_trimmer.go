package ui

import (
	"fmt"
	"path/filepath"
	"strings"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/subfile"
)

const (
	trimMinimapX = 8
	trimMinimapY = 38
	trimMinimapW = 304
	trimMinimapH = 54
)

// EnterTrimmerFromPulse opens the RAW Trimmer for the current CapturedRaw buffer.
func (a *App) EnterTrimmerFromPulse() {
	if len(a.CapturedRaw) < 4 {
		a.SetToast("Record RAW first (4)")
		return
	}
	name := fmt.Sprintf("raw_%.2fM.sub", float64(a.ActiveFreq)/1e6)
	a.openTrimmer(name, a.ActiveFreq, cc1101.GetPresetSpec(a.Preset).SubFileName, a.CapturedRaw, ModePulse)
}

// EnterTrimmerFromSaved opens the RAW Trimmer for the currently selected RAW .sub in SAVED.
func (a *App) EnterTrimmerFromSaved() {
	if len(a.SavedFiles) == 0 || a.SavedIdx >= len(a.SavedFiles) {
		a.SetToast("No saved file")
		return
	}
	entry := a.SavedFiles[a.SavedIdx]
	if entry.File == nil || len(entry.File.RawData) < 4 {
		a.SetToast("Selected file is not RAW")
		return
	}
	a.openTrimmer(entry.Name, entry.File.FreqHz, entry.File.Preset, entry.File.RawData, ModeSaved)
}

func (a *App) openTrimmer(name string, freq uint32, preset string, raw []int32, ret Mode) {
	a.ShowTrimmer = true
	a.SourceName = name
	a.SourceFreq = freq
	a.SourcePreset = preset
	a.RawSource = append([]int32(nil), raw...)
	b := subfile.SuggestTrimBounds(a.RawSource)
	a.StartIdx = b.Start
	a.EndIdx = b.End
	a.EditEnd = false
	a.Repeats = 1
	a.trimReturnMod = ret
}

// ExitTrimmer closes the RAW Trimmer and restores the originating tab without quitting.
func (a *App) ExitTrimmer() {
	if !a.ShowTrimmer {
		return
	}
	a.ShowTrimmer = false
	a.Mode = a.trimReturnMod
}

func (a *App) handleTrimmerKey(act KeyAction) {
	switch act {
	case KeyQuit, KeyNav8:
		a.ExitTrimmer()
	case KeyNav4:
		a.EditEnd = !a.EditEnd
	case KeyNav5:
		b := subfile.SuggestTrimBounds(a.RawSource)
		a.StartIdx, a.EndIdx = b.Start, b.End
		a.SetToast(fmt.Sprintf("Auto [%d..%d]", b.Start, b.End))
	case KeyLeft:
		a.stepTrimHandle(-1)
	case KeyRight:
		a.stepTrimHandle(1)
	case KeyUp:
		a.Repeats = min(10, a.Repeats+1)
	case KeyDown:
		a.Repeats = max(1, a.Repeats-1)
	case KeyNav6, KeyEnter:
		a.txTrimmedPreview()
	case KeyNav7:
		a.saveTrimmedRaw()
	}
}

func (a *App) stepTrimHandle(dir int) {
	n := len(a.RawSource)
	if n < 2 {
		return
	}
	step := max(1, n/40) * dir
	if !a.EditEnd {
		a.StartIdx = max(0, min(a.EndIdx-1, a.StartIdx+step))
		return
	}
	a.EndIdx = max(a.StartIdx+1, min(n, a.EndIdx+step))
}

func (a *App) txTrimmedPreview() {
	res, err := subfile.TrimAndRepeatRaw(a.RawSource, a.StartIdx, a.EndIdx, a.Repeats, 10000)
	if err != nil {
		a.SetToast("Trim err!")
		return
	}
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File",
		Version:  1,
		FreqHz:   a.SourceFreq,
		Preset:   a.SourcePreset,
		Protocol: "RAW",
		RawData:  res.Pulses,
	}
	ref := library.EntryRef{Name: "trim_preview.sub", File: sf}
	a.submitTX(ref, sf, -1, false)
}

func (a *App) saveTrimmedRaw() {
	if a.Store == nil {
		a.SetToast("No library store")
		return
	}
	res, err := subfile.TrimAndRepeatRaw(a.RawSource, a.StartIdx, a.EndIdx, a.Repeats, 10000)
	if err != nil {
		a.SetToast("Trim err!")
		return
	}
	base := strings.TrimSuffix(filepath.Base(a.SourceName), filepath.Ext(a.SourceName))
	if base == "" {
		base = "raw"
	}
	dstName := base + "_trim.sub"
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File",
		Version:  1,
		FreqHz:   a.SourceFreq,
		Preset:   a.SourcePreset,
		Protocol: "RAW",
		RawData:  res.Pulses,
	}
	saved, err := a.Store.SaveUserSubFileAtomic(dstName, sf)
	if err != nil {
		a.SetToast("Save err!")
		return
	}
	a.refreshSavedFiles()
	a.SetToast("Saved " + saved.Name)
}

func (a *App) drawTrimmer(c *Canvas) {
	res, ok := a.previewTrimResult()
	c.DrawText(8, 22, fmt.Sprintf("TRIM: %s", truncate(a.SourceName, 16)), ColCyan, 1)
	c.DrawText(160, 22, fmt.Sprintf("RPT:%dx", a.Repeats), ColGreen, 1)
	if ok {
		c.DrawText(218, 22, fmt.Sprintf("%dp/%.0fms", res.TrimmedCount, res.DurationMs), ColMuted, 1)
	}
	a.drawTrimmerMinimap(c)
	hdl := "[START]"
	if a.EditEnd {
		hdl = "[END]"
	}
	c.DrawText(8, 102, fmt.Sprintf("Handle:%s  Z/C:Move  F/X:Rpt(1..10x)", hdl), ColText, 1)
	c.DrawText(8, 116, fmt.Sprintf("Range: [%d..%d) of %d pulses  5:Auto", a.StartIdx, a.EndIdx, len(a.RawSource)), ColMuted, 1)
	c.DrawText(8, 129, "7:Save atomically to USER captures", ColGreen, 1)
}

func (a *App) previewTrimResult() (subfile.TrimResult, bool) {
	res, err := subfile.TrimAndRepeatRaw(a.RawSource, a.StartIdx, a.EndIdx, a.Repeats, 10000)
	return res, err == nil
}

func (a *App) drawTrimmerMinimap(c *Canvas) {
	c.FillRect(trimMinimapX, trimMinimapY, trimMinimapW, trimMinimapH, ColPanel)
	c.DrawRect(trimMinimapX, trimMinimapY, trimMinimapW, trimMinimapH, ColBorder)

	n := len(a.RawSource)
	if n == 0 {
		return
	}
	startX := trimMinimapX + (a.StartIdx*(trimMinimapW-2))/n + 1
	endX := trimMinimapX + (a.EndIdx*(trimMinimapW-2))/n + 1
	if endX > startX {
		c.FillRect(startX, trimMinimapY+1, endX-startX, trimMinimapH-2, ColPanelAlt)
	}
	const cols = 75
	for i := range cols {
		idx := (i * n) / cols
		p := a.RawSource[idx]
		if p < 0 {
			p = -p
		}
		h := min(trimMinimapH-8, max(3, int(p/120)))
		col := ColMuted
		if idx >= a.StartIdx && idx < a.EndIdx {
			col = ColGreen
		}
		bx := trimMinimapX + 2 + i*4
		c.FillRect(bx, trimMinimapY+trimMinimapH-3-h, 2, h, col)
	}
	c.FillRect(startX, trimMinimapY+1, 2, trimMinimapH-2, ColCyan)
	c.FillRect(max(startX+2, endX-2), trimMinimapY+1, 2, trimMinimapH-2, ColAmber)
}
