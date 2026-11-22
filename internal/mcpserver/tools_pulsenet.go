package mcpserver

import (
	"encoding/json"

	"rf-heatmap/internal/rfml"
)

func (s *Server) mlNeuralToolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "rf_model_info",
			Description: "Inspect active on-device Pure-Go INT8 .czml models (PulseNet-RF 28-class 1D-CNN + SpectrumNet-2D waterfall classifier) including version, SHA-256, source, and class labels.",
			InputSchema: map[string]any{"type": "object", "properties": map[string]any{}},
		},
		{
			Name:        "rf_classify_spectrum",
			Description: "Classify a 2D RF waterfall spectrogram (up to 32 rows x 36 bins in dBm) using SpectrumNet-2D into Quiet, Narrowband_OOK, Periodic_Beacon, Wideband_EMI_SMPS, FHSS_Hopper, LoRa_Chirp, CW_Jammer, or RollJam_Suspect.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"waterfall_dbm": map[string]any{
						"type":        "array",
						"description": "2D array of RSSI rows in dBm (up to 32 rows x 36 bins); shorter grids are padded with noise floor",
						"items": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "integer"},
						},
					},
					"baseline_dbm": map[string]any{
						"type":        "array",
						"description": "Optional per-bin noise floor baseline in dBm (36 values, default -102 dBm)",
						"items":       map[string]any{"type": "number"},
					},
				},
				"required": []string{"waterfall_dbm"},
			},
		},
	}
}

func (s *Server) dispatchNeuralMLTool(name string, rawArgs json.RawMessage) (*toolCallResult, *rpcError, bool) {
	switch name {
	case "rf_model_info":
		res, err := s.toolModelInfo()
		return res, err, true
	case "rf_classify_spectrum":
		res, err := s.toolClassifySpectrum(rawArgs)
		return res, err, true
	default:
		return nil, nil, false
	}
}

func (s *Server) toolModelInfo() (*toolCallResult, *rpcError) {
	return jsonResult(map[string]any{
		"pulsenet_rf":  rfml.ActivePulseNetInfo(),
		"spectrumnet":  rfml.ActiveSpectrumNetInfo(),
		"models_dir":   rfml.DefaultModelsDir(),
		"embedding_dim": rfml.EmbeddingDim,
	})
}

func (s *Server) toolClassifySpectrum(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		WaterfallDBm [][]int32 `json:"waterfall_dbm"`
		BaselineDBm  []float32 `json:"baseline_dbm"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if len(args.WaterfallDBm) == 0 {
		return toolError("waterfall_dbm must contain at least 1 row")
	}
	var wf [rfml.SpectrumRows][rfml.SpectrumCols]int32
	for r := range rfml.SpectrumRows {
		for c := range rfml.SpectrumCols {
			wf[r][c] = -104
		}
	}
	rows := min(len(args.WaterfallDBm), rfml.SpectrumRows)
	rowOffset := rfml.SpectrumRows - rows
	for r := range rows {
		cols := min(len(args.WaterfallDBm[r]), rfml.SpectrumCols)
		for c := range cols {
			wf[rowOffset+r][c] = args.WaterfallDBm[r][c]
		}
	}
	var floor [rfml.SpectrumCols]float32
	for c := range rfml.SpectrumCols {
		floor[c] = -102
		if c < len(args.BaselineDBm) {
			floor[c] = args.BaselineDBm[c]
		}
	}
	pred, ok := rfml.ClassifySpectrum(&wf, &floor)
	if !ok {
		return toolError("spectrumnet model is unavailable")
	}
	return jsonResult(map[string]any{
		"prediction": pred,
		"model":      rfml.ActiveSpectrumNetInfo(),
	})
}
