package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

const (
	defaultPurgeMinConfidence = 0.85
	defaultPurgeAssumeSNRDB   = 8.0
)

type purgeNoiseArgs struct {
	DryRun        *bool    `json:"dry_run"`
	Confirm       bool     `json:"confirm"`
	MinConfidence *float64 `json:"min_confidence"`
}

type purgeCandidate struct {
	Name       string  `json:"name"`
	Confidence float64 `json:"confidence"`
	Deleted    bool    `json:"deleted"`
}

func (s *Server) toolPurgeNoise(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args purgeNoiseArgs
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	dryRun := true
	if args.DryRun != nil {
		dryRun = *args.DryRun
	}
	if !dryRun && !args.Confirm {
		return toolError("purging noise files requires confirm=true when dry_run=false")
	}
	minConf := defaultPurgeMinConfidence
	if args.MinConfidence != nil && *args.MinConfidence >= 0.5 && *args.MinConfidence <= 1.0 {
		minConf = *args.MinConfidence
	}
	if s.capturesDir == "" {
		return toolError("captures directory is not configured")
	}

	scanned, candidates, deleted := s.evaluateAndPurgeNoise(minConf, dryRun)
	return jsonResult(map[string]any{
		"dry_run":        dryRun,
		"min_confidence": minConf,
		"scanned":        scanned,
		"matched_count":  len(candidates),
		"deleted_count":  deleted,
		"files":          candidates,
	})
}

func (s *Server) evaluateAndPurgeNoise(minConf float64, dryRun bool) (int, []purgeCandidate, int) {
	entries, err := os.ReadDir(s.capturesDir)
	if err != nil {
		return 0, []purgeCandidate{}, 0
	}
	scanned, deleted := 0, 0
	candidates := make([]purgeCandidate, 0)
	for _, e := range entries {
		if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sub") {
			continue
		}
		full := filepath.Join(s.capturesDir, e.Name())
		sf, ok := readSubFileQuiet(full)
		if !ok || len(sf.RawData) == 0 {
			continue
		}
		scanned++
		feat, _ := rfml.ExtractFeatures(sf.RawData, sf.FreqHz, defaultPurgeAssumeSNRDB)
		pred := rfml.Classify(feat)
		if pred.Class != rfml.ClassNoise || float64(pred.Confidence) < minConf {
			continue
		}
		wasDeleted := !dryRun && os.Remove(full) == nil
		if wasDeleted {
			deleted++
		}
		candidates = append(candidates, purgeCandidate{
			Name:       e.Name(),
			Confidence: round1(float64(pred.Confidence)*100) / 100,
			Deleted:    wasDeleted,
		})
	}
	return scanned, candidates, deleted
}

func readSubFileQuiet(path string) (*subfile.SubFile, bool) {
	raw, err := os.ReadFile(path)
	if err != nil {
		return nil, false
	}
	sf, err := subfile.Parse(string(raw))
	if err != nil {
		return nil, false
	}
	return sf, true
}
