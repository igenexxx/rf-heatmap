package library

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"rf-heatmap/internal/subfile"
)

// SaveUserSubFileAtomic writes sf atomically (temp file + os.Rename, mode 0644) strictly inside
// the Store's UserDir (~/rf-heatmap/captures/), rejecting path traversal and never touching
// read-only external databases. Existing files are preserved via collision-safe suffixes.
func (s *Store) SaveUserSubFileAtomic(name string, sf *subfile.SubFile) (EntryRef, error) {
	if s.UserDir == "" {
		return EntryRef{}, errors.New("library: user directory is not configured")
	}
	if sf == nil {
		return EntryRef{}, subfile.ErrNilSubFile
	}
	cleanName, err := SanitizeSubName(name)
	if err != nil {
		return EntryRef{}, err
	}
	absBase, err := filepath.Abs(s.UserDir)
	if err != nil {
		return EntryRef{}, fmt.Errorf("library: resolve user dir: %w", err)
	}
	if err := os.MkdirAll(absBase, 0o755); err != nil {
		return EntryRef{}, fmt.Errorf("library: mkdir user dir: %w", err)
	}
	dstName, dstPath := resolveUniqueUserPath(absBase, cleanName)
	rel, err := filepath.Rel(absBase, dstPath)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return EntryRef{}, ErrInvalidPath
	}
	if err := writeAtomicFile(absBase, dstPath, []byte(sf.Format())); err != nil {
		return EntryRef{}, fmt.Errorf("library: atomic write %s: %w", dstName, err)
	}
	return EntryRef{
		Name:     dstName,
		Path:     dstPath,
		PackID:   PackIDUser,
		Source:   SourceUser,
		ReadOnly: false,
		File:     sf,
	}, nil
}

func writeAtomicFile(dir, dstPath string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".sub-*.tmp")
	if err != nil {
		return err
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Chmod(0o644); err != nil {
		_ = tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if err := os.Rename(tmpPath, dstPath); err != nil {
		return err
	}
	cleanup = false
	return nil
}
