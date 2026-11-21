package dbsync

import (
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"strings"
)

// SourceType identifies the remote transport protocol for a DatabaseSource.
type SourceType string

const (
	// SourceGitHub syncs .sub files from a GitHub repository/subfolder via Git Trees API.
	SourceGitHub SourceType = "github"
	// SourceManifest syncs .sub files listed in a remote HTTP(S) JSON manifest.
	SourceManifest SourceType = "manifest"
)

var (
	// ErrInvalidSource is returned when a DatabaseSource configuration fails validation.
	ErrInvalidSource = errors.New("dbsync: invalid source")
	// ErrSourceNotFound is returned when removing or syncing an unknown source ID.
	ErrSourceNotFound = errors.New("dbsync: source not found")
)

// DatabaseSource defines a remote .sub catalog synced into ~/rf-heatmap/databases/<ID>/.
type DatabaseSource struct {
	ID      string     `json:"id"`
	Type    SourceType `json:"type"`
	Owner   string     `json:"owner,omitempty"`
	Repo    string     `json:"repo,omitempty"`
	Branch  string     `json:"branch,omitempty"`
	Subpath string     `json:"subpath,omitempty"`
	URL     string     `json:"url,omitempty"`
	Enabled bool       `json:"enabled"`
	Prune   bool       `json:"prune,omitempty"`
}

// DefaultSourcesPath returns $HOME/.config/rf-heatmap/sources.json (or a /tmp fallback).
func DefaultSourcesPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/tmp/rf-heatmap/sources.json"
	}
	return filepath.Join(home, ".config", "rf-heatmap", "sources.json")
}

// NormalizeAndValidate trims fields, applies defaults, and enforces strict ID/URL invariants.
func (s DatabaseSource) NormalizeAndValidate() (DatabaseSource, error) {
	s.ID = strings.ToLower(strings.TrimSpace(s.ID))
	if err := validateSourceID(s.ID); err != nil {
		return s, err
	}
	switch s.Type {
	case SourceGitHub:
		return normalizeGitHubSource(s)
	case SourceManifest:
		return normalizeManifestSource(s)
	default:
		return s, fmt.Errorf("%w: unsupported type %q (use github or manifest)", ErrInvalidSource, s.Type)
	}
}

func validateSourceID(id string) error {
	if len(id) == 0 || len(id) > 32 {
		return fmt.Errorf("%w: id length must be 1..32", ErrInvalidSource)
	}
	if id == "user" || id == "root" {
		return fmt.Errorf("%w: reserved id %q", ErrInvalidSource, id)
	}
	for i, r := range id {
		isAlphaNum := (r >= 'a' && r <= 'z') || (r >= '0' && r <= '9')
		if i == 0 && !isAlphaNum {
			return fmt.Errorf("%w: id %q must start with a-z or 0-9", ErrInvalidSource, id)
		}
		if !isAlphaNum && r != '-' && r != '_' {
			return fmt.Errorf("%w: id %q contains forbidden character %q", ErrInvalidSource, id, r)
		}
	}
	return nil
}

func normalizeGitHubSource(s DatabaseSource) (DatabaseSource, error) {
	s.Owner = strings.TrimSpace(s.Owner)
	s.Repo = strings.TrimSpace(s.Repo)
	s.Branch = strings.TrimSpace(s.Branch)
	s.Subpath = strings.Trim(strings.TrimSpace(s.Subpath), "/")
	if s.Owner == "" || s.Repo == "" {
		return s, fmt.Errorf("%w: github source requires owner and repo", ErrInvalidSource)
	}
	if strings.Contains(s.Owner, "/") || strings.Contains(s.Repo, "/") || strings.Contains(s.Subpath, "..") {
		return s, fmt.Errorf("%w: invalid github owner/repo/subpath", ErrInvalidSource)
	}
	if s.Branch == "" {
		s.Branch = "main"
	}
	return s, nil
}

func normalizeManifestSource(s DatabaseSource) (DatabaseSource, error) {
	s.URL = strings.TrimSpace(s.URL)
	if err := validateRemoteURL(s.URL); err != nil {
		return s, err
	}
	return s, nil
}

func validateRemoteURL(raw string) error {
	u, err := url.Parse(raw)
	if err != nil || u.Host == "" {
		return fmt.Errorf("%w: invalid url %q", ErrInvalidSource, raw)
	}
	switch u.Scheme {
	case "https":
		return nil
	case "http":
		host := u.Hostname()
		if host == "127.0.0.1" || host == "localhost" || host == "::1" {
			return nil
		}
		return fmt.Errorf("%w: remote url must use https:// (got %q)", ErrInvalidSource, raw)
	default:
		return fmt.Errorf("%w: unsupported url scheme %q", ErrInvalidSource, u.Scheme)
	}
}

// ParseGitHubURL parses https://github.com/<owner>/<repo>[/tree/<branch>[/<subpath...>]].
func ParseGitHubURL(rawURL string) (owner, repo, branch, subpath string, err error) {
	u, err := url.Parse(strings.TrimSpace(rawURL))
	if err != nil || u.Scheme != "https" || !strings.EqualFold(u.Hostname(), "github.com") {
		return "", "", "", "", fmt.Errorf("%w: expected https://github.com/<owner>/<repo>", ErrInvalidSource)
	}
	parts := strings.Split(strings.Trim(u.Path, "/"), "/")
	if len(parts) < 2 || parts[0] == "" || parts[1] == "" {
		return "", "", "", "", fmt.Errorf("%w: incomplete github url %q", ErrInvalidSource, rawURL)
	}
	owner = parts[0]
	repo = strings.TrimSuffix(parts[1], ".git")
	branch = "main"
	if len(parts) >= 4 && parts[2] == "tree" {
		branch = parts[3]
		if len(parts) > 4 {
			subpath = strings.Join(parts[4:], "/")
		}
	}
	return owner, repo, branch, subpath, nil
}

// LoadSources reads the source registry JSON file, returning an empty slice if it does not exist.
func LoadSources(path string) ([]DatabaseSource, error) {
	if path == "" {
		path = DefaultSourcesPath()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return nil, nil
		}
		return nil, fmt.Errorf("dbsync: read sources %s: %w", path, err)
	}
	var list []DatabaseSource
	if err := json.Unmarshal(raw, &list); err != nil {
		return nil, fmt.Errorf("dbsync: parse sources %s: %w", path, err)
	}
	return list, nil
}

// SaveSources atomically persists the source registry with 0700 dir and 0600 file permissions.
func SaveSources(path string, sources []DatabaseSource) error {
	if path == "" {
		path = DefaultSourcesPath()
	}
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("dbsync: mkdir %s: %w", dir, err)
	}
	if sources == nil {
		sources = []DatabaseSource{}
	}
	data, err := json.MarshalIndent(sources, "", "  ")
	if err != nil {
		return fmt.Errorf("dbsync: marshal sources: %w", err)
	}
	return writeAtomic(dir, path, append(data, '\n'), 0o600)
}

// UpsertSource validates and inserts or replaces a source by ID in the registry file.
func UpsertSource(path string, src DatabaseSource) (DatabaseSource, error) {
	norm, err := src.NormalizeAndValidate()
	if err != nil {
		return DatabaseSource{}, err
	}
	list, err := LoadSources(path)
	if err != nil {
		return DatabaseSource{}, err
	}
	replaced := false
	for i := range list {
		if list[i].ID == norm.ID {
			list[i] = norm
			replaced = true
			break
		}
	}
	if !replaced {
		list = append(list, norm)
	}
	if err := SaveSources(path, list); err != nil {
		return DatabaseSource{}, err
	}
	return norm, nil
}

// RemoveSource removes a source by ID from the registry file.
func RemoveSource(path, id string) error {
	list, err := LoadSources(path)
	if err != nil {
		return err
	}
	id = strings.ToLower(strings.TrimSpace(id))
	out := make([]DatabaseSource, 0, len(list))
	found := false
	for _, s := range list {
		if s.ID == id {
			found = true
			continue
		}
		out = append(out, s)
	}
	if !found {
		return fmt.Errorf("%w: %q", ErrSourceNotFound, id)
	}
	return SaveSources(path, out)
}
