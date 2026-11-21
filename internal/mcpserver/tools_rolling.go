package mcpserver

import (
	"encoding/json"
	"fmt"

	"rf-heatmap/internal/subfile"
)

const (
	maxRollingFrames     = 8
	maxRollingFramePulse = 65536
	defaultRollingFreqHz = 433_920_000
)

func (s *Server) toolAnalyzeRollingCode(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		FreqHz uint32    `json:"freq_hz"`
		Frames [][]int32 `json:"frames"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if len(args.Frames) == 0 || len(args.Frames) > maxRollingFrames {
		return toolError(fmt.Sprintf("frames must contain between 1 and %d captures", maxRollingFrames))
	}
	freqHz := args.FreqHz
	if freqHz == 0 {
		freqHz = defaultRollingFreqHz
	}
	if freqHz < minFreqHz || freqHz > maxFreqHz {
		return toolError(fmt.Sprintf("freq_hz must be between %d and %d", minFreqHz, maxFreqHz))
	}
	for i, f := range args.Frames {
		if len(f) == 0 || len(f) > maxRollingFramePulse {
			return toolError(fmt.Sprintf("frame %d length must be between 1 and %d pulses", i, maxRollingFramePulse))
		}
	}
	rep := subfile.AnalyzeRollingCode(freqHz, args.Frames)
	return jsonResult(rep)
}
