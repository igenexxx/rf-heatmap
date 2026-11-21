package dbsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"
)

// SyncStateFilename is the hidden metadata file inside each synced pack directory.
// Because it does not end in ".sub", library.Store ignores it automatically.
const SyncStateFilename = ".sync-state.json"

// SyncState tracks the remote ETag, per-file SHA hashes, and locally deleted (ignored) files.
type SyncState struct {
	ETag     string            `json:"etag,omitempty"`
	Files    map[string]string `json:"files"`
	Ignored  map[string]bool   `json:"ignored,omitempty"`
	LastSync time.Time         `json:"last_sync"`
}

// LoadSyncState reads .sync-state.json from packDir, returning an empty initialized state if missing.
func LoadSyncState(packDir string) SyncState {
	st := SyncState{
		Files:   make(map[string]string),
		Ignored: make(map[string]bool),
	}
	raw, err := os.ReadFile(filepath.Join(packDir, SyncStateFilename))
	if err != nil {
		return st
	}
	_ = json.Unmarshal(raw, &st)
	if st.Files == nil {
		st.Files = make(map[string]string)
	}
	if st.Ignored == nil {
		st.Ignored = make(map[string]bool)
	}
	return st
}

// MarkIgnored records manually deleted .sub filenames in .sync-state.json so future syncs skip them.
func MarkIgnored(packDir string, names []string) error {
	if packDir == "" || len(names) == 0 {
		return nil
	}
	st := LoadSyncState(packDir)
	for _, n := range names {
		if n == "" {
			continue
		}
		st.Ignored[n] = true
		delete(st.Files, n)
	}
	return SaveSyncState(packDir, st)
}

// DeletePackDir removes an entire synced pack subdirectory strictly inside databasesDir.
func DeletePackDir(databasesDir, id string) error {
	id = strings.ToLower(strings.TrimSpace(id))
	if err := validateSourceID(id); err != nil {
		return err
	}
	if databasesDir == "" {
		return errors.New("dbsync: databases directory is not configured")
	}
	absBase, err := filepath.Abs(databasesDir)
	if err != nil {
		return fmt.Errorf("dbsync: resolve databases dir: %w", err)
	}
	target := filepath.Join(absBase, id)
	rel, err := filepath.Rel(absBase, target)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return fmt.Errorf("%w: path outside databases sandbox", ErrInvalidSource)
	}
	return os.RemoveAll(target)
}

// SaveSyncState atomically persists .sync-state.json inside packDir.
func SaveSyncState(packDir string, st SyncState) error {
	if st.Files == nil {
		st.Files = make(map[string]string)
	}
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		return fmt.Errorf("dbsync: mkdir %s: %w", packDir, err)
	}
	data, err := json.MarshalIndent(st, "", "  ")
	if err != nil {
		return fmt.Errorf("dbsync: marshal sync state: %w", err)
	}
	return writeAtomic(packDir, filepath.Join(packDir, SyncStateFilename), append(data, '\n'), 0o644)
}

func writeAtomic(dir, targetPath string, data []byte, perm os.FileMode) error {
	tmp, err := os.CreateTemp(dir, ".dbsync-*.tmp")
	if err != nil {
		return fmt.Errorf("dbsync: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(perm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("dbsync: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("dbsync: write temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("dbsync: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, targetPath); err != nil {
		return fmt.Errorf("dbsync: rename %s: %w", targetPath, err)
	}
	cleanup = false
	return nil
}

func fileExists(path string) bool {
	info, err := os.Stat(path)
	return err == nil && !errors.Is(err, os.ErrNotExist) && !info.IsDir()
}
