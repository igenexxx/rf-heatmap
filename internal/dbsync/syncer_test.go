package dbsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"

	"rf-heatmap/internal/subfile"
)

func validSubPayload(key string) string {
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   433_920_000,
		Preset:   "FuriHalSubGhzPresetOok650Async",
		Protocol: "Princeton",
		Bit:      24,
		Key:      key,
		TE:       350,
	}
	return sf.Format()
}

func TestGitHubDeltaSyncETagAndPrune(t *testing.T) {
	var rawDownloads atomic.Int32
	etag := `"tree-v1"`
	treePayload := map[string]any{
		"tree": []map[string]string{
			{"path": "Sub-GHz/Fans/power.sub", "type": "blob", "sha": "sha-pwr-1"},
			{"path": "Sub-GHz/Fans/speed.sub", "type": "blob", "sha": "sha-spd-1"},
			{"path": "Sub-GHz/Fans/README.md", "type": "blob", "sha": "sha-md"},
		},
	}
	rawBody := map[string]string{
		"/owner/repo/main/Sub-GHz/Fans/power.sub": validSubPayload("00 00 00 00 00 11 22 01"),
		"/owner/repo/main/Sub-GHz/Fans/speed.sub": validSubPayload("00 00 00 00 00 11 22 02"),
		"/owner/repo/main/Sub-GHz/Fans/light.sub": validSubPayload("00 00 00 00 00 11 22 03"),
	}

	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/repos/owner/repo/git/trees/main" {
			if r.Header.Get("If-None-Match") == etag {
				w.WriteHeader(http.StatusNotModified)
				return
			}
			w.Header().Set("ETag", etag)
			_ = json.NewEncoder(w).Encode(treePayload)
			return
		}
		if body, ok := rawBody[r.URL.Path]; ok {
			rawDownloads.Add(1)
			_, _ = w.Write([]byte(body))
			return
		}
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	dbDir := t.TempDir()
	syncer := &Syncer{
		Client:        srv.Client(),
		DatabasesDir:  dbDir,
		GitHubAPIBase: srv.URL,
		GitHubRawBase: srv.URL,
	}
	src := DatabaseSource{
		ID:      "fans",
		Type:    SourceGitHub,
		Owner:   "owner",
		Repo:    "repo",
		Branch:  "main",
		Subpath: "Sub-GHz/Fans",
		Enabled: true,
	}

	// 1. Initial sync downloads 2 .sub files (ignores README.md).
	rep1 := syncer.SyncSource(context.Background(), src, nil)
	if rep1.Err != "" || rep1.Added != 2 || rep1.Updated != 0 || rep1.Skipped != 0 {
		t.Fatalf("initial sync unexpected: %+v", rep1)
	}
	if rawDownloads.Load() != 2 {
		t.Fatalf("rawDownloads = %d, want 2", rawDownloads.Load())
	}

	// 2. Second sync with unchanged ETag returns 304 Not Modified and 0 downloads.
	rep2 := syncer.SyncSource(context.Background(), src, nil)
	if !rep2.NotModified || rep2.Added != 0 || rep2.Updated != 0 || rep2.Skipped != 2 {
		t.Fatalf("second sync unexpected: %+v", rep2)
	}
	if rawDownloads.Load() != 2 {
		t.Fatalf("expected 0 extra downloads on 304, got %d", rawDownloads.Load())
	}

	// 3. Remote changes: power.sub updated, light.sub added, speed.sub removed.
	// With Prune=false: ONLY power.sub and light.sub are downloaded; speed.sub stays locally.
	etag = `"tree-v2"`
	treePayload["tree"] = []map[string]string{
		{"path": "Sub-GHz/Fans/power.sub", "type": "blob", "sha": "sha-pwr-2"},
		{"path": "Sub-GHz/Fans/light.sub", "type": "blob", "sha": "sha-lgt-1"},
	}
	rep3 := syncer.SyncSource(context.Background(), src, nil)
	if rep3.Err != "" || rep3.Added != 1 || rep3.Updated != 1 || rep3.Pruned != 0 {
		t.Fatalf("delta sync unexpected: %+v", rep3)
	}
	if rawDownloads.Load() != 4 {
		t.Fatalf("expected exactly 2 delta downloads (total 4), got %d", rawDownloads.Load())
	}
	if !fileExists(filepath.Join(dbDir, "fans", "speed.sub")) {
		t.Fatal("speed.sub must remain when Prune=false")
	}

	// 4. With Prune=true and new ETag: speed.sub is pruned while power.sub & light.sub are skipped.
	etag = `"tree-v3"`
	src.Prune = true
	rep4 := syncer.SyncSource(context.Background(), src, nil)
	if rep4.Pruned != 1 || rep4.Skipped != 2 || rep4.Added != 0 || rep4.Updated != 0 {
		t.Fatalf("prune sync unexpected: %+v", rep4)
	}
	if fileExists(filepath.Join(dbDir, "fans", "speed.sub")) {
		t.Fatal("speed.sub should have been pruned")
	}

	// 5. Manually delete light.sub + MarkIgnored -> subsequent sync must NOT re-download it.
	_ = os.Remove(filepath.Join(dbDir, "fans", "light.sub"))
	if err := MarkIgnored(filepath.Join(dbDir, "fans"), []string{"light.sub"}); err != nil {
		t.Fatalf("MarkIgnored failed: %v", err)
	}
	etag = `"tree-v4"`
	rep5 := syncer.SyncSource(context.Background(), src, nil)
	if rep5.Added != 0 || rep5.Updated != 0 || rep5.Skipped != 2 {
		t.Fatalf("ignored file must be skipped, got %+v", rep5)
	}
	if fileExists(filepath.Join(dbDir, "fans", "light.sub")) {
		t.Fatal("ignored light.sub should not be re-downloaded")
	}

	// 6. DeletePackDir removes fans directory safely and rejects invalid IDs.
	if err := DeletePackDir(dbDir, "../evil"); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("expected ErrInvalidSource on traversal, got %v", err)
	}
	if err := DeletePackDir(dbDir, "fans"); err != nil {
		t.Fatalf("DeletePackDir(fans) failed: %v", err)
	}
	if _, err := os.Stat(filepath.Join(dbDir, "fans")); !os.IsNotExist(err) {
		t.Fatalf("expected fans directory removed, err=%v", err)
	}
}

func TestManifestSyncSHA256AndAdversarialRejection(t *testing.T) {
	goodBody := validSubPayload("00 00 00 00 00 AA BB CC")
	goodSum := sha256.Sum256([]byte(goodBody))
	goodHex := hex.EncodeToString(goodSum[:])

	var srvURL string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/manifest.json":
			_ = json.NewEncoder(w).Encode(map[string]any{
				"files": []map[string]string{
					{"name": "ok.sub", "url": srvURL + "/ok.sub", "sha256": goodHex},
					{"name": "bad_hash.sub", "url": srvURL + "/ok.sub", "sha256": "deadbeef"},
					{"name": "invalid_sub.sub", "url": srvURL + "/invalid.sub"},
					{"name": "../escape.sub", "url": srvURL + "/ok.sub"},
				},
			})
		case "/ok.sub":
			_, _ = w.Write([]byte(goodBody))
		case "/invalid.sub":
			_, _ = w.Write([]byte("not a valid flipper sub file"))
		}
	}))
	defer srv.Close()
	srvURL = srv.URL

	dbDir := t.TempDir()
	syncer := &Syncer{Client: srv.Client(), DatabasesDir: dbDir}
	src := DatabaseSource{ID: "my_pack", Type: SourceManifest, URL: srvURL + "/manifest.json", Enabled: true}

	rep := syncer.SyncSource(context.Background(), src, nil)
	if rep.Added != 1 || rep.Failed != 2 {
		t.Fatalf("expected 1 added, 2 failed (bad hash + invalid sub; traversal skipped), got %+v", rep)
	}
	if !fileExists(filepath.Join(dbDir, "my_pack", "ok.sub")) {
		t.Fatal("expected ok.sub to be saved")
	}
	if fileExists(filepath.Join(dbDir, "escape.sub")) {
		t.Fatal("path traversal file must never be created")
	}
}

func TestSourcesRegistryAndGitHubURLParser(t *testing.T) {
	owner, repo, branch, sub, err := ParseGitHubURL("https://github.com/UberGuidoZ/Flipper/tree/main/Sub-GHz/Ceiling_Fans")
	if err != nil || owner != "UberGuidoZ" || repo != "Flipper" || branch != "main" || sub != "Sub-GHz/Ceiling_Fans" {
		t.Fatalf("ParseGitHubURL got (%q,%q,%q,%q,%v)", owner, repo, branch, sub, err)
	}

	path := filepath.Join(t.TempDir(), "sources.json")
	if _, err := UpsertSource(path, DatabaseSource{ID: "user", Type: SourceGitHub, Owner: "a", Repo: "b"}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("reserved id 'user' must fail: %v", err)
	}
	if _, err := UpsertSource(path, DatabaseSource{ID: "bad", Type: SourceManifest, URL: "http://evil.example/m.json"}); !errors.Is(err, ErrInvalidSource) {
		t.Fatalf("non-https remote manifest must fail: %v", err)
	}

	saved, err := UpsertSource(path, DatabaseSource{ID: "fans", Type: SourceGitHub, Owner: "o", Repo: "r", Enabled: true})
	if err != nil || saved.Branch != "main" {
		t.Fatalf("UpsertSource failed: %+v err=%v", saved, err)
	}
	list, err := LoadSources(path)
	if err != nil || len(list) != 1 || list[0].ID != "fans" {
		t.Fatalf("LoadSources got %+v err=%v", list, err)
	}
	if err := RemoveSource(path, "fans"); err != nil {
		t.Fatalf("RemoveSource failed: %v", err)
	}
	if err := RemoveSource(path, "missing"); !errors.Is(err, ErrSourceNotFound) {
		t.Fatalf("expected ErrSourceNotFound, got %v", err)
	}
	if info, err := os.Stat(path); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("sources.json perm must be 0600, err=%v", err)
	}
}
