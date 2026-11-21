package config

import (
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestDefaultPath(t *testing.T) {
	p := DefaultPath()
	if p == "" || !strings.HasSuffix(p, filepath.Join("rf-heatmap", "config.json")) {
		t.Fatalf("unexpected DefaultPath: %q", p)
	}
}

func TestLoadMissingFileReturnsDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "does-not-exist", "config.json")
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load(missing) returned error: %v", err)
	}
	want := DefaultSettings()
	if got != want {
		t.Fatalf("Load(missing) = %+v, want %+v", got, want)
	}
	if got.MCPEnabled {
		t.Fatal("MCPEnabled must default to false")
	}
}

func TestSaveAndLoadRoundTrip(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "nested-cfg")
	path := filepath.Join(dir, "config.json")

	want := Settings{
		MCPEnabled:      true,
		AutoSquelch:     false,
		AutoLock:        false,
		SquelchK:        4.5,
		ProbeIntervalMs: 1500,
	}
	if err := Save(path, want); err != nil {
		t.Fatalf("Save failed: %v", err)
	}

	// Verify directory and file permissions.
	dirInfo, err := os.Stat(dir)
	if err != nil {
		t.Fatalf("stat dir: %v", err)
	}
	if perm := dirInfo.Mode().Perm(); perm != 0o700 {
		t.Fatalf("dir perm = %o, want 700", perm)
	}
	fileInfo, err := os.Stat(path)
	if err != nil {
		t.Fatalf("stat file: %v", err)
	}
	if perm := fileInfo.Mode().Perm(); perm != 0o600 {
		t.Fatalf("file perm = %o, want 600", perm)
	}

	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load failed: %v", err)
	}
	if got != want {
		t.Fatalf("round-trip mismatch: got %+v, want %+v", got, want)
	}
}

func TestLoadCorruptedJSON(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp_enabled": true, bad json`), 0o600); err != nil {
		t.Fatalf("write corrupt file: %v", err)
	}
	got, err := Load(path)
	if err == nil {
		t.Fatal("expected error for corrupted JSON, got nil")
	}
	if got != DefaultSettings() {
		t.Fatalf("expected DefaultSettings on error, got %+v", got)
	}
}

func TestLoadPartialJSONPreservesDefaults(t *testing.T) {
	path := filepath.Join(t.TempDir(), "config.json")
	if err := os.WriteFile(path, []byte(`{"mcp_enabled": true}`), 0o600); err != nil {
		t.Fatalf("write partial file: %v", err)
	}
	got, err := Load(path)
	if err != nil {
		t.Fatalf("Load(partial) returned error: %v", err)
	}
	want := Settings{
		MCPEnabled:      true,
		AutoSquelch:     true,
		AutoLock:        true,
		SquelchK:        DefaultSquelchK,
		ProbeIntervalMs: DefaultProbeIntervalMs,
		TXPowerDBm:      DefaultTXPowerDBm,
	}
	if got != want {
		t.Fatalf("Load(partial) = %+v, want %+v", got, want)
	}
}

func TestTXPowerDBmClampingAndPersistence(t *testing.T) {
	tests := []struct {
		name  string
		input int
		want  int
	}{
		{"below_min", -100, -30},
		{"exact_neg30", -30, -30},
		{"mid_neg25", -25, -30},
		{"mid_neg14", -14, -10},
		{"exact_zero", 0, 0},
		{"mid_3", 3, 5},
		{"exact_10", 10, 10},
		{"above_max", 42, 10},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := DefaultSettings()
			s.TXPowerDBm = tc.input
			if got := s.Normalize().TXPowerDBm; got != tc.want {
				t.Fatalf("Normalize(%d).TXPowerDBm = %d, want %d", tc.input, got, tc.want)
			}
			path := filepath.Join(t.TempDir(), "config.json")
			if err := Save(path, s); err != nil {
				t.Fatalf("Save failed: %v", err)
			}
			loaded, err := Load(path)
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}
			if loaded.TXPowerDBm != tc.want {
				t.Fatalf("loaded TXPowerDBm = %d, want %d", loaded.TXPowerDBm, tc.want)
			}
		})
	}
}

func TestSquelchKClamping(t *testing.T) {
	tests := []struct {
		name  string
		input float64
		want  float64
	}{
		{name: "below_min", input: 0.5, want: MinSquelchK},
		{name: "above_max", input: 12.0, want: MaxSquelchK},
		{name: "zero_uses_default", input: 0, want: DefaultSquelchK},
		{name: "negative_uses_default", input: -4.0, want: DefaultSquelchK},
		{name: "nan_uses_default", input: math.NaN(), want: DefaultSquelchK},
		{name: "inf_uses_default", input: math.Inf(1), want: DefaultSquelchK},
		{name: "exact_min", input: MinSquelchK, want: MinSquelchK},
		{name: "exact_max", input: MaxSquelchK, want: MaxSquelchK},
		{name: "mid_valid", input: 4.0, want: 4.0},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			s := DefaultSettings()
			s.SquelchK = tc.input
			norm := s.Normalize()
			if norm.SquelchK != tc.want {
				t.Fatalf("Normalize(%v).SquelchK = %v, want %v", tc.input, norm.SquelchK, tc.want)
			}

			// Also verify Save normalizes before persisting.
			path := filepath.Join(t.TempDir(), "config.json")
			if err := Save(path, s); err != nil {
				t.Fatalf("Save failed: %v", err)
			}
			loaded, err := Load(path)
			if err != nil {
				t.Fatalf("Load failed: %v", err)
			}
			if loaded.SquelchK != tc.want {
				t.Fatalf("loaded SquelchK = %v, want %v", loaded.SquelchK, tc.want)
			}
		})
	}
}

func TestLoadUnreadableDirectoryReturnsError(t *testing.T) {
	// Passing a directory path instead of a file must return a non-ErrNotExist error.
	dir := t.TempDir()
	_, err := Load(dir)
	if err == nil {
		t.Fatal("expected error reading directory as config file")
	}
}

func TestSaveErrorsAndEmptyHomeFallback(t *testing.T) {
	// Empty HOME should fall back to /tmp/rf-heatmap/config.json.
	t.Setenv("HOME", "")
	if got := DefaultPath(); got != "/tmp/rf-heatmap/config.json" {
		t.Fatalf("DefaultPath with empty HOME = %q, want /tmp/rf-heatmap/config.json", got)
	}

	// Saving under a file path as parent directory must fail cleanly.
	blockingFile := filepath.Join(t.TempDir(), "not-a-dir")
	if err := os.WriteFile(blockingFile, []byte("x"), 0o600); err != nil {
		t.Fatalf("write blocking file: %v", err)
	}
	if err := Save(filepath.Join(blockingFile, "config.json"), DefaultSettings()); err == nil {
		t.Fatal("expected Save to fail when parent path is a regular file")
	}
}
