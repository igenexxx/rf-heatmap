package mcpserver

import (
	"encoding/json"
	"fmt"

	"rf-heatmap/internal/sensors"
)

const (
	defaultSensorFreqHz = 433_920_000
	defaultSensorPreset = "GFSK99"
	maxSensorMCPPulses  = 65536
)

func (s *Server) toolDecodeSensor(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Pulses    []int32 `json:"pulses"`
		FreqHz    uint32  `json:"freq_hz"`
		Preset    string  `json:"preset"`
		SimSample bool    `json:"sim_sample"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	pulses := args.Pulses
	freqHz := args.FreqHz
	preset := args.Preset
	source := "pulses"

	if len(pulses) == 0 {
		if !args.SimSample {
			return toolError("pulses array is required unless sim_sample=true")
		}
		pulses, freqHz, preset = sensors.SimFrame(0)
		source = "sim"
	}
	if len(pulses) > maxSensorMCPPulses {
		return toolError(fmt.Sprintf("pulses exceeds maximum %d samples", maxSensorMCPPulses))
	}
	if freqHz == 0 {
		freqHz = defaultSensorFreqHz
	}
	if freqHz < minFreqHz || freqHz > maxFreqHz {
		return toolError(fmt.Sprintf("freq_hz must be between %d and %d", minFreqHz, maxFreqHz))
	}
	if preset == "" {
		preset = defaultSensorPreset
	}
	reading, ok := sensors.Decode(pulses, freqHz, preset)
	if !ok {
		return jsonResult(map[string]any{
			"decoded": false,
			"source":  source,
		})
	}
	return jsonResult(map[string]any{
		"decoded": true,
		"source":  source,
		"reading": reading,
	})
}
