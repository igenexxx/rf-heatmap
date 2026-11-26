package mcpserver

import (
	"os"
	"path/filepath"
	"testing"

	"rf-heatmap/internal/config"
	"rf-heatmap/internal/subfile"
)

func TestToolSignalSnapshot_InlineAndSavedKeySub(t *testing.T) {
	capsDir := t.TempDir()
	keySub := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   433_920_000,
		Preset:   "FuriHalSubGhzPresetOok650Async",
		Protocol: "Princeton",
		Bit:      24,
		Key:      "00 00 00 00 00 A5 5A 01",
		TE:       350,
	}
	if err := os.WriteFile(filepath.Join(capsDir, "gate.sub"), []byte(keySub.Format()), 0o644); err != nil {
		t.Fatalf("write gate.sub: %v", err)
	}

	srv := NewServer(Options{
		CapturesDir:    capsDir,
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
	})

	// 1. Saved Key .sub file -> synthesizes pulses and builds histogram.
	res, isErr, rpcErr := callTool(t, srv, "rf_signal_snapshot", map[string]any{
		"name":         "gate.sub",
		"bins":         16,
		"max_segments": 32,
	})
	if rpcErr != nil || isErr {
		t.Fatalf("saved snapshot failed: isErr=%v rpcErr=%v res=%v", isErr, rpcErr, res)
	}
	if res["protocol"] != "Princeton" {
		t.Fatalf("protocol = %v, want Princeton", res["protocol"])
	}
	if hist, ok := res["histogram"].([]any); !ok || len(hist) != 16 {
		t.Fatalf("expected 16 histogram bins, got %v", res["histogram"])
	}
	if preview, ok := res["waveform_preview"].([]any); !ok || len(preview) != 32 {
		t.Fatalf("expected 32 preview segments, got %v", res["waveform_preview"])
	}
	if truncated, _ := res["waveform_truncated"].(bool); !truncated {
		t.Fatalf("expected waveform_truncated=true")
	}

	// 2. Inline RAW pulses.
	res2, isErr, rpcErr := callTool(t, srv, "rf_signal_snapshot", map[string]any{
		"pulses":  []int32{-10500, 350, -1050, 1050, -350, 350, -1050, 1050, -350, 350, -10500},
		"freq_hz": 868_350_000,
	})
	if rpcErr != nil || isErr {
		t.Fatalf("inline snapshot failed: isErr=%v rpcErr=%v res=%v", isErr, rpcErr, res2)
	}
	if res2["source"] != "inline" || int(res2["total_pulses"].(float64)) != 11 {
		t.Fatalf("unexpected inline snapshot result: %+v", res2)
	}

	// 3. Adversarial checks: missing source, path traversal, out-of-range frequency.
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"empty args", map[string]any{}},
		{"path traversal", map[string]any{"name": "../etc/passwd"}},
		{"missing file", map[string]any{"name": "nope.sub"}},
		{"bad freq", map[string]any{"pulses": []int32{350, -350}, "freq_hz": 100}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			_, gotErr, gotRPC := callTool(t, srv, "rf_signal_snapshot", tc.args)
			if gotRPC != nil || !gotErr {
				t.Fatalf("expected tool error for %s, got isErr=%v rpcErr=%v", tc.name, gotErr, gotRPC)
			}
		})
	}
}
