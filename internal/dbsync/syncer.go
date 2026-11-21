package dbsync

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"time"

	"rf-heatmap/internal/library"
	"rf-heatmap/internal/subfile"
)

const (
	// MaxSubFileBytes caps individual downloaded .sub files to protect 512 MB RAM devices.
	MaxSubFileBytes = 1 << 20 // 1 MiB
	maxIndexBytes   = 8 << 20 // 8 MiB for large GitHub Trees JSON
	defaultTimeout  = 20 * time.Second

	defaultGitHubAPIBase = "https://api.github.com"
	defaultGitHubRawBase = "https://raw.githubusercontent.com"
	userAgent            = "rf-heatmap-dbsync/1.0"
)

// SyncReport summarizes the delta outcome for a single DatabaseSource.
type SyncReport struct {
	SourceID    string `json:"source_id"`
	NotModified bool   `json:"not_modified"`
	Added       int    `json:"added"`
	Updated     int    `json:"updated"`
	Skipped     int    `json:"skipped"`
	Pruned      int    `json:"pruned"`
	Failed      int    `json:"failed"`
	Err         string `json:"error,omitempty"`
}

// SyncProgress reports live incremental progress for UI/CLI feedback.
type SyncProgress struct {
	SourceID string
	Done     int
	Total    int
	Added    int
	Updated  int
}

type remoteEntry struct {
	LocalName string
	URL       string
	SHA       string // git blob sha or sha256
	Verify256 bool   // true when SHA is a hex SHA-256 of the file bytes
}

// Syncer performs incremental ETag/SHA delta sync into ~/rf-heatmap/databases/<id>/.
type Syncer struct {
	Client        *http.Client
	DatabasesDir  string
	GitHubAPIBase string
	GitHubRawBase string
}

// NewSyncer constructs a Syncer targeting databasesDir with safe defaults.
func NewSyncer(databasesDir string) *Syncer {
	return &Syncer{
		Client:        &http.Client{Timeout: defaultTimeout},
		DatabasesDir:  databasesDir,
		GitHubAPIBase: defaultGitHubAPIBase,
		GitHubRawBase: defaultGitHubRawBase,
	}
}

// SyncAll runs SyncSource sequentially for every enabled source.
func (s *Syncer) SyncAll(ctx context.Context, sources []DatabaseSource, onProgress func(SyncProgress)) []SyncReport {
	reports := make([]SyncReport, 0, len(sources))
	for _, src := range sources {
		if !src.Enabled {
			continue
		}
		if ctx.Err() != nil {
			reports = append(reports, SyncReport{SourceID: src.ID, Err: ctx.Err().Error()})
			break
		}
		reports = append(reports, s.SyncSource(ctx, src, onProgress))
	}
	return reports
}

// SyncSource performs an ETag + SHA delta sync for src into <DatabasesDir>/<src.ID>/.
func (s *Syncer) SyncSource(ctx context.Context, src DatabaseSource, onProgress func(SyncProgress)) SyncReport {
	norm, err := src.NormalizeAndValidate()
	if err != nil {
		return SyncReport{SourceID: src.ID, Err: err.Error()}
	}
	if s.DatabasesDir == "" {
		return SyncReport{SourceID: norm.ID, Err: "databases directory is not configured"}
	}
	packDir := filepath.Join(s.DatabasesDir, norm.ID)
	state := LoadSyncState(packDir)

	entries, newETag, notModified, err := s.fetchIndex(ctx, norm, state.ETag)
	if err != nil {
		return SyncReport{SourceID: norm.ID, Err: err.Error()}
	}
	if notModified {
		return SyncReport{SourceID: norm.ID, NotModified: true, Skipped: len(state.Files)}
	}

	report := s.applyDelta(ctx, norm, packDir, entries, &state, onProgress)
	if ctx.Err() != nil {
		report.Err = ctx.Err().Error()
	}
	if newETag != "" && report.Failed == 0 && report.Err == "" {
		state.ETag = newETag
	}
	state.LastSync = time.Now().UTC()
	if err := SaveSyncState(packDir, state); err != nil && report.Err == "" {
		report.Err = err.Error()
	}
	return report
}

func (s *Syncer) applyDelta(ctx context.Context, src DatabaseSource, packDir string, entries []remoteEntry, state *SyncState, onProgress func(SyncProgress)) SyncReport {
	report := SyncReport{SourceID: src.ID}
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		report.Err = err.Error()
		return report
	}
	remoteSet := make(map[string]struct{}, len(entries))
	for i, item := range entries {
		if ctx.Err() != nil {
			break
		}
		remoteSet[item.LocalName] = struct{}{}
		if state.Ignored[item.LocalName] {
			report.Skipped++
			if onProgress != nil {
				onProgress(SyncProgress{SourceID: src.ID, Done: i + 1, Total: len(entries), Added: report.Added, Updated: report.Updated})
			}
			continue
		}
		dstPath := filepath.Join(packDir, item.LocalName)
		prevSHA, known := state.Files[item.LocalName]
		if known && item.SHA != "" && prevSHA == item.SHA && fileExists(dstPath) {
			report.Skipped++
		} else if err := s.downloadSubFile(ctx, item, packDir, dstPath); err != nil {
			report.Failed++
		} else {
			if known {
				report.Updated++
			} else {
				report.Added++
			}
			state.Files[item.LocalName] = item.SHA
		}
		if onProgress != nil {
			onProgress(SyncProgress{SourceID: src.ID, Done: i + 1, Total: len(entries), Added: report.Added, Updated: report.Updated})
		}
	}
	if src.Prune && ctx.Err() == nil {
		report.Pruned = pruneOrphanedSubs(packDir, remoteSet, state)
	}
	return report
}

func (s *Syncer) downloadSubFile(ctx context.Context, item remoteEntry, packDir, dstPath string) error {
	if err := validateRemoteURL(item.URL); err != nil {
		return err
	}
	body, _, _, err := s.httpGet(ctx, item.URL, "", MaxSubFileBytes)
	if err != nil {
		return err
	}
	if item.Verify256 && item.SHA != "" {
		sum := sha256.Sum256(body)
		if !strings.EqualFold(hex.EncodeToString(sum[:]), item.SHA) {
			return fmt.Errorf("sha256 mismatch for %s", item.LocalName)
		}
	}
	if _, err := subfile.Parse(string(body)); err != nil {
		return fmt.Errorf("invalid .sub payload %s: %w", item.LocalName, err)
	}
	return writeAtomic(packDir, dstPath, body, 0o644)
}

func pruneOrphanedSubs(packDir string, remoteSet map[string]struct{}, state *SyncState) int {
	entries, err := os.ReadDir(packDir)
	if err != nil {
		return 0
	}
	pruned := 0
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sub") {
			continue
		}
		if _, ok := remoteSet[e.Name()]; ok {
			continue
		}
		if os.Remove(filepath.Join(packDir, e.Name())) == nil {
			delete(state.Files, e.Name())
			pruned++
		}
	}
	return pruned
}

func (s *Syncer) fetchIndex(ctx context.Context, src DatabaseSource, etag string) ([]remoteEntry, string, bool, error) {
	switch src.Type {
	case SourceGitHub:
		return s.fetchGitHubIndex(ctx, src, etag)
	case SourceManifest:
		return s.fetchManifestIndex(ctx, src, etag)
	default:
		return nil, "", false, ErrInvalidSource
	}
}

type githubTreeResp struct {
	Tree []struct {
		Path string `json:"path"`
		Type string `json:"type"`
		SHA  string `json:"sha"`
	} `json:"tree"`
}

func (s *Syncer) fetchGitHubIndex(ctx context.Context, src DatabaseSource, etag string) ([]remoteEntry, string, bool, error) {
	apiBase := strings.TrimRight(s.GitHubAPIBase, "/")
	rawBase := strings.TrimRight(s.GitHubRawBase, "/")
	treeURL := fmt.Sprintf("%s/repos/%s/%s/git/trees/%s?recursive=1",
		apiBase, url.PathEscape(src.Owner), url.PathEscape(src.Repo), url.PathEscape(src.Branch))

	body, newETag, status, err := s.httpGet(ctx, treeURL, etag, maxIndexBytes)
	if err != nil {
		return nil, "", false, err
	}
	if status == http.StatusNotModified {
		return nil, etag, true, nil
	}
	var tree githubTreeResp
	if err := json.Unmarshal(body, &tree); err != nil {
		return nil, "", false, fmt.Errorf("dbsync: parse github tree: %w", err)
	}
	prefix := ""
	if src.Subpath != "" {
		prefix = src.Subpath + "/"
	}
	entries := make([]remoteEntry, 0)
	for _, node := range tree.Tree {
		if node.Type != "blob" || !strings.HasSuffix(strings.ToLower(node.Path), ".sub") {
			continue
		}
		rel := node.Path
		if prefix != "" {
			if !strings.HasPrefix(node.Path, prefix) {
				continue
			}
			rel = strings.TrimPrefix(node.Path, prefix)
		}
		localName, err := flattenSafeSubName(rel)
		if err != nil {
			continue
		}
		rawURL := fmt.Sprintf("%s/%s/%s/%s/%s", rawBase, src.Owner, src.Repo, src.Branch, escapeURLPath(node.Path))
		entries = append(entries, remoteEntry{LocalName: localName, URL: rawURL, SHA: node.SHA})
	}
	return entries, newETag, false, nil
}

type manifestDoc struct {
	Files []struct {
		Name   string `json:"name"`
		URL    string `json:"url"`
		SHA256 string `json:"sha256"`
	} `json:"files"`
}

func (s *Syncer) fetchManifestIndex(ctx context.Context, src DatabaseSource, etag string) ([]remoteEntry, string, bool, error) {
	body, newETag, status, err := s.httpGet(ctx, src.URL, etag, maxIndexBytes)
	if err != nil {
		return nil, "", false, err
	}
	if status == http.StatusNotModified {
		return nil, etag, true, nil
	}
	var doc manifestDoc
	if err := json.Unmarshal(body, &doc); err != nil {
		return nil, "", false, fmt.Errorf("dbsync: parse manifest: %w", err)
	}
	entries := make([]remoteEntry, 0, len(doc.Files))
	for _, f := range doc.Files {
		cleanName, err := library.SanitizeSubName(f.Name)
		if err != nil {
			continue
		}
		entries = append(entries, remoteEntry{
			LocalName: cleanName,
			URL:       strings.TrimSpace(f.URL),
			SHA:       strings.TrimSpace(f.SHA256),
			Verify256: strings.TrimSpace(f.SHA256) != "",
		})
	}
	return entries, newETag, false, nil
}

func (s *Syncer) httpGet(ctx context.Context, rawURL, etag string, maxBytes int64) ([]byte, string, int, error) {
	client := s.Client
	if client == nil {
		client = &http.Client{Timeout: defaultTimeout}
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, rawURL, nil)
	if err != nil {
		return nil, "", 0, err
	}
	req.Header.Set("User-Agent", userAgent)
	if etag != "" {
		req.Header.Set("If-None-Match", etag)
	}
	resp, err := client.Do(req)
	if err != nil {
		return nil, "", 0, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotModified {
		return nil, etag, resp.StatusCode, nil
	}
	if resp.StatusCode != http.StatusOK {
		return nil, "", resp.StatusCode, fmt.Errorf("http %d for %s", resp.StatusCode, rawURL)
	}
	data, err := io.ReadAll(io.LimitReader(resp.Body, maxBytes+1))
	if err != nil {
		return nil, "", resp.StatusCode, err
	}
	if int64(len(data)) > maxBytes {
		return nil, "", resp.StatusCode, errors.New("response exceeds maximum allowed size")
	}
	return data, resp.Header.Get("ETag"), resp.StatusCode, nil
}

func flattenSafeSubName(relPath string) (string, error) {
	if strings.Contains(relPath, "..") || strings.ContainsRune(relPath, '\\') {
		return "", ErrInvalidSource
	}
	flat := strings.ReplaceAll(strings.Trim(relPath, "/"), "/", "_")
	return library.SanitizeSubName(flat)
}

func escapeURLPath(p string) string {
	parts := strings.Split(p, "/")
	for i := range parts {
		parts[i] = url.PathEscape(parts[i])
	}
	return strings.Join(parts, "/")
}
