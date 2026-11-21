package library

import (
	"cmp"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"rf-heatmap/internal/subfile"
)

// SourceKind distinguishes read-write user captures from read-only external databases.
type SourceKind string

const (
	// SourceUser is the app-owned read-write captures directory (~/rf-heatmap/captures).
	SourceUser SourceKind = "user"
	// SourceExternal is a read-only external .sub database pack (~/rf-heatmap/databases/<pack> or ~/subg/files).
	SourceExternal SourceKind = "external"

	// PackIDUser is the canonical pack identifier for the User Library.
	PackIDUser = "user"
	// PackIDSubg is the canonical pack identifier for legacy ~/subg/files.
	PackIDSubg = "ext:subg"
	// PackIDDBRoot is the pack identifier for .sub files placed directly in ~/rf-heatmap/databases/.
	PackIDDBRoot = "db:root"
)

var (
	// ErrReadOnlySource is returned when a mutation targets a read-only external database.
	ErrReadOnlySource = errors.New("library: external database is read-only")
	// ErrInvalidPath is returned on path traversal or out-of-sandbox file access.
	ErrInvalidPath = errors.New("library: invalid path outside sandbox")
	// ErrPackNotFound is returned when a requested pack ID does not exist.
	ErrPackNotFound = errors.New("library: pack not found")
	// ErrEntryNotFound is returned when a requested .sub entry does not exist.
	ErrEntryNotFound = errors.New("library: entry not found")
)

// PackInfo describes a signal catalog (User Library or external DatabasePack).
type PackInfo struct {
	ID       string     `json:"pack_id"`
	Name     string     `json:"name"`
	Source   SourceKind `json:"source"`
	Dir      string     `json:"dir"`
	ReadOnly bool       `json:"read_only"`
	Count    int        `json:"count"`
}

// EntryRef is a lightweight reference to a .sub file; File is populated lazily via LoadSubFile.
type EntryRef struct {
	Name     string
	Path     string
	PackID   string
	Source   SourceKind
	ReadOnly bool
	File     *subfile.SubFile
}

// Store manages separated read-write UserLibrary and read-only External DatabasePacks.
type Store struct {
	UserDir      string
	DatabasesDir string
	ExtraDirs    []string
}

// NewStore creates a Store with explicit root directories.
func NewStore(userDir, databasesDir string, extraDirs []string) *Store {
	return &Store{
		UserDir:      userDir,
		DatabasesDir: databasesDir,
		ExtraDirs:    extraDirs,
	}
}

// DefaultDatabasesDir returns ~/rf-heatmap/databases (or a /tmp fallback).
func DefaultDatabasesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/tmp/rf-heatmap/databases"
	}
	return filepath.Join(home, "rf-heatmap", "databases")
}

// ListPacks returns external DatabasePacks, built-in GeneratorCatalogs, and the User Library.
func (s *Store) ListPacks() []PackInfo {
	var external []PackInfo
	if s.DatabasesDir != "" {
		external = append(external, scanDatabasesRoot(s.DatabasesDir)...)
	}
	for _, extra := range s.ExtraDirs {
		if extra == "" || extra == s.UserDir || extra == s.DatabasesDir {
			continue
		}
		if cnt := countSubFiles(extra); cnt > 0 {
			external = append(external, PackInfo{
				ID:       PackIDSubg,
				Name:     "subg",
				Source:   SourceExternal,
				Dir:      extra,
				ReadOnly: true,
				Count:    cnt,
			})
		}
	}
	slices.SortFunc(external, func(a, b PackInfo) int { return cmp.Compare(a.ID, b.ID) })

	userPack := PackInfo{
		ID:       PackIDUser,
		Name:     "USER",
		Source:   SourceUser,
		Dir:      s.UserDir,
		ReadOnly: false,
		Count:    countSubFiles(s.UserDir),
	}
	packs := make([]PackInfo, 0, len(external)+len(builtInGenerators)+1)
	packs = append(packs, external...)
	packs = append(packs, BuiltInGeneratorPacks()...)
	packs = append(packs, userPack)
	return packs
}

// OpenCatalog returns an O(1) Catalog for disk packs or virtual generator packs.
func (s *Store) OpenCatalog(packID string) (Catalog, PackInfo, error) {
	if gen, ok := NewGeneratorCatalog(packID); ok {
		return gen, gen.Pack, nil
	}
	pack, err := s.FindPack(packID)
	if err != nil {
		return nil, PackInfo{}, err
	}
	return SliceCatalog(readPackEntries(pack)), pack, nil
}

func scanDatabasesRoot(root string) []PackInfo {
	entries, err := os.ReadDir(root)
	if err != nil {
		return nil
	}
	var packs []PackInfo
	rootCount := 0
	for _, e := range entries {
		if e.IsDir() {
			subDir := filepath.Join(root, e.Name())
			if cnt := countSubFiles(subDir); cnt > 0 {
				packs = append(packs, PackInfo{
					ID:       "db:" + e.Name(),
					Name:     e.Name(),
					Source:   SourceExternal,
					Dir:      subDir,
					ReadOnly: true,
					Count:    cnt,
				})
			}
		} else if isSubFilename(e.Name()) {
			rootCount++
		}
	}
	if rootCount > 0 {
		packs = append(packs, PackInfo{
			ID:       PackIDDBRoot,
			Name:     "databases",
			Source:   SourceExternal,
			Dir:      root,
			ReadOnly: true,
			Count:    rootCount,
		})
	}
	return packs
}

// FindPack looks up a pack by its canonical ID.
func (s *Store) FindPack(packID string) (PackInfo, error) {
	for _, p := range s.ListPacks() {
		if p.ID == packID {
			return p, nil
		}
	}
	return PackInfo{}, fmt.Errorf("%w: %q", ErrPackNotFound, packID)
}

// ListEntries returns lightweight EntryRefs for the requested pack without parsing .sub contents.
func (s *Store) ListEntries(packID string) ([]EntryRef, error) {
	pack, err := s.FindPack(packID)
	if err != nil {
		return nil, err
	}
	return readPackEntries(pack), nil
}

// ListUserEntries returns parsed EntryRefs for the User Library sorted newest-name first.
func (s *Store) ListUserEntries() []EntryRef {
	pack := PackInfo{
		ID:       PackIDUser,
		Name:     "USER",
		Source:   SourceUser,
		Dir:      s.UserDir,
		ReadOnly: false,
	}
	raw := readPackEntries(pack)
	parsed := make([]EntryRef, 0, len(raw))
	for _, ref := range raw {
		if sf, err := LoadSubFile(ref); err == nil {
			ref.File = sf
			parsed = append(parsed, ref)
		}
	}
	slices.SortFunc(parsed, func(a, b EntryRef) int { return cmp.Compare(b.Name, a.Name) })
	return parsed
}

func readPackEntries(pack PackInfo) []EntryRef {
	if pack.Dir == "" {
		return nil
	}
	dirEntries, err := os.ReadDir(pack.Dir)
	if err != nil {
		return nil
	}
	out := make([]EntryRef, 0, len(dirEntries))
	for _, e := range dirEntries {
		if e.IsDir() || !isSubFilename(e.Name()) {
			continue
		}
		out = append(out, EntryRef{
			Name:     e.Name(),
			Path:     filepath.Join(pack.Dir, e.Name()),
			PackID:   pack.ID,
			Source:   pack.Source,
			ReadOnly: pack.ReadOnly,
		})
	}
	slices.SortFunc(out, func(a, b EntryRef) int { return cmp.Compare(a.Name, b.Name) })
	return out
}

// LoadSubFile parses the .sub file referenced by ref (returning cached File if already loaded).
func LoadSubFile(ref EntryRef) (*subfile.SubFile, error) {
	if ref.File != nil {
		return ref.File, nil
	}
	raw, err := os.ReadFile(ref.Path)
	if err != nil {
		return nil, fmt.Errorf("library: read %s: %w", ref.Name, err)
	}
	sf, err := subfile.Parse(string(raw))
	if err != nil {
		return nil, fmt.Errorf("library: parse %s: %w", ref.Name, err)
	}
	return sf, nil
}

// PromoteToUser copies a .sub entry from an external DatabasePack or virtual GeneratorCatalog
// into the User Library. If a file with the same name exists, a collision-safe suffix is appended.
func (s *Store) PromoteToUser(ref EntryRef) (EntryRef, error) {
	if s.UserDir == "" {
		return EntryRef{}, errors.New("library: user directory is not configured")
	}
	cleanName, err := SanitizeSubName(ref.Name)
	if err != nil {
		return EntryRef{}, err
	}
	var raw []byte
	var sf *subfile.SubFile
	if ref.Path == "" && ref.File != nil {
		sf = ref.File
		raw = []byte(sf.Format())
	} else {
		raw, err = os.ReadFile(ref.Path)
		if err != nil {
			return EntryRef{}, fmt.Errorf("library: read source %s: %w", ref.Name, err)
		}
		sf, err = subfile.Parse(string(raw))
		if err != nil {
			return EntryRef{}, fmt.Errorf("library: invalid .sub %s: %w", ref.Name, err)
		}
	}
	if err := os.MkdirAll(s.UserDir, 0o755); err != nil {
		return EntryRef{}, fmt.Errorf("library: mkdir user dir: %w", err)
	}
	dstName, dstPath := resolveUniqueUserPath(s.UserDir, cleanName)
	if err := os.WriteFile(dstPath, raw, 0o644); err != nil {
		return EntryRef{}, fmt.Errorf("library: write %s: %w", dstName, err)
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

// DeleteUserEntry removes a .sub file strictly inside UserDir, rejecting external DB entries.
func (s *Store) DeleteUserEntry(ref EntryRef) error {
	if ref.ReadOnly || ref.Source != SourceUser {
		return ErrReadOnlySource
	}
	if s.UserDir == "" {
		return errors.New("library: user directory is not configured")
	}
	cleanName, err := SanitizeSubName(ref.Name)
	if err != nil {
		return err
	}
	absBase, err := filepath.Abs(s.UserDir)
	if err != nil {
		return fmt.Errorf("library: resolve user dir: %w", err)
	}
	target := filepath.Join(absBase, cleanName)
	rel, err := filepath.Rel(absBase, target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ErrInvalidPath
	}
	if err := os.Remove(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrEntryNotFound, cleanName)
		}
		return fmt.Errorf("library: delete %s: %w", cleanName, err)
	}
	return nil
}

// DeleteDatabaseEntry removes a .sub file strictly inside DatabasesDir (~/rf-heatmap/databases/<pack>/).
// Legacy ~/subg/files (ext:subg), virtual generators (gen:*), and UserDir are rejected.
func (s *Store) DeleteDatabaseEntry(ref EntryRef) error {
	if ref.Source != SourceExternal || ref.PackID == PackIDSubg || s.DatabasesDir == "" {
		return ErrReadOnlySource
	}
	if _, err := SanitizeSubName(ref.Name); err != nil {
		return err
	}
	absBase, err := filepath.Abs(s.DatabasesDir)
	if err != nil {
		return fmt.Errorf("library: resolve databases dir: %w", err)
	}
	absTarget, err := filepath.Abs(ref.Path)
	if err != nil {
		return fmt.Errorf("library: resolve entry path: %w", err)
	}
	rel, err := filepath.Rel(absBase, absTarget)
	if err != nil || rel == "." || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return ErrInvalidPath
	}
	if err := os.Remove(absTarget); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return fmt.Errorf("%w: %s", ErrEntryNotFound, ref.Name)
		}
		return fmt.Errorf("library: delete %s: %w", ref.Name, err)
	}
	return nil
}

// SanitizeSubName rejects path traversal, separators, and non-.sub filenames.
func SanitizeSubName(name string) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" || name == ".sub" {
		return "", fmt.Errorf("%w: empty filename", ErrInvalidPath)
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("%w: %q", ErrInvalidPath, name)
	}
	if !isSubFilename(name) {
		return "", fmt.Errorf("%w: must end with .sub", ErrInvalidPath)
	}
	return name, nil
}

func resolveUniqueUserPath(userDir, name string) (string, string) {
	base := strings.TrimSuffix(name, filepath.Ext(name))
	ext := filepath.Ext(name)
	candidate := name
	full := filepath.Join(userDir, candidate)
	for i := 2; i < 1000; i++ {
		if _, err := os.Stat(full); errors.Is(err, os.ErrNotExist) {
			return candidate, full
		}
		candidate = fmt.Sprintf("%s_%d%s", base, i, ext)
		full = filepath.Join(userDir, candidate)
	}
	return candidate, full
}

func countSubFiles(dir string) int {
	if dir == "" {
		return 0
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		return 0
	}
	n := 0
	for _, e := range entries {
		if !e.IsDir() && isSubFilename(e.Name()) {
			n++
		}
	}
	return n
}

func isSubFilename(name string) bool {
	return strings.HasSuffix(strings.ToLower(name), ".sub")
}
