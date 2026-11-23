package ui

import (
	"os"
	"path/filepath"
	"testing"

	"rf-heatmap/internal/dbsync"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/subfile"
)

func TestBatchDeleteInSavedProbeAndSourcesManage(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "captures")
	dbDir := filepath.Join(root, "databases")
	sourcesPath := filepath.Join(root, "sources.json")

	writeSub := func(path, key string) {
		t.Helper()
		sf := &subfile.SubFile{
			FileType: "Flipper SubGhz Key File", Version: 1, FreqHz: 433_920_000,
			Preset: "FuriHalSubGhzPresetOok650Async", Protocol: "Princeton",
			Bit: 24, Key: key, TE: 350,
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(sf.Format()), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	writeSub(filepath.Join(userDir, "u1.sub"), "00 00 00 00 00 00 00 01")
	writeSub(filepath.Join(userDir, "u2.sub"), "00 00 00 00 00 00 00 02")
	writeSub(filepath.Join(userDir, "u3.sub"), "00 00 00 00 00 00 00 03")
	writeSub(filepath.Join(dbDir, "fans", "f1.sub"), "00 00 00 00 00 00 00 11")
	writeSub(filepath.Join(dbDir, "fans", "f2.sub"), "00 00 00 00 00 00 00 12")
	writeSub(filepath.Join(dbDir, "led", "l1.sub"), "00 00 00 00 00 00 00 21")

	_, _ = dbsync.UpsertSource(sourcesPath, dbsync.DatabaseSource{ID: "fans", Type: dbsync.SourceGitHub, Owner: "o", Repo: "r", Enabled: true})
	_, _ = dbsync.UpsertSource(sourcesPath, dbsync.DatabaseSource{ID: "led", Type: dbsync.SourceGitHub, Owner: "o", Repo: "r", Enabled: true})

	app := NewApp(nil, userDir)
	defer app.Close()
	app.Settings.ExpertMode = true
	app.SetSourcesPath(sourcesPath)
	app.SetLibraryStore(library.NewStore(userDir, dbDir, nil))

	// 1. SAVED: multi-select 2 files with Space, delete batch with 2-step KeyNav6.
	app.Mode = ModeSaved
	app.HandleKey(KeySpace) // mark row 0
	app.HandleKey(KeyDown)
	app.HandleKey(KeySpace) // mark row 1
	app.HandleKey(KeyNav6)  // 1st press: confirm prompt
	if len(app.SavedFiles) != 3 {
		t.Fatalf("expected 3 files before confirm, got %d", len(app.SavedFiles))
	}
	app.HandleKey(KeyNav6) // 2nd press: execute batch delete
	if len(app.SavedFiles) != 1 {
		t.Fatalf("expected 1 file left after batch delete, got %d", len(app.SavedFiles))
	}

	// 2. PROBE: mark 1 file in db:fans with KeyMark, 2-step delete records ignored in .sync-state.json.
	app.Mode = ModeProbe
	app.refreshProbePacks()
	app.HandleKey(KeyMark)
	app.HandleKey(KeyDelete) // prompt
	app.HandleKey(KeyDelete) // confirm
	st := dbsync.LoadSyncState(filepath.Join(dbDir, "fans"))
	if !st.Ignored["f1.sub"] {
		t.Fatalf("expected f1.sub recorded in .sync-state.json ignored, got %+v", st.Ignored)
	}

	// 3. SET -> Manage DB Sources: open row 7 (Expert Mode), toggle enabled, batch delete source+dir.
	app.Mode = ModeSettings
	app.SettingsIdx = 7
	app.HandleKey(KeyEnter)
	if !app.ShowSources || len(app.SourceRows) != 2 {
		t.Fatalf("expected ShowSources with 2 rows, got show=%v len=%d", app.ShowSources, len(app.SourceRows))
	}
	c, _ := NewCanvas("")
	app.Render(c)

	// Mark both fans & led, press 6 (ALL) twice -> deletes both from sources.json and databases/.
	app.HandleKey(KeySpace)
	app.HandleKey(KeyDown)
	app.HandleKey(KeySpace)
	app.HandleKey(KeyNav6) // prompt
	app.HandleKey(KeyNav6) // confirm
	if len(app.SourceRows) != 0 {
		t.Fatalf("expected 0 managed sources after batch ALL delete, got %d", len(app.SourceRows))
	}
	if _, err := os.Stat(filepath.Join(dbDir, "fans")); !os.IsNotExist(err) {
		t.Fatalf("expected databases/fans directory removed")
	}
	remaining, _ := dbsync.LoadSources(sourcesPath)
	if len(remaining) != 0 {
		t.Fatalf("expected sources.json empty, got %+v", remaining)
	}
	app.HandleKey(KeyNav8) // BACK
	if app.ShowSources {
		t.Fatal("expected ShowSources=false after BACK")
	}
}
