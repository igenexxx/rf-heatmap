package mcpserver

import "rf-heatmap/internal/cc1101"

func (s *Server) unlockToolDefinitions() []toolDefinition {
	return []toolDefinition{
		{
			Name:        "rf_measure_freq_offset",
			Description: "Measure CC1101 carrier frequency drift (FREQEST 0x32) in Hz/ppm along with RSSI and LQI (0x33).",
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
						"description": "Optional modem preset (AM650, AM270, FM238, FM476, GFSK99)",
					},
					"samples": map[string]any{
						"type":        "integer",
						"description": "Number of averaged samples (1..16, default 4)",
						"minimum":     1,
						"maximum":     16,
					},
				},
				"required": []string{"freq_hz"},
			},
		},
		{
			Name:        "rf_set_tx_power",
			Description: "Configure CC1101 TX power via band-calibrated PATABLE (-30, -20, -10, 0, +5, +10 dBm) and optionally persist to config.json.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"dbm": map[string]any{
						"type":        "integer",
						"description": "Target TX power in dBm (clamped to -30, -20, -10, 0, 5, 10)",
					},
					"freq_hz": map[string]any{
						"type":        "integer",
						"description": "Carrier frequency in Hz for band PATABLE lookup (default 433920000)",
						"minimum":     minFreqHz,
						"maximum":     maxFreqHz,
					},
					"persist": map[string]any{
						"type":        "boolean",
						"description": "Persist tx_power_dbm into config.json (default true)",
					},
				},
				"required": []string{"dbm"},
			},
		},
		{
			Name:        "rf_wor_listen",
			Description: "Configure CC1101 Wake-On-Radio (WOREVT1/0, MCSM2, WORCTRL, SWOR) and monitor for an RSSI wake trigger.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"freq_hz": map[string]any{
						"type":        "integer",
						"description": "Carrier frequency in Hz (280000000..950000000)",
						"minimum":     minFreqHz,
						"maximum":     maxFreqHz,
					},
					"interval_ms": map[string]any{
						"type":        "integer",
						"description": "WOR polling interval in ms (10..60000, default 500)",
						"minimum":     int(cc1101.MinWORIntervalMs),
						"maximum":     int(cc1101.MaxWORIntervalMs),
					},
					"threshold_dbm": map[string]any{
						"type":        "integer",
						"description": "Wake RSSI threshold in dBm (default -75)",
					},
					"duration_ms": map[string]any{
						"type":        "integer",
						"description": "Listen window in ms (50..5000, default 500)",
						"minimum":     50,
						"maximum":     5000,
					},
				},
				"required": []string{"freq_hz"},
			},
		},
		{
			Name:        "rf_send_packet",
			Description: "Transmit a 1..61 byte payload using the CC1101 64B hardware FIFO Packet Engine (Sync Word, CRC-16, optional whitening). Requires confirm_rf=true.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"freq_hz": map[string]any{
						"type":        "integer",
						"description": "Carrier frequency in Hz (280000000..950000000)",
						"minimum":     minFreqHz,
						"maximum":     maxFreqHz,
					},
					"payload_hex": map[string]any{
						"type":        "string",
						"description": "Hex-encoded payload bytes (1..61 bytes)",
					},
					"text": map[string]any{
						"type":        "string",
						"description": "UTF-8/ASCII text payload (1..61 bytes) when payload_hex is omitted",
					},
					"preset": map[string]any{
						"type":        "string",
						"description": "Modem preset (default GFSK99)",
					},
					"sync_word": map[string]any{
						"type":        "integer",
						"description": "16-bit sync word (default 54161 / 0xD391)",
						"minimum":     1,
						"maximum":     65535,
					},
					"whitening": map[string]any{
						"type":        "boolean",
						"description": "Enable PN9 data whitening (default true)",
					},
					"crc": map[string]any{
						"type":        "boolean",
						"description": "Enable hardware CRC-16 (default true)",
					},
					"confirm_rf": map[string]any{
						"type":        "boolean",
						"description": "Must be explicitly true to authorize RF packet transmission",
					},
				},
				"required": []string{"freq_hz", "confirm_rf"},
			},
		},
		{
			Name:        "rf_recv_packet",
			Description: "Listen for a 1..61 byte packet via the CC1101 64B hardware RX FIFO Packet Engine and return payload, RSSI, LQI, and CRC_OK.",
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
						"description": "Modem preset (default GFSK99)",
					},
					"sync_word": map[string]any{
						"type":        "integer",
						"description": "16-bit sync word (default 54161 / 0xD391)",
						"minimum":     1,
						"maximum":     65535,
					},
					"whitening": map[string]any{
						"type":        "boolean",
						"description": "Enable PN9 data whitening (default true)",
					},
					"crc": map[string]any{
						"type":        "boolean",
						"description": "Enable hardware CRC-16 (default true)",
					},
					"timeout_ms": map[string]any{
						"type":        "integer",
						"description": "RX timeout in ms (50..5000, default 500)",
						"minimum":     50,
						"maximum":     5000,
					},
					"sim_sample": map[string]any{
						"type":        "boolean",
						"description": "Return a deterministic simulated pager packet when true or in SIM mode",
					},
				},
				"required": []string{"freq_hz"},
			},
		},
		{
			Name:        "rf_decode_pocsag",
			Description: "Decode a POCSAG (512/1200/2400 bps) pager byte or codeword stream with BCH(31,21) verification and 7-bit alpha / 4-bit numeric extraction.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data_hex": map[string]any{
						"type":        "string",
						"description": "Hex-encoded big-endian POCSAG byte stream",
					},
					"words": map[string]any{
						"type":        "array",
						"description": "32-bit POCSAG codewords (alternative to data_hex)",
						"items":       map[string]any{"type": "integer"},
					},
					"baud_rate": map[string]any{
						"type":        "integer",
						"description": "POCSAG baud rate: 512, 1200, or 2400 (default 1200)",
					},
					"sim_sample": map[string]any{
						"type":        "boolean",
						"description": "If true and input omitted, decode a deterministic BCH-encoded synthetic POCSAG batch",
					},
				},
			},
		},
		{
			Name:        "rf_decode_wmbus",
			Description: "Decode an EN 13757-4 Wireless M-Bus (wM-Bus T1/C1/S1, 868.95 MHz) frame into manufacturer code, BCD serial, device type, CI-field, and CRC-16 status.",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"data_hex": map[string]any{
						"type":        "string",
						"description": "Hex-encoded wM-Bus Format A byte frame",
					},
					"mode": map[string]any{
						"type":        "string",
						"description": "wM-Bus mode: T1, C1, or S1 (default T1)",
					},
					"sim_sample": map[string]any{
						"type":        "boolean",
						"description": "If true and data_hex omitted, decode a deterministic synthetic wM-Bus water meter frame",
					},
				},
			},
		},
	}
}
