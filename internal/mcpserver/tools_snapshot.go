package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

const (
	defaultSnapshotMaxSegments = 128
	maxSnapshotSegmentsLimit   = 512
)

type signalSnapshotArgs struct {
	Pack        string  `json:"pack"`
	Name        string  `json:"name"`
	Pulses      []int32 `json:"pulses"`
	FreqHz      uint32  `json:"freq_hz"`
	Preset      string  `json:"preset"`
	Bins        int     `json:"bins"`
	MaxSegments int     `json:"max_segments"`
}

func (s *Server) toolSignalSnapshot(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args signalSnapshotArgs
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	raw, freqHz, preset, protocol, source, err := s.resolveSnapshotSource(args)
	if err != nil {
		return toolError(err.Error())
	}

	snap := rfml.BuildSignalSnapshot(raw, freqHz, preset, args.Bins)
	maxSegs := args.MaxSegments
	if maxSegs <= 0 {
		maxSegs = defaultSnapshotMaxSegments
	}
	if maxSegs > maxSnapshotSegmentsLimit {
		maxSegs = maxSnapshotSegmentsLimit
	}
	preview := snap.Segments
	if len(preview) > maxSegs {
		preview = preview[:maxSegs]
	}

	return jsonResult(map[string]any{
		"source":                source,
		"protocol":              protocol,
		"freq_hz":               snap.FreqHz,
		"preset":                snap.Preset,
		"total_pulses":          snap.TotalPulses,
		"high_pulses":           snap.HighPulses,
		"low_pulses":            snap.LowPulses,
		"sync_gaps":             snap.SyncGaps,
		"frames_estimated":      snap.FramesEstimated,
		"total_duration_us":     snap.TotalDurationUs,
		"total_duration_ms":     snap.TotalDurationMs,
		"duty_cycle_pct":        snap.DutyCyclePct,
		"min_data_us":           snap.MinDataUs,
		"max_data_us":           snap.MaxDataUs,
		"median_data_us":        snap.MedianDataUs,
		"max_sync_us":           snap.MaxSyncUs,
		"hist_min_us":           snap.HistMinUs,
		"hist_max_us":           snap.HistMaxUs,
		"hist_peak_count":       snap.HistPeakCount,
		"short_bin_idx":         snap.ShortBinIdx,
		"long_bin_idx":          snap.LongBinIdx,
		"recommended_window_us": snap.RecommendedWindowUs(),
		"clusters":              snap.Clusters,
		"histogram":             snap.Histogram,
		"waveform_preview":      preview,
		"waveform_truncated":    len(snap.Segments) > len(preview),
	})
}

func (s *Server) resolveSnapshotSource(args signalSnapshotArgs) ([]int32, uint32, string, string, string, error) {
	if len(args.Pulses) > 0 {
		freq := args.FreqHz
		if freq == 0 {
			freq = defaultTrimFreqHz
		}
		if freq < minFreqHz || freq > maxFreqHz {
			return nil, 0, "", "", "", fmt.Errorf("freq_hz must be between %d and %d", minFreqHz, maxFreqHz)
		}
		preset := strings.TrimSpace(args.Preset)
		if preset == "" {
			preset = cc1101.GetPresetSpec(cc1101.PresetAM650).SubFileName
		}
		proto := "RAW"
		if dec, ok := subfile.DecodeOOK(args.Pulses); ok {
			proto = dec.Protocol
		}
		return args.Pulses, freq, preset, proto, "inline", nil
	}

	name := strings.TrimSpace(args.Name)
	if name == "" {
		return nil, 0, "", "", "", fmt.Errorf("either name or pulses is required")
	}
	if strings.ContainsAny(name, `/\`) || strings.Contains(name, "..") {
		return nil, 0, "", "", "", fmt.Errorf("invalid filename %q: path separators are not allowed", name)
	}
	packID := strings.TrimSpace(args.Pack)
	if packID == "" {
		packID = library.PackIDUser
	}
	entries, err := s.store.ListEntries(packID)
	if err != nil {
		return nil, 0, "", "", "", err
	}
	for _, ref := range entries {
		if ref.Name != name {
			continue
		}
		sf, err := library.LoadSubFile(ref)
		if err != nil {
			return nil, 0, "", "", "", err
		}
		pulses, err := subfile.EncodePulses(sf)
		if err != nil {
			return nil, 0, "", "", "", err
		}
		proto := sf.Protocol
		if proto == "" {
			proto = "RAW"
		}
		return pulses, sf.FreqHz, sf.Preset, proto, packID + "/" + ref.Name, nil
	}
	return nil, 0, "", "", "", fmt.Errorf("entry %q not found in pack %q", name, packID)
}
