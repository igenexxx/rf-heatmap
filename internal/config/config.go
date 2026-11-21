package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
)

const (
	// MinSquelchK is the lower bound for the Smart Squelch sigma multiplier.
	MinSquelchK = 2.0
	// MaxSquelchK is the upper bound for the Smart Squelch sigma multiplier.
	MaxSquelchK = 6.0
	// DefaultSquelchK is the default Smart Squelch sigma multiplier.
	DefaultSquelchK = 3.5

	// MinProbeIntervalMs is the shortest Auto-Probe delay between transmissions.
	MinProbeIntervalMs = 50
	// MaxProbeIntervalMs is the longest Auto-Probe delay between transmissions.
	MaxProbeIntervalMs = 5000
	// DefaultProbeIntervalMs is the default Auto-Probe delay between transmissions.
	DefaultProbeIntervalMs = 1000
)

// Settings holds persisted user preferences for rf-heatmap.
// Opt-in services such as the MCP server are disabled by default.
type Settings struct {
	MCPEnabled      bool    `json:"mcp_enabled"`
	AutoSquelch     bool    `json:"auto_squelch"`
	AutoLock        bool    `json:"auto_lock"`
	SquelchK        float64 `json:"squelch_k"`
	ProbeIntervalMs int     `json:"probe_interval_ms"`
}

// DefaultSettings returns the safe default application settings.
func DefaultSettings() Settings {
	return Settings{
		MCPEnabled:      false,
		AutoSquelch:     true,
		AutoLock:        true,
		SquelchK:        DefaultSquelchK,
		ProbeIntervalMs: DefaultProbeIntervalMs,
	}
}

// Normalize returns a copy of s with out-of-range numeric fields clamped.
func (s Settings) Normalize() Settings {
	if math.IsNaN(s.SquelchK) || math.IsInf(s.SquelchK, 0) || s.SquelchK <= 0 {
		s.SquelchK = DefaultSquelchK
	}
	if s.SquelchK < MinSquelchK {
		s.SquelchK = MinSquelchK
	}
	if s.SquelchK > MaxSquelchK {
		s.SquelchK = MaxSquelchK
	}
	if s.ProbeIntervalMs <= 0 {
		s.ProbeIntervalMs = DefaultProbeIntervalMs
	} else if s.ProbeIntervalMs < MinProbeIntervalMs {
		s.ProbeIntervalMs = MinProbeIntervalMs
	} else if s.ProbeIntervalMs > MaxProbeIntervalMs {
		s.ProbeIntervalMs = MaxProbeIntervalMs
	}
	return s
}

// DefaultPath returns $HOME/.config/rf-heatmap/config.json (or a /tmp fallback).
func DefaultPath() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/tmp/rf-heatmap/config.json"
	}
	return filepath.Join(home, ".config", "rf-heatmap", "config.json")
}

// Load reads settings from path, returning DefaultSettings if the file does not exist.
func Load(path string) (Settings, error) {
	if path == "" {
		path = DefaultPath()
	}
	raw, err := os.ReadFile(path)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return DefaultSettings(), nil
		}
		return DefaultSettings(), fmt.Errorf("config: read %s: %w", path, err)
	}

	s := DefaultSettings()
	if err := json.Unmarshal(raw, &s); err != nil {
		return DefaultSettings(), fmt.Errorf("config: parse %s: %w", path, err)
	}
	return s.Normalize(), nil
}

// Save atomically persists settings to path with 0700 parent directory and 0600 file permissions.
func Save(path string, s Settings) error {
	if path == "" {
		path = DefaultPath()
	}
	s = s.Normalize()
	dir := filepath.Dir(path)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return fmt.Errorf("config: mkdir %s: %w", dir, err)
	}
	data, err := json.MarshalIndent(s, "", "  ")
	if err != nil {
		return fmt.Errorf("config: marshal: %w", err)
	}
	return writeAtomicFile(dir, path, append(data, '\n'))
}

func writeAtomicFile(dir, path string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".config-*.tmp")
	if err != nil {
		return fmt.Errorf("config: create temp: %w", err)
	}
	tmpPath := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpPath)
		}
	}()

	if err := tmp.Chmod(0o600); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: chmod temp: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: write temp: %w", err)
	}
	if err := tmp.Sync(); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("config: sync temp: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("config: close temp: %w", err)
	}
	if err := os.Rename(tmpPath, path); err != nil {
		return fmt.Errorf("config: rename %s: %w", path, err)
	}
	cleanup = false
	return nil
}
