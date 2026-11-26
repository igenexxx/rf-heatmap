package ui

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

func (a *App) handleSavedKey(act KeyAction) {
	switch act {
	case KeyUp:
		if a.SavedIdx > 0 {
			a.SavedIdx--
		}
	case KeyDown:
		if a.SavedIdx < len(a.SavedFiles)-1 {
			a.SavedIdx++
		}
	case KeyNav4, KeyEnter:
		if len(a.SavedFiles) > 0 {
			entry := a.SavedFiles[a.SavedIdx]
			a.ActiveFreq = entry.File.FreqHz
			ref := library.EntryRef{
				Name:   entry.Name,
				Path:   entry.Path,
				PackID: library.PackIDUser,
				Source: library.SourceUser,
				File:   entry.File,
			}
			a.submitTX(ref, entry.File, -1, false)
		}
	case KeyNav5:
		if !a.Settings.ExpertMode {
			a.EnterTrimmerFromSaved()
			return
		}
		if len(a.SavedFiles) > 0 {
			entry := a.SavedFiles[a.SavedIdx]
			a.ActiveFreq = entry.File.FreqHz
			if a.LiveBaseline != nil {
				a.LiveBaseline.Reset()
			}
			a.SwitchMode(ModePulse)
			a.SetToast("Loaded " + entry.Name)
		}
	case KeySpace, KeyMark:
		if a.Settings.ExpertMode {
			a.toggleSavedMark()
		}
	case KeyNav6:
		if !a.Settings.ExpertMode {
			a.enterReadWizard()
			return
		}
		a.handleSavedDelete()
	case KeyDelete:
		a.handleSavedDelete()
	case KeyNav7:
		if !a.Settings.ExpertMode {
			a.handleSavedDelete()
			return
		}
		a.handlePurgeNoiseKey()
	case KeyTrim:
		a.EnterTrimmerFromSaved()
	case KeyInspect:
		a.EnterSnapshotFromSaved()
	}
}

func (a *App) toggleSavedMark() {
	if len(a.SavedFiles) == 0 {
		return
	}
	if a.SavedSelected == nil {
		a.SavedSelected = make(map[string]bool)
	}
	name := a.SavedFiles[a.SavedIdx].Name
	if a.SavedSelected[name] {
		delete(a.SavedSelected, name)
	} else {
		a.SavedSelected[name] = true
	}
	a.savedDelUntil = time.Time{}
}

func (a *App) handleSavedDelete() {
	if len(a.SavedFiles) == 0 || a.Store == nil {
		return
	}
	targets := a.selectedSavedEntries()
	if len(targets) > 1 && time.Now().After(a.savedDelUntil) {
		a.savedDelUntil = time.Now().Add(3 * time.Second)
		a.SetToast(fmt.Sprintf("Del %d files? DEL again", len(targets)))
		return
	}
	a.savedDelUntil = time.Time{}
	removed := 0
	for _, entry := range targets {
		ref := library.EntryRef{Name: entry.Name, Path: entry.Path, PackID: library.PackIDUser, Source: library.SourceUser}
		if a.Store.DeleteUserEntry(ref) == nil {
			removed++
			delete(a.SavedSelected, entry.Name)
		}
	}
	a.refreshSavedFiles()
	if len(targets) == 1 && removed == 1 {
		a.SetToast("Deleted " + targets[0].Name)
		return
	}
	a.SetToast(fmt.Sprintf("Deleted %d file(s)", removed))
}

func (a *App) selectedSavedEntries() []SavedEntry {
	if len(a.SavedSelected) == 0 {
		return []SavedEntry{a.SavedFiles[a.SavedIdx]}
	}
	var out []SavedEntry
	for _, e := range a.SavedFiles {
		if a.SavedSelected[e.Name] {
			out = append(out, e)
		}
	}
	if len(out) == 0 {
		return []SavedEntry{a.SavedFiles[a.SavedIdx]}
	}
	return out
}

func (a *App) handlePurgeNoiseKey() {
	noiseFiles := a.findNoiseCaptures()
	if len(noiseFiles) == 0 {
		a.purgeConfirmUntil = time.Time{}
		a.refreshSavedFiles()
		a.SetToast("No noise captures (0)")
		return
	}
	if time.Now().After(a.purgeConfirmUntil) {
		a.purgeConfirmUntil = time.Now().Add(3 * time.Second)
		a.SetToast(fmt.Sprintf("Purge %d noise? 7 again", len(noiseFiles)))
		return
	}
	a.purgeConfirmUntil = time.Time{}
	removed := 0
	for _, entry := range noiseFiles {
		if os.Remove(entry.Path) == nil {
			removed++
		}
	}
	a.refreshSavedFiles()
	a.SetToast(fmt.Sprintf("Purged %d noise files", removed))
}

func (a *App) findNoiseCaptures() []SavedEntry {
	absCaptures, err := filepath.Abs(a.CapturesDir)
	if err != nil || a.CapturesDir == "" {
		return nil
	}
	var matches []SavedEntry
	for _, entry := range a.SavedFiles {
		if entry.File == nil || len(entry.File.RawData) == 0 {
			continue
		}
		absEntry, err := filepath.Abs(entry.Path)
		if err != nil || filepath.Dir(absEntry) != absCaptures {
			continue
		}
		pred, _, _ := rfml.ClassifyPulses(entry.File.RawData, entry.File.FreqHz, 8.0)
		if pred.IsNoise {
			matches = append(matches, entry)
		}
	}
	return matches
}

func (a *App) saveCurrentRaw() {
	if len(a.CapturedRaw) == 0 {
		// Create a sample capture if none recorded yet in sim mode.
		a.CapturedRaw = []int32{-10500, 350, -1050, 1050, -350, 350, -1050, -10500}
	}
	// Smart Save Guard: warn before saving high-confidence Noise; second press within 3s forces save.
	if a.HasPrediction && a.LastPrediction.IsNoise && time.Now().After(a.noiseSaveWarnUntil) {
		a.noiseSaveWarnUntil = time.Now().Add(3 * time.Second)
		a.SetToast(fmt.Sprintf("Noise (%.0f%%)! 6 again to save", a.LastPrediction.Confidence*100))
		return
	}
	a.noiseSaveWarnUntil = time.Time{}
	if err := os.MkdirAll(a.CapturesDir, 0o755); err != nil {
		a.SetToast("Save err!")
		return
	}
	name := fmt.Sprintf("raw_%s.sub", time.Now().Format("150405"))
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File",
		Version:  1,
		FreqHz:   a.ActiveFreq,
		Preset:   cc1101.GetPresetSpec(a.Preset).SubFileName,
		Protocol: "RAW",
		RawData:  a.CapturedRaw,
	}
	if err := os.WriteFile(filepath.Join(a.CapturesDir, name), []byte(sf.Format()), 0o644); err != nil {
		a.SetToast("Write err!")
		return
	}
	a.SetToast("Saved " + name)
}

func (a *App) saveDecodedKey() {
	if a.LastDecoded == nil {
		a.SetToast("No key to save")
		return
	}
	if err := os.MkdirAll(a.CapturesDir, 0o755); err != nil {
		a.SetToast("Save err!")
		return
	}
	name := fmt.Sprintf("%s_%s.sub", strings.ToLower(a.LastDecoded.Protocol), time.Now().Format("150405"))
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   a.ActiveFreq,
		Preset:   cc1101.GetPresetSpec(a.Preset).SubFileName,
		Protocol: a.LastDecoded.Protocol,
		Bit:      a.LastDecoded.Bits,
		Key:      a.LastDecoded.KeyHex,
		TE:       a.LastDecoded.TE,
	}
	if err := os.WriteFile(filepath.Join(a.CapturesDir, name), []byte(sf.Format()), 0o644); err != nil {
		a.SetToast("Write err!")
		return
	}
	a.SetToast("Saved " + name)
}

func (a *App) refreshSavedFiles() {
	a.SavedFiles = nil
	if a.Store != nil {
		for _, ref := range a.Store.ListUserEntries() {
			a.SavedFiles = append(a.SavedFiles, SavedEntry{Name: ref.Name, Path: ref.Path, File: ref.File})
		}
	}
	if a.SavedIdx >= len(a.SavedFiles) {
		a.SavedIdx = max(0, len(a.SavedFiles)-1)
	}
	a.rebuildFingerprintIndex()
}

func (a *App) rebuildFingerprintIndex() {
	entries := make([]rfml.FingerprintEntry, 0, len(a.SavedFiles))
	for _, s := range a.SavedFiles {
		var emb rfml.Embedding
		if len(s.File.RawData) > 0 {
			emb = rfml.ComputeEmbeddingFromRaw(s.File.RawData, s.File.FreqHz, s.File.Preset)
		} else {
			emb = rfml.ComputeEmbeddingFromKey(s.File.FreqHz, s.File.Preset, s.File.Protocol, s.File.Bit, s.File.TE)
		}
		entries = append(entries, rfml.FingerprintEntry{
			Name:      s.Name,
			Path:      s.Path,
			FreqHz:    s.File.FreqHz,
			Protocol:  s.File.Protocol,
			Embedding: emb,
		})
	}
	a.FPIndex = rfml.NewFingerprintIndex(entries)
}

func (a *App) drawSaved(c *Canvas) {
	title := fmt.Sprintf("User .sub Library (%d)", len(a.SavedFiles))
	if len(a.SavedSelected) > 0 {
		title = fmt.Sprintf("User .sub Library (%d, %d sel)", len(a.SavedFiles), len(a.SavedSelected))
	}
	c.DrawText(6, 21, title, ColCyan, 1)
	if len(a.SavedFiles) > 0 {
		c.DrawText(256, 21, "[V:SNAP]", ColMuted, 1)
	}
	c.FillRect(6, 32, 308, 108, ColPanel)
	c.DrawRect(6, 32, 308, 108, ColBorder)

	if len(a.SavedFiles) == 0 {
		c.DrawTextCentered(ScreenW/2, 76, "No .sub captures yet", ColMuted, 1)
		c.DrawTextCentered(ScreenW/2, 90, "Press 6 NEW to capture a remote", ColMuted, 1)
		return
	}

	start := max(0, a.SavedIdx-3)
	visible := min(6, len(a.SavedFiles)-start)
	for row := range visible {
		idx := start + row
		entry := a.SavedFiles[idx]
		y := 36 + row*17
		if idx == a.SavedIdx {
			c.FillRect(8, y-1, 304, 15, ColNavActive)
			c.DrawRect(8, y-1, 304, 15, ColCyan)
		}
		proto := entry.File.Protocol
		if proto == "" {
			proto = "RAW"
		}
		var label string
		if !a.Settings.ExpertMode {
			cur := " "
			if idx == a.SavedIdx {
				cur = ">"
			}
			label = fmt.Sprintf("%s %-18s %.2fM %s", cur, truncate(entry.Name, 18), float64(entry.File.FreqHz)/1e6, proto)
		} else {
			mark := " "
			if a.SavedSelected[entry.Name] {
				mark = "x"
			}
			label = fmt.Sprintf("[%s] %-16s %.2fM %s", mark, truncate(entry.Name, 16), float64(entry.File.FreqHz)/1e6, proto)
		}
		c.DrawText(10, y+3, label, ColText, 1)
	}
}
