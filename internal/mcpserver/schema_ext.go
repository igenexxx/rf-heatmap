package mcpserver

func (s *Server) extToolDefinitions() []toolDefinition {
	base := []toolDefinition{
		{
			Name:        "rf_analyze_rolling_code",
			Description: "Analyze 1 to 8 Sub-GHz RAW_Data captures for KeeLoq/HCS301, Somfy Telis, or generic consecutive rolling-code behavior.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"freq_hz": map[string]any{
						"type":        "integer",
						"description": "Carrier frequency in Hz (default 433920000)",
						"minimum":     minFreqHz,
						"maximum":     maxFreqHz,
					},
					"frames": map[string]any{
						"type":        "array",
						"description": "1 to 8 captured RAW_Data pulse arrays (+high, -low in microseconds)",
						"minItems":    1,
						"maxItems":    8,
						"items": map[string]any{
							"type":  "array",
							"items": map[string]any{"type": "integer"},
						},
					},
				},
				"required": []string{"frames"},
			},
		},
		{
			Name:        "rf_decode_sensor",
			Description: "Decode Sub-GHz TPMS (Schrader/Toyota) or Weather Station (Oregon Scientific, Acurite, LaCrosse) telemetry from signed microsecond pulse timings.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pulses": map[string]any{
						"type":        "array",
						"description": "Signed pulse durations in microseconds (+high, -low)",
						"items":       map[string]any{"type": "integer"},
					},
					"freq_hz": map[string]any{
						"type":        "integer",
						"description": "Carrier frequency in Hz (default 433920000)",
						"minimum":     minFreqHz,
						"maximum":     maxFreqHz,
					},
					"preset": map[string]any{
						"type":        "string",
						"description": "Modem preset (AM650, AM270, FM238, FM476, GFSK99)",
					},
					"sim_sample": map[string]any{
						"type":        "boolean",
						"description": "If true and pulses omitted, decode a deterministic synthetic sensor frame",
					},
				},
			},
		},
		{
			Name:        "rf_foxhunt_sample",
			Description: "Sample a single locked Sub-GHz frequency with peak-hold, 1D Kalman smoothing, and Geiger RSSI tick cadence.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"freq_hz": map[string]any{
						"type":        "integer",
						"description": "Carrier frequency in Hz (280000000..950000000)",
						"minimum":     minFreqHz,
						"maximum":     maxFreqHz,
					},
					"preset": map[string]any{
						"type":        "string",
						"description": "Optional modem preset (default AM650)",
					},
					"samples": map[string]any{
						"type":        "integer",
						"description": "Samples to advance filter (1..16, default 4)",
						"minimum":     1,
						"maximum":     16,
					},
					"reset_peak": map[string]any{
						"type":        "boolean",
						"description": "Reset peak-hold and Kalman state before sampling",
					},
				},
				"required": []string{"freq_hz"},
			},
		},
		{
			Name:        "rf_trim_raw",
			Description: "Trim leading/trailing noise from a RAW .sub capture (or inline pulses), optionally repeat 1..10x, and save atomically into User Library (~/rf-heatmap/captures/). Never modifies external databases.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pack": map[string]any{
						"type":        "string",
						"description": "Optional source pack_id (default: user)",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Source .sub filename (no path separators)",
					},
					"pulses": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "integer"},
						"description": "Optional inline RAW_Data pulses when name is omitted",
					},
					"freq_hz": map[string]any{
						"type":        "integer",
						"minimum":     minFreqHz,
						"maximum":     maxFreqHz,
						"description": "Carrier frequency in Hz when saving inline pulses (default 433920000)",
					},
					"auto_trim": map[string]any{
						"type":        "boolean",
						"description": "Auto-detect clean burst boundaries (default true if start_idx/end_idx omitted)",
					},
					"start_idx": map[string]any{
						"type":    "integer",
						"minimum": 0,
					},
					"end_idx": map[string]any{
						"type":    "integer",
						"minimum": 1,
					},
					"repeats": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     10,
						"description": "Frame repeat count (default 1)",
					},
					"inter_frame_gap_us": map[string]any{
						"type":        "integer",
						"minimum":     1000,
						"maximum":     100000,
						"description": "Low gap between repeats in us (default 10000)",
					},
					"save_as": map[string]any{
						"type":        "string",
						"description": "Optional output filename saved atomically to ~/rf-heatmap/captures/ (no path separators)",
					},
				},
			},
		},
		{
			Name:        "rf_signal_snapshot",
			Description: "Build a time-domain OOK waveform snapshot and dual-polarity (+HIGH/-LOW) microsecond pulse-width histogram with K-Means Short/Long/Sync centroids for a saved .sub capture (RAW or Key) or inline pulses.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pack": map[string]any{
						"type":        "string",
						"description": "Optional source pack_id (default: user)",
					},
					"name": map[string]any{
						"type":        "string",
						"description": "Source .sub filename (no path separators; supports RAW and Key .sub files)",
					},
					"pulses": map[string]any{
						"type":        "array",
						"items":       map[string]any{"type": "integer"},
						"description": "Optional inline signed RAW_Data pulses (+high, -low in us) when name is omitted",
					},
					"freq_hz": map[string]any{
						"type":        "integer",
						"minimum":     minFreqHz,
						"maximum":     maxFreqHz,
						"description": "Carrier frequency in Hz for inline pulses (default 433920000)",
					},
					"preset": map[string]any{
						"type":        "string",
						"description": "Optional modem preset label for inline pulses (default AM650)",
					},
					"bins": map[string]any{
						"type":        "integer",
						"minimum":     8,
						"maximum":     48,
						"description": "Pulse-width histogram bucket count (8..48, default 24)",
					},
					"max_segments": map[string]any{
						"type":        "integer",
						"minimum":     1,
						"maximum":     512,
						"description": "Maximum waveform segments returned in waveform_preview (default 128)",
					},
				},
			},
		},
	}
	out := append(base, s.unlockToolDefinitions()...)
	return append(out, s.mlNeuralToolDefinitions()...)
}
