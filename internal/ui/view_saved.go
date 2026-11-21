package ui

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"rf-heatmap/internal/cc1101"
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
			a.SetToast("TX: " + entry.Name)
		}
	case KeyNav5:
		if len(a.SavedFiles) > 0 {
			entry := a.SavedFiles[a.SavedIdx]
			a.ActiveFreq = entry.File.FreqHz
			if a.LiveBaseline != nil {
				a.LiveBaseline.Reset()
			}
			a.Mode = ModePulse
			a.SetToast("Loaded " + entry.Name)
		}
	case KeyNav6, KeyDelete:
		if len(a.SavedFiles) > 0 {
			entry := a.SavedFiles[a.SavedIdx]
			_ = os.Remove(entry.Path)
			a.refreshSavedFiles()
			a.SetToast("Deleted " + entry.Name)
		}
	case KeyNav7:
		a.handlePurgeNoiseKey()
	}
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
	dirs := []string{a.CapturesDir}
	if home, err := os.UserHomeDir(); err == nil {
		dirs = append(dirs, filepath.Join(home, "subg", "files"))
	}
	for _, dir := range dirs {
		entries, err := os.ReadDir(dir)
		if err != nil {
			continue
		}
		for _, e := range entries {
			if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sub") {
				continue
			}
			full := filepath.Join(dir, e.Name())
			raw, err := os.ReadFile(full)
			if err != nil {
				continue
			}
			if sf, err := subfile.Parse(string(raw)); err == nil {
				a.SavedFiles = append(a.SavedFiles, SavedEntry{Name: e.Name(), Path: full, File: sf})
			}
		}
	}
	slices.SortFunc(a.SavedFiles, func(x, y SavedEntry) int { return cmp.Compare(y.Name, x.Name) })
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
	c.DrawText(6, 21, fmt.Sprintf("Saved .sub Library (%d)", len(a.SavedFiles)), ColCyan, 1)
	c.FillRect(6, 32, 308, 108, ColPanel)
	c.DrawRect(6, 32, 308, 108, ColBorder)

	if len(a.SavedFiles) == 0 {
		c.DrawTextCentered(ScreenW/2, 76, "No .sub files found", ColMuted, 1)
		c.DrawTextCentered(ScreenW/2, 90, "Capture in PULSE or DECODE first", ColMuted, 1)
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
		label := fmt.Sprintf("%-18s %.2fM %s", truncate(entry.Name, 18), float64(entry.File.FreqHz)/1e6, proto)
		c.DrawText(12, y+3, label, ColText, 1)
	}
}
