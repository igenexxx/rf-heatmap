package mcpserver

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/subfile"
)

type savedDirSource struct {
	dir    string
	source string
}

type savedItem struct {
	Name       string `json:"name"`
	Path       string `json:"path"`
	Source     string `json:"source"`
	FreqHz     uint32 `json:"freq_hz"`
	Preset     string `json:"preset"`
	Protocol   string `json:"protocol"`
	Bits       int    `json:"bits,omitempty"`
	Key        string `json:"key,omitempty"`
	TE         int    `json:"te,omitempty"`
	RawSamples int    `json:"raw_samples,omitempty"`
}

func (s *Server) savedSearchDirs() []savedDirSource {
	var dirs []savedDirSource
	if s.capturesDir != "" {
		dirs = append(dirs, savedDirSource{dir: s.capturesDir, source: "captures"})
	}
	for _, d := range s.extraDirs {
		if d != "" && d != s.capturesDir {
			dirs = append(dirs, savedDirSource{dir: d, source: "subg"})
		}
	}
	return dirs
}

func (s *Server) toolListSaved() (*toolCallResult, *rpcError) {
	items := make([]savedItem, 0)
	for _, src := range s.savedSearchDirs() {
		items = append(items, scanSavedDir(src)...)
	}
	slices.SortFunc(items, func(a, b savedItem) int { return cmp.Compare(b.Name, a.Name) })
	return jsonResult(map[string]any{
		"count": len(items),
		"files": items,
	})
}

func scanSavedDir(src savedDirSource) []savedItem {
	entries, err := os.ReadDir(src.dir)
	if err != nil {
		return nil
	}
	items := make([]savedItem, 0, len(entries))
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sub") {
			continue
		}
		full := filepath.Join(src.dir, e.Name())
		raw, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		sf, err := subfile.Parse(string(raw))
		if err != nil {
			continue
		}
		items = append(items, savedItem{
			Name: e.Name(), Path: full, Source: src.source,
			FreqHz: sf.FreqHz, Preset: sf.Preset, Protocol: sf.Protocol,
			Bits: sf.Bit, Key: sf.Key, TE: sf.TE, RawSamples: len(sf.RawData),
		})
	}
	return items
}

func (s *Server) toolReadSaved(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Name string `json:"name"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	cleanName, err := sanitizeSubFilename(args.Name, false)
	if err != nil {
		return toolError(err.Error())
	}

	for _, src := range s.savedSearchDirs() {
		full := filepath.Join(src.dir, cleanName)
		raw, err := os.ReadFile(full)
		if err != nil {
			continue
		}
		sf, err := subfile.Parse(string(raw))
		if err != nil {
			return toolError(fmt.Sprintf("parse %s: %v", cleanName, err))
		}
		return jsonResult(map[string]any{
			"name": cleanName, "path": full, "source": src.source,
			"filetype": sf.FileType, "version": sf.Version,
			"freq_hz": sf.FreqHz, "preset": sf.Preset, "protocol": sf.Protocol,
			"bit": sf.Bit, "key": sf.Key, "te": sf.TE, "raw_data": sf.RawData,
		})
	}
	return toolError(fmt.Sprintf("saved capture %q not found", cleanName))
}

type saveCaptureArgs struct {
	Name     string  `json:"name"`
	FreqHz   uint32  `json:"freq_hz"`
	Preset   string  `json:"preset"`
	Protocol string  `json:"protocol"`
	KeyHex   string  `json:"key_hex"`
	Bits     int     `json:"bits"`
	TE       int     `json:"te"`
	RawData  []int32 `json:"raw_data"`
}

func (s *Server) toolSaveCapture(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args saveCaptureArgs
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	cleanName, err := s.validateSaveArgs(args)
	if err != nil {
		return toolError(err.Error())
	}
	sf := buildSubFileFromSaveArgs(args)
	if err := os.MkdirAll(s.capturesDir, 0o755); err != nil {
		return toolError(fmt.Sprintf("mkdir captures: %v", err))
	}
	full := filepath.Join(s.capturesDir, cleanName)
	content := sf.Format()
	if err := os.WriteFile(full, []byte(content), 0o644); err != nil {
		return toolError(fmt.Sprintf("write %s: %v", cleanName, err))
	}
	return jsonResult(map[string]any{
		"saved": true, "name": cleanName, "path": full, "bytes": len(content),
	})
}

func (s *Server) validateSaveArgs(args saveCaptureArgs) (string, error) {
	cleanName, err := sanitizeSubFilename(args.Name, true)
	if err != nil {
		return "", err
	}
	if args.FreqHz < minFreqHz || args.FreqHz > maxFreqHz {
		return "", fmt.Errorf("freq_hz %d out of range [%d..%d]", args.FreqHz, minFreqHz, maxFreqHz)
	}
	if args.KeyHex == "" && len(args.RawData) == 0 {
		return "", errors.New("either key_hex or raw_data must be provided")
	}
	if s.capturesDir == "" {
		return "", errors.New("captures directory is not configured")
	}
	return cleanName, nil
}

func buildSubFileFromSaveArgs(args saveCaptureArgs) *subfile.SubFile {
	presetName := cc1101.GetPresetSpec(cc1101.PresetAM650).SubFileName
	if args.Preset != "" {
		if p, ok := cc1101.PresetFromSubName(args.Preset); ok {
			presetName = cc1101.GetPresetSpec(p).SubFileName
		} else {
			presetName = args.Preset
		}
	}
	sf := &subfile.SubFile{
		Version: 1, FreqHz: args.FreqHz, Preset: presetName,
		Protocol: args.Protocol, Bit: args.Bits, Key: args.KeyHex, TE: args.TE, RawData: args.RawData,
	}
	if len(args.RawData) > 0 && args.KeyHex == "" {
		sf.FileType = "Flipper SubGhz RAW File"
		if sf.Protocol == "" {
			sf.Protocol = "RAW"
		}
	} else {
		sf.FileType = "Flipper SubGhz Key File"
	}
	return sf
}

func (s *Server) toolDeleteSaved(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if !args.Confirm {
		return toolError("deletion requires confirm=true")
	}
	cleanName, err := sanitizeSubFilename(args.Name, false)
	if err != nil {
		return toolError(err.Error())
	}
	target, err := s.resolveSafeCapturePath(cleanName)
	if err != nil {
		return toolError(err.Error())
	}
	if err := os.Remove(target); err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return toolError(fmt.Sprintf("capture %q not found in captures directory", cleanName))
		}
		return toolError(fmt.Sprintf("delete %s: %v", cleanName, err))
	}
	return jsonResult(map[string]any{"deleted": true, "name": cleanName, "path": target})
}

func (s *Server) resolveSafeCapturePath(cleanName string) (string, error) {
	if s.capturesDir == "" {
		return "", errors.New("captures directory is not configured")
	}
	absBase, err := filepath.Abs(s.capturesDir)
	if err != nil {
		return "", fmt.Errorf("resolve captures dir: %w", err)
	}
	target := filepath.Join(absBase, cleanName)
	rel, err := filepath.Rel(absBase, target)
	if err != nil || strings.HasPrefix(rel, "..") || filepath.IsAbs(rel) {
		return "", errors.New("invalid path outside captures directory")
	}
	return target, nil
}

func sanitizeSubFilename(name string, appendExt bool) (string, error) {
	name = strings.TrimSpace(name)
	if name == "" {
		return "", errors.New("filename must not be empty")
	}
	if strings.Contains(name, "..") || strings.ContainsAny(name, `/\`) || strings.ContainsRune(name, 0) {
		return "", fmt.Errorf("invalid filename %q: path traversal and separators are forbidden", name)
	}
	if appendExt && !strings.HasSuffix(strings.ToLower(name), ".sub") {
		name += ".sub"
	}
	if !strings.HasSuffix(strings.ToLower(name), ".sub") {
		return "", fmt.Errorf("invalid filename %q: must have .sub extension", name)
	}
	if name == ".sub" {
		return "", errors.New("filename must not be empty")
	}
	return name, nil
}
