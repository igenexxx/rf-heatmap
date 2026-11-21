package mcpserver

import (
	"encoding/json"
	"fmt"
	"strings"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/library"
	"rf-heatmap/internal/subfile"
)

const (
	defaultTrimFreqHz = 433_920_000
	defaultTrimGapUs  = 10000
)

type trimRawArgs struct {
	Pack            string  `json:"pack"`
	Name            string  `json:"name"`
	Pulses          []int32 `json:"pulses"`
	FreqHz          uint32  `json:"freq_hz"`
	AutoTrim        *bool   `json:"auto_trim"`
	StartIdx        *int    `json:"start_idx"`
	EndIdx          *int    `json:"end_idx"`
	Repeats         int     `json:"repeats"`
	InterFrameGapUs int32   `json:"inter_frame_gap_us"`
	SaveAs          string  `json:"save_as"`
}

func (s *Server) toolTrimRaw(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args trimRawArgs
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	raw, freqHz, preset, err := s.resolveTrimSource(args)
	if err != nil {
		return toolError(err.Error())
	}
	startIdx, endIdx := resolveTrimRange(raw, args)
	repeats := args.Repeats
	if repeats == 0 {
		repeats = 1
	}
	gapUs := args.InterFrameGapUs
	if gapUs == 0 {
		gapUs = defaultTrimGapUs
	}
	res, err := subfile.TrimAndRepeatRaw(raw, startIdx, endIdx, repeats, gapUs)
	if err != nil {
		return toolError(err.Error())
	}
	out := map[string]any{
		"original_count": res.OriginalCount,
		"trimmed_count":  res.TrimmedCount,
		"start_idx":      res.Bounds.Start,
		"end_idx":        res.Bounds.End,
		"repeats":        res.Repeats,
		"duration_ms":    res.DurationMs,
		"pulses":         res.Pulses,
	}
	if strings.TrimSpace(args.SaveAs) != "" {
		savedRef, err := s.saveTrimmedCapture(args.SaveAs, freqHz, preset, res.Pulses)
		if err != nil {
			return toolError(err.Error())
		}
		out["saved_name"] = savedRef.Name
		out["saved_path"] = savedRef.Path
	}
	return jsonResult(out)
}

func (s *Server) resolveTrimSource(args trimRawArgs) ([]int32, uint32, string, error) {
	if len(args.Pulses) > 0 {
		freq := args.FreqHz
		if freq == 0 {
			freq = defaultTrimFreqHz
		}
		if freq < minFreqHz || freq > maxFreqHz {
			return nil, 0, "", fmt.Errorf("freq_hz must be between %d and %d", minFreqHz, maxFreqHz)
		}
		return args.Pulses, freq, cc1101.GetPresetSpec(cc1101.PresetAM650).SubFileName, nil
	}
	if strings.TrimSpace(args.Name) == "" {
		return nil, 0, "", fmt.Errorf("either name or pulses is required")
	}
	packID := strings.TrimSpace(args.Pack)
	if packID == "" {
		packID = library.PackIDUser
	}
	entries, err := s.store.ListEntries(packID)
	if err != nil {
		return nil, 0, "", err
	}
	for _, ref := range entries {
		if ref.Name == args.Name {
			sf, err := library.LoadSubFile(ref)
			if err != nil {
				return nil, 0, "", err
			}
			if len(sf.RawData) == 0 {
				return nil, 0, "", fmt.Errorf("file %s does not contain RAW_Data", args.Name)
			}
			return sf.RawData, sf.FreqHz, sf.Preset, nil
		}
	}
	return nil, 0, "", fmt.Errorf("entry %q not found in pack %q", args.Name, packID)
}

func resolveTrimRange(raw []int32, args trimRawArgs) (int, int) {
	useAuto := args.StartIdx == nil && args.EndIdx == nil
	if args.AutoTrim != nil {
		useAuto = *args.AutoTrim
	}
	if useAuto {
		b := subfile.SuggestTrimBounds(raw)
		return b.Start, b.End
	}
	start := 0
	end := len(raw)
	if args.StartIdx != nil {
		start = *args.StartIdx
	}
	if args.EndIdx != nil {
		end = *args.EndIdx
	}
	return start, end
}

func (s *Server) saveTrimmedCapture(saveAs string, freqHz uint32, preset string, pulses []int32) (library.EntryRef, error) {
	if preset == "" {
		preset = cc1101.GetPresetSpec(cc1101.PresetAM650).SubFileName
	}
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File",
		Version:  1,
		FreqHz:   freqHz,
		Preset:   preset,
		Protocol: "RAW",
		RawData:  pulses,
	}
	return s.store.SaveUserSubFileAtomic(saveAs, sf)
}
