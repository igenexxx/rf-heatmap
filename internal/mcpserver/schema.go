package mcpserver

// toolDefinitions returns the canonical SSOT list of MCP tool schemas exposed by rf-heatmap.
func (s *Server) toolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "rf_get_status",
			Description: "Return CC1101 hardware/simulator status, active settings, supported ISM bands, and modem presets.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "rf_sweep_band",
			Description: "Sweep an ISM Sub-GHz band using CC1101 (or simulator) with rfml.BinBaseline Smart Squelch and burst detection.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"band": map[string]any{
						"type":        "integer",
						"description": "Band index: 0=433 ISM, 1=868 EU, 2=315 US, 3=915 ISM, 4=ALL ISM",
						"minimum":     0,
						"maximum":     4,
					},
					"sweeps": map[string]any{
						"type":        "integer",
						"description": "Number of sweeps (1..30, default 8)",
						"minimum":     1,
						"maximum":     30,
					},
					"squelch_k": map[string]any{
						"type":        "number",
						"description": "Optional Smart Squelch sigma multiplier k (1.5..8.0)",
						"minimum":     1.5,
						"maximum":     8.0,
					},
				},
			},
		},
		{
			Name:        "rf_read_rssi",
			Description: "Tune to a specific Sub-GHz frequency and sample live RSSI with rfml.BinBaseline statistics.",
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
						"description": "Modem preset name (AM650, AM270, FM238, FM476, or Flipper preset string)",
					},
					"samples": map[string]any{
						"type":        "integer",
						"description": "Number of RSSI samples (1..32, default 8)",
						"minimum":     1,
						"maximum":     32,
					},
				},
				"required": []string{"freq_hz"},
			},
		},
		{
			Name:        "rf_decode_ook",
			Description: "Decode signed microsecond OOK pulse timings (Princeton, CAME, Linear) with automatic 1D K-Means glitch-denoising fallback.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pulses": map[string]any{
						"type":        "array",
						"description": "Signed pulse durations in microseconds (+high, -low)",
						"items":       map[string]any{"type": "integer"},
					},
				},
				"required": []string{"pulses"},
			},
		},
		{
			Name:        "rf_analyze_pulses",
			Description: "Run 1D K-Means pulse clustering, EMI glitch merging, Auto-TE estimation, MLP signal classification, and noise-resilient OOK decoding.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"pulses": map[string]any{
						"type":        "array",
						"description": "Signed pulse durations in microseconds (+high, -low)",
						"items":       map[string]any{"type": "integer"},
					},
					"min_glitch_us": map[string]any{
						"type":        "integer",
						"description": "Minimum pulse duration in microseconds before glitch merging (default 60)",
						"minimum":     1,
						"maximum":     500,
					},
					"freq_hz": map[string]any{
						"type":        "integer",
						"description": "Optional carrier frequency in Hz for MLP band feature (default 433920000)",
					},
					"snr_db": map[string]any{
						"type":        "number",
						"description": "Optional signal-to-noise ratio in dB (default 25)",
					},
				},
				"required": []string{"pulses"},
			},
		},
		{
			Name:        "rf_classify_signal",
			Description: "Classify a Sub-GHz RAW_Data pulse capture using the on-device 12->24->5 MLP (Fixed OOK, Rolling/KeyFob, Sensor/Weather, CW/Jammer, Noise).",
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
						"description": "Optional carrier frequency in Hz (default 433920000)",
					},
					"snr_db": map[string]any{
						"type":        "number",
						"description": "Optional signal-to-noise ratio in dB (default 25)",
					},
				},
				"required": []string{"pulses"},
			},
		},
		{
			Name:        "rf_match_fingerprint",
			Description: "Match a live RAW_Data capture against saved .sub files using 8D L2-normalized RF embeddings and frequency-gated cosine k-NN.",
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
					},
					"preset": map[string]any{
						"type":        "string",
						"description": "Optional modem preset (AM650/AM270/FM238/FM476)",
					},
					"min_similarity": map[string]any{
						"type":        "number",
						"description": "Minimum cosine similarity threshold 0..1 (default 0.85)",
					},
					"top_k": map[string]any{
						"type":        "integer",
						"description": "Maximum number of ranked matches to return (default 3)",
					},
				},
				"required": []string{"pulses"},
			},
		},
		{
			Name:        "rf_foxhunt_track",
			Description: "Track a single frequency with the 1D Kalman RSSI Foxhunt filter and IAT Periodicity/Beacon/CW detector.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"freq_hz": map[string]any{
						"type":        "integer",
						"description": "Carrier frequency in Hz (280000000..950000000)",
					},
					"preset": map[string]any{
						"type":        "string",
						"description": "Optional modem preset (AM650/AM270/FM238/FM476)",
					},
					"samples": map[string]any{
						"type":        "integer",
						"description": "Number of live RSSI samples (8..64, default 16)",
						"minimum":     8,
						"maximum":     64,
					},
					"interval_ms": map[string]any{
						"type":        "integer",
						"description": "Delay between samples in ms (0..200, default 20)",
						"minimum":     0,
						"maximum":     200,
					},
				},
				"required": []string{"freq_hz"},
			},
		},
		{
			Name:        "rf_list_saved",
			Description: "List saved Flipper-compatible .sub files from the captures directory and ~/subg/files.",
			InputSchema: map[string]any{
				"type":       "object",
				"properties": map[string]any{},
			},
		},
		{
			Name:        "rf_read_saved",
			Description: "Read and parse a saved .sub file by filename.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Filename of the .sub capture (no path separators)",
					},
				},
				"required": []string{"name"},
			},
		},
		{
			Name:        "rf_save_capture",
			Description: "Save a Flipper-compatible .sub capture (Key or RAW) into the captures directory.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Target filename (.sub appended if omitted; path separators rejected)",
					},
					"freq_hz": map[string]any{
						"type":        "integer",
						"description": "Carrier frequency in Hz (280000000..950000000)",
					},
					"preset": map[string]any{
						"type":        "string",
						"description": "Preset name (e.g. AM650 or FuriHalSubGhzPresetOok650Async)",
					},
					"protocol": map[string]any{
						"type":        "string",
						"description": "Protocol name (e.g. Princeton, CAME, Linear, RAW)",
					},
					"key_hex": map[string]any{
						"type":        "string",
						"description": "Decoded hex key string (for Key files)",
					},
					"bits": map[string]any{
						"type":        "integer",
						"description": "Bit count (for Key files)",
					},
					"te": map[string]any{
						"type":        "integer",
						"description": "Base pulse width TE in microseconds",
					},
					"raw_data": map[string]any{
						"type":        "array",
						"description": "Signed microsecond RAW_Data pulses",
						"items":       map[string]any{"type": "integer"},
					},
				},
				"required": []string{"name", "freq_hz"},
			},
		},
		{
			Name:        "rf_delete_saved",
			Description: "Delete a saved .sub file inside the captures directory (requires confirm=true).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"name": map[string]any{
						"type":        "string",
						"description": "Filename inside captures directory (path traversal rejected)",
					},
					"confirm": map[string]any{
						"type":        "boolean",
						"description": "Must be explicitly true to confirm deletion",
					},
				},
				"required": []string{"name", "confirm"},
			},
		},
		{
			Name:        "rf_purge_noise",
			Description: "Scan captures directory for RAW .sub files classified as Noise by the on-device MLP and optionally delete them (dry_run defaults to true; deletion requires dry_run=false and confirm=true).",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"dry_run": map[string]any{
						"type":        "boolean",
						"description": "Preview matching noise files without deleting (default true)",
					},
					"confirm": map[string]any{
						"type":        "boolean",
						"description": "Must be explicitly true when dry_run=false",
					},
					"min_confidence": map[string]any{
						"type":        "number",
						"description": "Minimum Noise class confidence 0.5..1.0 (default 0.85)",
						"minimum":     0.5,
						"maximum":     1.0,
					},
				},
			},
		},
	}
}
