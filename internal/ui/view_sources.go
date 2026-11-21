package ui

import (
	"cmp"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"

	"rf-heatmap/internal/dbsync"
)

func (a *App) openSourcesManage() {
	a.ShowSources = true
	a.SourceIdx = 0
	a.SourceSelected = make(map[string]bool)
	a.sourceDelMode = 0
	a.sourceDelUntil = time.Time{}
	a.refreshManagedSources()
}

func (a *App) refreshManagedSources() {
	byID := make(map[string]ManagedSourceRow)
	if sources, err := dbsync.LoadSources(a.SourcesPath); err == nil {
		for _, s := range sources {
			byID[s.ID] = ManagedSourceRow{
				ID:         s.ID,
				HasSource:  true,
				Enabled:    s.Enabled,
				SourceType: s.Type,
			}
		}
	}
	if a.Store != nil && a.Store.DatabasesDir != "" {
		if entries, err := os.ReadDir(a.Store.DatabasesDir); err == nil {
			for _, e := range entries {
				if !e.IsDir() {
					continue
				}
				id := e.Name()
				row := byID[id]
				row.ID = id
				row.HasDir = true
				row.FileCount = countDirSubFiles(filepath.Join(a.Store.DatabasesDir, id))
				byID[id] = row
			}
		}
	}
	rows := make([]ManagedSourceRow, 0, len(byID))
	for _, r := range byID {
		rows = append(rows, r)
	}
	slices.SortFunc(rows, func(x, y ManagedSourceRow) int { return cmp.Compare(x.ID, y.ID) })
	a.SourceRows = rows
	if a.SourceIdx >= len(a.SourceRows) {
		a.SourceIdx = max(0, len(a.SourceRows)-1)
	}
}

func countDirSubFiles(dir string) int {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && strings.HasSuffix(strings.ToLower(e.Name()), ".sub") {
			n++
		}
	}
	return n
}

func (a *App) handleSourcesManageKey(act KeyAction) {
	switch act {
	case KeyQuit, KeyNav8:
		a.ShowSources = false
		a.refreshProbePacks()
	case KeyUp:
		a.SourceIdx = max(0, a.SourceIdx-1)
	case KeyDown:
		a.SourceIdx = min(max(0, len(a.SourceRows)-1), a.SourceIdx+1)
	case KeySpace, KeyEnter, KeyMark:
		a.toggleSourceMark()
	case KeyNav4:
		a.toggleSelectedSourcesEnabled()
	case KeyNav5:
		a.deleteSelectedSources(1) // directory only
	case KeyNav6, KeyDelete:
		a.deleteSelectedSources(2) // source + directory
	case KeyNav7:
		a.triggerDBSync()
	}
}

func (a *App) toggleSourceMark() {
	if len(a.SourceRows) == 0 {
		return
	}
	if a.SourceSelected == nil {
		a.SourceSelected = make(map[string]bool)
	}
	id := a.SourceRows[a.SourceIdx].ID
	if a.SourceSelected[id] {
		delete(a.SourceSelected, id)
	} else {
		a.SourceSelected[id] = true
	}
	a.sourceDelUntil = time.Time{}
}

func (a *App) targetManagedSources() []ManagedSourceRow {
	if len(a.SourceRows) == 0 {
		return nil
	}
	if len(a.SourceSelected) == 0 {
		return []ManagedSourceRow{a.SourceRows[a.SourceIdx]}
	}
	var out []ManagedSourceRow
	for _, r := range a.SourceRows {
		if a.SourceSelected[r.ID] {
			out = append(out, r)
		}
	}
	if len(out) == 0 {
		return []ManagedSourceRow{a.SourceRows[a.SourceIdx]}
	}
	return out
}

func (a *App) toggleSelectedSourcesEnabled() {
	targets := a.targetManagedSources()
	if len(targets) == 0 {
		return
	}
	list, err := dbsync.LoadSources(a.SourcesPath)
	if err != nil {
		a.SetToast("Sources err!")
		return
	}
	targetIDs := make(map[string]bool, len(targets))
	for _, t := range targets {
		targetIDs[t.ID] = true
	}
	toggled := 0
	for i := range list {
		if targetIDs[list[i].ID] {
			list[i].Enabled = !list[i].Enabled
			toggled++
		}
	}
	if toggled == 0 {
		a.SetToast("Local folder only")
		return
	}
	if err := dbsync.SaveSources(a.SourcesPath, list); err != nil {
		a.SetToast("Save err!")
		return
	}
	a.refreshManagedSources()
	a.SetToast(fmt.Sprintf("Toggled %d source(s)", toggled))
}

func (a *App) deleteSelectedSources(mode int) {
	targets := a.targetManagedSources()
	if len(targets) == 0 {
		return
	}
	if a.sourceDelMode != mode || time.Now().After(a.sourceDelUntil) {
		a.sourceDelMode = mode
		a.sourceDelUntil = time.Now().Add(3 * time.Second)
		if mode == 1 {
			a.SetToast(fmt.Sprintf("Del %d dir(s)? 5 again", len(targets)))
		} else {
			a.SetToast(fmt.Sprintf("Del %d src+dir? 6 again", len(targets)))
		}
		return
	}
	a.sourceDelMode = 0
	a.sourceDelUntil = time.Time{}

	dirsDel, srcsDel := a.executeSourceDeletions(targets, mode == 2)
	clear(a.SourceSelected)
	a.refreshManagedSources()
	a.refreshProbePacks()
	if mode == 1 {
		a.SetToast(fmt.Sprintf("Deleted %d dir(s)", dirsDel))
		return
	}
	a.SetToast(fmt.Sprintf("Del %d src, %d dir", srcsDel, dirsDel))
}

func (a *App) executeSourceDeletions(targets []ManagedSourceRow, removeFromJSON bool) (int, int) {
	dirsDel, srcsDel := 0, 0
	dbDir := ""
	if a.Store != nil {
		dbDir = a.Store.DatabasesDir
	}
	for _, t := range targets {
		if t.HasDir && dbDir != "" {
			if dbsync.DeletePackDir(dbDir, t.ID) == nil {
				dirsDel++
			}
		}
		if removeFromJSON && t.HasSource {
			if dbsync.RemoveSource(a.SourcesPath, t.ID) == nil {
				srcsDel++
			}
		}
	}
	return dirsDel, srcsDel
}

func (a *App) drawSourcesManage(c *Canvas) {
	title := fmt.Sprintf("Manage DB Sources (%d)", len(a.SourceRows))
	if len(a.SourceSelected) > 0 {
		title = fmt.Sprintf("DB Sources (%d, %d sel)", len(a.SourceRows), len(a.SourceSelected))
	}
	c.DrawText(6, 21, title, ColCyan, 1)
	c.FillRect(6, 32, 308, 108, ColPanel)
	c.DrawRect(6, 32, 308, 108, ColBorder)

	if len(a.SourceRows) == 0 {
		c.DrawTextCentered(ScreenW/2, 76, "No DB sources or folders", ColMuted, 1)
		c.DrawTextCentered(ScreenW/2, 90, "Add via sources.json or MCP", ColMuted, 1)
		return
	}

	start := max(0, a.SourceIdx-3)
	visible := min(6, len(a.SourceRows)-start)
	for r := range visible {
		idx := start + r
		row := a.SourceRows[idx]
		y := 36 + r*17
		if idx == a.SourceIdx {
			c.FillRect(8, y-1, 304, 15, ColNavActive)
			c.DrawRect(8, y-1, 304, 15, ColCyan)
		}
		mark := " "
		if a.SourceSelected[row.ID] {
			mark = "x"
		}
		status, sCol := managedSourceStatus(row)
		label := fmt.Sprintf("[%s] %-16s %4d", mark, truncate(row.ID, 16), row.FileCount)
		c.DrawText(10, y+3, label, ColText, 1)
		c.DrawText(306-len(status)*6, y+3, status, sCol, 1)
	}
}

func managedSourceStatus(row ManagedSourceRow) (string, uint16) {
	if !row.HasSource {
		return "LOCAL", ColCyan
	}
	if row.Enabled {
		return "ON", ColGreen
	}
	return "OFF", ColMuted
}
