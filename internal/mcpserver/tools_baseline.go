package mcpserver

import (
	"encoding/json"
	"fmt"

	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/ui"
)

func (s *Server) dispatchBaselineTool(name string, rawArgs json.RawMessage) (*toolCallResult, *rpcError, bool) {
	switch name {
	case "rf_save_room_baseline":
		res, err := s.toolSaveRoomBaseline(rawArgs)
		return res, err, true
	case "rf_list_room_baselines":
		res, err := s.toolListRoomBaselines()
		return res, err, true
	case "rf_diff_room_baseline":
		res, err := s.toolDiffRoomBaseline(rawArgs)
		return res, err, true
	case "rf_delete_room_baseline":
		res, err := s.toolDeleteRoomBaseline(rawArgs)
		return res, err, true
	default:
		return nil, nil, false
	}
}

func (s *Server) toolSaveRoomBaseline(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Name   string      `json:"name"`
		Band   int         `json:"band"`
		Sweeps *int        `json:"sweeps"`
		Frames [][]float32 `json:"frames"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if args.Band < 0 || args.Band >= len(ui.DefaultBands) {
		return toolError(fmt.Sprintf("band %d out of range [0..%d]", args.Band, len(ui.DefaultBands)-1))
	}
	sweeps := defaultSweepCount
	if args.Sweeps != nil {
		if *args.Sweeps < 1 || *args.Sweeps > maxSweepCount {
			return toolError(fmt.Sprintf("sweeps %d out of range [1..%d]", *args.Sweeps, maxSweepCount))
		}
		sweeps = *args.Sweeps
	}

	band := ui.DefaultBands[args.Band]
	frames := args.Frames
	source := "inline"
	if len(frames) == 0 {
		var err error
		frames, source, err = s.captureBandFrames(band.FreqsHz, sweeps)
		if err != nil {
			return toolError("sweep capture failed: " + err.Error())
		}
	}

	startHz, stepHz := bandStartStep(band.FreqsHz)
	base, err := rfml.CaptureBaselineFromFrames(args.Name, band.Name, startHz, stepHz, frames)
	if err != nil {
		return toolError(err.Error())
	}
	path, err := rfml.SaveRoomBaselineAtomic(s.baselinesDir, base)
	if err != nil {
		return toolError("save baseline failed: " + err.Error())
	}
	return jsonResult(map[string]any{
		"saved":      true,
		"name":       base.Name,
		"band":       base.Band,
		"bins":       len(base.MeanDBm),
		"samples":    base.Samples,
		"start_hz":   base.StartHz,
		"step_hz":    base.StepHz,
		"source":     source,
		"path":       path,
		"created_at": base.CreatedAt,
	})
}

func (s *Server) captureBandFrames(freqs []uint32, sweeps int) ([][]float32, string, error) {
	s.radioMu.Lock()
	defer s.radioMu.Unlock()
	if s.radio != nil {
		defer func() { _ = s.radio.Idle() }()
	}
	source := "sim"
	if s.radio != nil {
		source = "hardware"
	}
	frames := make([][]float32, sweeps)
	for sw := range sweeps {
		row := make([]float32, len(freqs))
		for bi, freq := range freqs {
			rssi, err := s.sampleFreqLocked(freq, sw, bi, len(freqs))
			if err != nil {
				return nil, source, err
			}
			row[bi] = float32(rssi)
		}
		frames[sw] = row
	}
	return frames, source, nil
}

func bandStartStep(freqs []uint32) (uint32, uint32) {
	if len(freqs) == 0 {
		return 0, 0
	}
	if len(freqs) == 1 {
		return freqs[0], 0
	}
	return freqs[0], freqs[1] - freqs[0]
}

func (s *Server) toolListRoomBaselines() (*toolCallResult, *rpcError) {
	list, err := rfml.ListRoomBaselines(s.baselinesDir)
	if err != nil {
		return toolError("list baselines failed: " + err.Error())
	}
	items := make([]map[string]any, len(list))
	for i, b := range list {
		items[i] = map[string]any{
			"name":       b.Name,
			"band":       b.Band,
			"bins":       len(b.MeanDBm),
			"samples":    b.Samples,
			"start_hz":   b.StartHz,
			"step_hz":    b.StepHz,
			"created_at": b.CreatedAt,
		}
	}
	return jsonResult(map[string]any{
		"count":         len(items),
		"baselines_dir": s.baselinesDir,
		"baselines":     items,
	})
}

func (s *Server) toolDiffRoomBaseline(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Name          string    `json:"name"`
		LiveDBm       []float32 `json:"live_dbm"`
		OnlyAnomalies bool      `json:"only_anomalies"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	base, err := rfml.LoadRoomBaseline(s.baselinesDir, args.Name)
	if err != nil {
		return toolError("load baseline failed: " + err.Error())
	}

	live := args.LiveDBm
	source := "inline"
	if len(live) == 0 {
		bandIdx := findBandIndexByName(base.Band)
		frames, src, sweepErr := s.captureBandFrames(ui.DefaultBands[bandIdx].FreqsHz, 1)
		if sweepErr != nil {
			return toolError("live sweep failed: " + sweepErr.Error())
		}
		live = frames[0]
		source = src
	}

	diff := rfml.ComputeDiff(base, live, make([]rfml.DiffBin, len(base.MeanDBm)))
	return jsonResult(buildDiffSummary(base, source, diff, args.OnlyAnomalies))
}

func findBandIndexByName(name string) int {
	for i, b := range ui.DefaultBands {
		if b.Name == name {
			return i
		}
	}
	return 0
}

func buildDiffSummary(base *rfml.RoomBaseline, source string, diff []rfml.DiffBin, onlyAnomalies bool) map[string]any {
	newEmitters, elevated := 0, 0
	var maxDelta, maxZ float32
	outBins := make([]rfml.DiffBin, 0, len(diff))
	for i, b := range diff {
		if i == 0 || b.DeltaDB > maxDelta {
			maxDelta = b.DeltaDB
		}
		if i == 0 || b.ZScore > maxZ {
			maxZ = b.ZScore
		}
		switch b.Class {
		case rfml.DiffNewEmitter:
			newEmitters++
		case rfml.DiffElevated:
			elevated++
		}
		if !onlyAnomalies || b.Class != rfml.DiffNormal {
			outBins = append(outBins, b)
		}
	}
	return map[string]any{
		"name":         base.Name,
		"band":         base.Band,
		"source":       source,
		"total_bins":   len(diff),
		"new_emitters": newEmitters,
		"elevated":     elevated,
		"max_delta_db": round1(float64(maxDelta)),
		"max_z_score":  round1(float64(maxZ)),
		"bins":         outBins,
	}
}

func (s *Server) toolDeleteRoomBaseline(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Name    string `json:"name"`
		Confirm bool   `json:"confirm"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if !args.Confirm {
		return toolError("refusing to delete room baseline without confirm=true")
	}
	if err := rfml.DeleteRoomBaseline(s.baselinesDir, args.Name); err != nil {
		return toolError("delete baseline failed: " + err.Error())
	}
	return jsonResult(map[string]any{
		"deleted": true,
		"name":    args.Name,
	})
}
