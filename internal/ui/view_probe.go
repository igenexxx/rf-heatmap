package ui

import (
	"fmt"
	"path/filepath"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/config"
	"rf-heatmap/internal/dbsync"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

func (a *App) refreshProbePacks() {
	if a.Store == nil || a.Probe == nil {
		return
	}
	prevPackID := ""
	if a.PackIdx >= 0 && a.PackIdx < len(a.Packs) {
		prevPackID = a.Packs[a.PackIdx].ID
	}
	a.Packs = a.Store.ListPacks()
	if len(a.Packs) == 0 {
		a.PackIdx = 0
		a.Probe.SetCatalog(library.PackInfo{}, nil)
		return
	}
	a.PackIdx = 0
	for i, p := range a.Packs {
		if p.ID == prevPackID {
			a.PackIdx = i
			break
		}
	}
	a.loadSelectedProbePack()
}

func (a *App) loadSelectedProbePack() {
	if a.PackIdx < 0 || a.PackIdx >= len(a.Packs) {
		return
	}
	pack := a.Packs[a.PackIdx]
	cat, resolvedPack, err := a.Store.OpenCatalog(pack.ID)
	if err != nil {
		a.Probe.SetCatalog(pack, nil)
		return
	}
	if gen, ok := cat.(*library.GeneratorCatalog); ok {
		a.ActiveFreq = gen.FreqHz
		gen.SetPreset(cc1101.GetPresetSpec(a.Preset).SubFileName)
	}
	a.Probe.SetCatalogSource(resolvedPack, cat)
	clear(a.ProbeSelected)
	a.probeDelUntil = time.Time{}
}

func (a *App) activeProbeGenerator() (*library.GeneratorCatalog, bool) {
	if a.Probe == nil || a.Probe.Catalog == nil {
		return nil, false
	}
	gen, ok := a.Probe.Catalog.(*library.GeneratorCatalog)
	return gen, ok
}

func (a *App) cycleProbeGenFreq() bool {
	gen, ok := a.activeProbeGenerator()
	if !ok {
		return false
	}
	freqs := library.GeneratorFreqPresets
	nextIdx := 0
	for i, f := range freqs {
		if f == gen.FreqHz {
			nextIdx = (i + 1) % len(freqs)
			break
		}
	}
	gen.SetFrequency(freqs[nextIdx])
	a.ActiveFreq = gen.FreqHz
	a.SetToast(fmt.Sprintf("Gen Freq: %.2fM", float64(gen.FreqHz)/1e6))
	return true
}

func (a *App) syncProbeGenPreset() {
	if gen, ok := a.activeProbeGenerator(); ok {
		gen.SetPreset(cc1101.GetPresetSpec(a.Preset).SubFileName)
	}
}

func (a *App) stopActiveProbe() {
	if a.Probe != nil && a.Probe.Status == library.ProbeRunning {
		a.Probe.Pause()
	}
	if a.TX != nil {
		a.TX.Cancel()
	}
	a.TXBusy = false
}

func (a *App) submitTX(ref library.EntryRef, sf *subfile.SubFile, idx int, isProbe bool) {
	if a.TX == nil {
		return
	}
	// Release GDO0 RX capture before keying GDO0 TX.
	a.stopRXCapture()
	req := library.TXRequest{Entry: ref, SubFile: sf, Index: idx, IsProbe: isProbe}
	if !a.TX.Submit(req) {
		a.SetToast("TX busy")
		return
	}
	a.TXBusy = true
	a.TXLabel = ref.Name
	if isProbe && a.Probe != nil {
		a.Probe.OnTXStarted(idx)
	} else {
		a.SetToast("TX: " + ref.Name)
	}
}

func (a *App) pollTXResults() {
	if a.TX == nil {
		return
	}
	for {
		res, ok := a.TX.PollResult()
		if !ok {
			return
		}
		a.TXBusy = false
		if res.IsProbe && a.Probe != nil {
			a.handleProbeTXResult(res)
			continue
		}
		if res.Err != nil && !res.Canceled {
			a.SetToast("TX err: " + truncate(res.Entry.Name, 14))
		}
	}
}

func (a *App) handleProbeTXResult(res library.TXResult) {
	if res.Canceled {
		a.Probe.Pause()
		return
	}
	if res.Err != nil {
		a.SetToast("Skip: " + truncate(res.Entry.Name, 16))
	}
	a.Probe.OnTXDone(res.Index, time.Now())
	if a.Probe.Status == library.ProbeDone {
		a.SetToast("Probe finished")
	}
}

func (a *App) stepProbe() {
	if a.Probe == nil || a.TXBusy {
		return
	}
	ref, idx, due := a.Probe.PollDue(time.Now())
	if !due {
		return
	}
	if sf, err := library.LoadSubFile(ref); err == nil {
		a.ActiveFreq = sf.FreqHz
		a.submitTX(ref, sf, idx, true)
		return
	}
	// Trigger submitTX with nil SubFile so worker reports clean error and advances.
	a.submitTX(ref, nil, idx, true)
}

func (a *App) handleProbeKey(act KeyAction) {
	if a.Probe == nil {
		return
	}
	if a.Probe.Status == library.ProbeRunning {
		a.handleProbeRunningKey(act)
		return
	}
	a.handleProbeIdleKey(act)
}

func (a *App) handleProbeRunningKey(act KeyAction) {
	switch act {
	case KeyNav4, KeySpace, KeyEnter:
		a.stopActiveProbe()
		a.SetToast(fmt.Sprintf("Stopped at #%d (F=Up)", a.Probe.LastSentIdx+1))
	case KeyLeft, KeyNav5:
		a.adjustProbeInterval(-1)
	case KeyRight, KeyNav6:
		a.adjustProbeInterval(1)
	case KeyNav7:
		a.stopActiveProbe()
		a.cycleProbePack()
	}
}

func (a *App) handleProbeIdleKey(act KeyAction) {
	switch act {
	case KeyUp:
		a.Probe.MoveCursor(-1)
	case KeyDown:
		a.Probe.MoveCursor(1)
	case KeyLeft:
		a.adjustProbeInterval(-1)
	case KeyRight:
		a.adjustProbeInterval(1)
	case KeyNav4, KeySpace:
		if a.Probe.Start(time.Now()) {
			a.SetToast("Auto-Probe " + a.Probe.IntervalLabel())
		} else {
			a.SetToast("Pack is empty")
		}
	case KeyNav5, KeyEnter:
		a.tryCurrentProbeEntry()
	case KeyNav6:
		a.promoteCurrentProbeEntry()
	case KeyNav7:
		a.cycleProbePack()
	case KeyMark:
		a.toggleProbeMark()
	case KeyDelete:
		a.handleProbeDelete()
	case KeyRank:
		a.rankActiveProbePack()
	}
}

func (a *App) rankActiveProbePack() {
	if a.Probe == nil || a.Probe.Pack.Source == library.SourceGenerated {
		a.SetToast("Rank: DB/USER only")
		return
	}
	sc, ok := a.Probe.Catalog.(library.SliceCatalog)
	if !ok || len(sc) < 2 {
		a.SetToast("Need >=2 signals")
		return
	}
	presetName := cc1101.GetPresetSpec(a.Preset).SubFileName
	var query rfml.Embedding
	switch {
	case len(a.CapturedRaw) > 0:
		query = rfml.ComputeEmbeddingFromRaw(a.CapturedRaw, a.ActiveFreq, presetName)
	case a.LastDecoded != nil:
		query = rfml.ComputeEmbeddingFromKey(a.ActiveFreq, presetName, a.LastDecoded.Protocol, a.LastDecoded.Bits, a.LastDecoded.TE)
	default:
		a.SetToast("Capture a signal first")
		return
	}
	ranked, best := library.RankEntriesByEmbedding([]library.EntryRef(sc), query, a.ActiveFreq)
	a.Probe.SetCatalog(a.Probe.Pack, ranked)
	if best <= 0 {
		a.SetToast("No in-band match")
		return
	}
	a.SetToast(fmt.Sprintf("Ranked: top %.0f%%", best*100))
}

func (a *App) canDeleteInActiveProbePack() bool {
	if a.Probe == nil {
		return false
	}
	p := a.Probe.Pack
	return p.Source == library.SourceUser || (p.Source == library.SourceExternal && p.ID != library.PackIDSubg)
}

func (a *App) toggleProbeMark() {
	if !a.canDeleteInActiveProbePack() {
		a.SetToast("Read-only pack")
		return
	}
	ref, ok := a.Probe.CurrentEntry()
	if !ok {
		return
	}
	if a.ProbeSelected == nil {
		a.ProbeSelected = make(map[string]bool)
	}
	if a.ProbeSelected[ref.Name] {
		delete(a.ProbeSelected, ref.Name)
	} else {
		a.ProbeSelected[ref.Name] = true
	}
	a.probeDelUntil = time.Time{}
}

func (a *App) handleProbeDelete() {
	if !a.canDeleteInActiveProbePack() || a.Store == nil {
		a.SetToast("Read-only pack")
		return
	}
	targets := a.selectedProbeEntries()
	if len(targets) == 0 {
		return
	}
	if time.Now().After(a.probeDelUntil) {
		a.probeDelUntil = time.Now().Add(3 * time.Second)
		a.SetToast(fmt.Sprintf("Del %d file(s)? DEL again", len(targets)))
		return
	}
	a.probeDelUntil = time.Time{}
	removed, ignoredNames, packDir := a.executeProbeDeletes(targets)
	if len(ignoredNames) > 0 && packDir != "" {
		_ = dbsync.MarkIgnored(packDir, ignoredNames)
	}
	clear(a.ProbeSelected)
	a.refreshSavedFiles()
	a.refreshProbePacks()
	a.SetToast(fmt.Sprintf("Deleted %d file(s)", removed))
}

func (a *App) selectedProbeEntries() []library.EntryRef {
	if len(a.ProbeSelected) == 0 {
		if ref, ok := a.Probe.CurrentEntry(); ok {
			return []library.EntryRef{ref}
		}
		return nil
	}
	var out []library.EntryRef
	for i := range a.Probe.EntryCount() {
		if ref, ok := a.Probe.EntryAt(i); ok && a.ProbeSelected[ref.Name] {
			out = append(out, ref)
		}
	}
	return out
}

func (a *App) executeProbeDeletes(targets []library.EntryRef) (int, []string, string) {
	removed := 0
	var ignored []string
	var packDir string
	for _, ref := range targets {
		var err error
		if ref.Source == library.SourceUser {
			err = a.Store.DeleteUserEntry(ref)
		} else {
			err = a.Store.DeleteDatabaseEntry(ref)
			if err == nil {
				ignored = append(ignored, ref.Name)
				packDir = filepath.Dir(ref.Path)
			}
		}
		if err == nil {
			removed++
		}
	}
	return removed, ignored, packDir
}

func (a *App) tryCurrentProbeEntry() {
	ref, ok := a.Probe.CurrentEntry()
	if !ok {
		a.SetToast("No signal selected")
		return
	}
	sf, err := library.LoadSubFile(ref)
	if err != nil {
		a.SetToast("Bad .sub: " + truncate(ref.Name, 14))
		return
	}
	a.ActiveFreq = sf.FreqHz
	a.Probe.OnTXStarted(a.Probe.CursorIdx)
	a.submitTX(ref, sf, a.Probe.CursorIdx, false)
	a.Probe.ActiveTX = false
}

func (a *App) promoteCurrentProbeEntry() {
	ref, ok := a.Probe.CurrentEntry()
	if !ok {
		a.SetToast("No signal selected")
		return
	}
	if !ref.ReadOnly && ref.Source == library.SourceUser {
		a.SetToast("Already in USER")
		return
	}
	promoted, err := a.Store.PromoteToUser(ref)
	if err != nil {
		a.SetToast("Save err!")
		return
	}
	a.refreshSavedFiles()
	a.SetToast("Saved: " + truncate(promoted.Name, 18))
}

func (a *App) cycleProbePack() {
	if len(a.Packs) == 0 {
		a.refreshProbePacks()
		return
	}
	a.PackIdx = (a.PackIdx + 1) % len(a.Packs)
	a.loadSelectedProbePack()
	pack := a.Packs[a.PackIdx]
	a.SetToast(fmt.Sprintf("Pack: %s (%d)", pack.Name, a.Probe.EntryCount()))
}

func (a *App) adjustProbeInterval(delta int) {
	dur := a.Probe.StepInterval(delta)
	a.Settings.ProbeIntervalMs = int(dur.Milliseconds())
	if a.ConfigPath != "" {
		_ = config.Save(a.ConfigPath, a.Settings)
	}
	a.SetToast("Interval: " + a.Probe.IntervalLabel())
}

func (a *App) probeNavLabels() [5]string {
	if a.Probe != nil && a.Probe.Status == library.ProbeRunning {
		return [5]string{"STOP", "INT-", "INT+", "PACK", "MODE"}
	}
	startLbl := "AUTO"
	if a.Probe != nil && a.Probe.Status == library.ProbePaused {
		startLbl = "CONT"
	}
	return [5]string{startLbl, "TRY", "SAVE", "PACK", "MODE"}
}

func (a *App) drawProbe(c *Canvas) {
	a.drawProbeHeaderBar(c)
	c.FillRect(6, 34, 308, 106, ColPanel)
	c.DrawRect(6, 34, 308, 106, ColBorder)

	if a.Probe == nil || a.Probe.EntryCount() == 0 {
		c.DrawTextCentered(ScreenW/2, 74, "No .sub files in this pack", ColMuted, 1)
		c.DrawTextCentered(ScreenW/2, 88, "Add packs in ~/rf-heatmap/databases/", ColMuted, 1)
		c.DrawTextCentered(ScreenW/2, 102, "Press 7(PACK) for GEN or USER", ColAmber, 1)
		return
	}
	a.drawProbeRows(c)
}

func (a *App) drawProbeHeaderBar(c *Canvas) {
	packLabel := "USER"
	packCol := ColGreen
	if a.Probe != nil {
		switch a.Probe.Pack.Source {
		case library.SourceGenerated:
			packLabel = "GEN:" + truncate(a.Probe.Pack.Name, 9)
			packCol = ColMagenta
		case library.SourceExternal:
			packLabel = "DB:" + truncate(a.Probe.Pack.Name, 10)
			packCol = ColCyan
		}
	}
	total := 0
	cur := 0
	if a.Probe != nil {
		total = a.Probe.EntryCount()
		if total > 0 {
			cur = a.Probe.CursorIdx + 1
		}
	}
	c.DrawText(6, 22, fmt.Sprintf("%s [%d/%d]", packLabel, cur, total), packCol, 1)
	if a.Probe != nil {
		c.DrawText(176, 22, "INT:"+a.Probe.IntervalLabel(), ColAmber, 1)
		stLabel, stCol := probeStatusStyle(a.Probe.Status, a.TXBusy)
		c.FillRect(266, 20, 48, 11, ColPanelAlt)
		c.DrawRect(266, 20, 48, 11, stCol)
		c.DrawTextCentered(266+24, 22, stLabel, stCol, 1)
	}
}

func probeStatusStyle(st library.ProbeStatus, txBusy bool) (string, uint16) {
	if txBusy {
		return "TX", ColMagenta
	}
	switch st {
	case library.ProbeRunning:
		return "RUN", ColGreen
	case library.ProbePaused:
		return "PAUSE", ColAmber
	case library.ProbeDone:
		return "DONE", ColCyan
	default:
		return "IDLE", ColMuted
	}
}

func (a *App) drawProbeRows(c *Canvas) {
	total := a.Probe.EntryCount()
	start := max(0, a.Probe.CursorIdx-3)
	visible := min(6, total-start)
	wideIdx := total >= 1000
	for row := range visible {
		idx := start + row
		entry, ok := a.Probe.EntryAt(idx)
		if !ok {
			continue
		}
		y := 38 + row*17
		if idx == a.Probe.CursorIdx {
			c.FillRect(8, y-1, 304, 15, ColNavActive)
			c.DrawRect(8, y-1, 304, 15, ColCyan)
		}
		glyph, gCol := a.probeRowGlyph(idx)
		if a.ProbeSelected[entry.Name] {
			glyph, gCol = "x", ColAmber
		}
		c.DrawText(11, y+3, glyph, gCol, 1)

		tag := "RW"
		switch entry.Source {
		case library.SourceGenerated:
			tag = "GEN"
		case library.SourceExternal:
			tag = "RO"
		}
		var label string
		if wideIdx {
			label = fmt.Sprintf("#%04d %-33s %s", idx+1, truncate(entry.Name, 33), tag)
		} else {
			label = fmt.Sprintf("#%02d %-36s %s", idx+1, truncate(entry.Name, 36), tag)
		}
		c.DrawText(20, y+3, label, ColText, 1)
	}
}

func (a *App) probeRowGlyph(idx int) (string, uint16) {
	if idx == a.Probe.LastSentIdx {
		if a.TXBusy {
			return ">", ColMagenta
		}
		return ">", ColGreen
	}
	if a.Probe.IsRecentSent(idx) {
		return "*", ColAmber
	}
	return " ", ColMuted
}
