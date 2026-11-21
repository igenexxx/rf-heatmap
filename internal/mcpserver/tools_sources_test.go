package mcpserver

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rf-heatmap/internal/config"
	"rf-heatmap/internal/dbsync"
	"rf-heatmap/internal/subfile"
)

func TestMCPSourcesAddSyncAndRemove(t *testing.T) {
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File", Version: 1, FreqHz: 433_920_000,
		Preset: "FuriHalSubGhzPresetOok650Async", Protocol: "Princeton",
		Bit: 24, Key: "00 00 00 00 00 AA BB CC", TE: 350,
	}
	subText := sf.Format()

	var srvURL string
	httpSrv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			w.Header().Set("ETag", `"m-v1"`)
			if r.Header.Get("If-None-Match") == `"m-v1"` {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			_ = json.NewEncoder(w).Encode(map[string]any{
				"files": []map[string]string{{"name": "ac_pwr.sub", "url": srvURL + "/ac_pwr.sub"}},
			})
		case "/ac_pwr.sub":
			_, _ = w.Write([]byte(subText))
		}
	}))
	defer httpSrv.Close()
	srvURL = httpSrv.URL

	root := t.TempDir()
	dbDir := filepath.Join(root, "databases")
	sourcesPath := filepath.Join(root, "sources.json")
	syncer := &dbsync.Syncer{Client: httpSrv.Client(), DatabasesDir: dbDir}

	srv := NewServer(Options{
		CapturesDir:    filepath.Join(root, "captures"),
		DatabasesDir:   dbDir,
		SourcesPath:    sourcesPath,
		Syncer:         syncer,
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
	})

	// 1. Add manifest source.
	addArgs := `{"id":"ac_pack","type":"manifest","url":"` + srvURL + `/manifest.json"}`
	res, rpcErr := srv.dispatchTool("rf_add_source", json.RawMessage(addArgs))
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"added":true`) {
		t.Fatalf("rf_add_source failed: res=%+v err=%+v", res, rpcErr)
	}

	// 2. List sources.
	res, rpcErr = srv.dispatchTool("rf_list_sources", nil)
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"id":"ac_pack"`) {
		t.Fatalf("rf_list_sources failed: res=%+v err=%+v", res, rpcErr)
	}

	// 3. First sync downloads ac_pwr.sub into databases/ac_pack/.
	res, rpcErr = srv.dispatchTool("rf_sync_sources", json.RawMessage(`{"id":"ac_pack"}`))
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"added":1`) {
		t.Fatalf("first rf_sync_sources failed: res=%+v err=%+v", res, rpcErr)
	}
	if _, err := os.Stat(filepath.Join(dbDir, "ac_pack", "ac_pwr.sub")); err != nil {
		t.Fatalf("expected downloaded ac_pwr.sub: %v", err)
	}

	// 4. Second sync hits 304 Not Modified.
	res, rpcErr = srv.dispatchTool("rf_sync_sources", nil)
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"not_modified":true`) {
		t.Fatalf("second rf_sync_sources expected not_modified: res=%+v err=%+v", res, rpcErr)
	}

	// 4b. Delete single pack file via rf_delete_pack_files (marks ignored in .sync-state.json).
	res, rpcErr = srv.dispatchTool("rf_delete_pack_files", json.RawMessage(`{"pack":"db:ac_pack","names":["ac_pwr.sub"],"confirm":true}`))
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"deleted":1`) {
		t.Fatalf("rf_delete_pack_files failed: res=%+v err=%+v", res, rpcErr)
	}
	if st := dbsync.LoadSyncState(filepath.Join(dbDir, "ac_pack")); !st.Ignored["ac_pwr.sub"] {
		t.Fatalf("expected ac_pwr.sub ignored after rf_delete_pack_files")
	}

	// 5. Remove source without confirm=true fails; with confirm=true & delete_files=true succeeds.
	res, rpcErr = srv.dispatchTool("rf_remove_source", json.RawMessage(`{"id":"ac_pack","confirm":false}`))
	if rpcErr != nil || !res.IsError {
		t.Fatalf("expected confirm guard, got %+v %+v", res, rpcErr)
	}
	res, rpcErr = srv.dispatchTool("rf_remove_source", json.RawMessage(`{"id":"ac_pack","confirm":true,"delete_files":true}`))
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"deleted_files":true`) {
		t.Fatalf("rf_remove_source failed: res=%+v err=%+v", res, rpcErr)
	}
}
