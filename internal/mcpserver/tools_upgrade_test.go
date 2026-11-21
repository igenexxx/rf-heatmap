package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"rf-heatmap/internal/config"
	"rf-heatmap/internal/sensors"
	"rf-heatmap/internal/subfile"
)

func newUpgradeTestServer(t *testing.T) (*Server, string, string) {
	t.Helper()
	tmp := t.TempDir()
	capDir := filepath.Join(tmp, "captures")
	dbDir := filepath.Join(tmp, "databases")
	if err := os.MkdirAll(filepath.Join(dbDir, "fans"), 0o755); err != nil {
		t.Fatal(err)
	}
	srv := NewServer(Options{
		CapturesDir:    capDir,
		DatabasesDir:   dbDir,
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
	})
	return srv, capDir, dbDir
}

func callToolJSON(t *testing.T, srv *Server, name string, args map[string]any) (map[string]any, bool) {
	t.Helper()
	raw, err := json.Marshal(args)
	if err != nil {
		t.Fatal(err)
	}
	res, rpcErr := srv.dispatchTool(name, raw)
	if rpcErr != nil {
		t.Fatalf("unexpected rpcErr for %s: %+v", name, rpcErr)
	}
	if res.IsError {
		return map[string]any{"error": res.Content[0].Text}, true
	}
	var out map[string]any
	if err := json.Unmarshal([]byte(res.Content[0].Text), &out); err != nil {
		t.Fatalf("invalid json response from %s: %v (%s)", name, err, res.Content[0].Text)
	}
	return out, false
}

func TestMCPAnalyzeRollingCode(t *testing.T) {
	srv, _, _ := newUpgradeTestServer(t)

	// KeeLoq single frame.
	kl := subfile.EncodeKeeLoqPulses(0xCAFEBABE, 0x0123456, 0x3, 400)
	out, isErr := callToolJSON(t, srv, "rf_analyze_rolling_code", map[string]any{
		"freq_hz": 433920000,
		"frames":  [][]int32{kl},
	})
	if isErr || out["is_rolling"] != true || out["kind"] != "keeloq" {
		t.Fatalf("KeeLoq MCP result unexpected: isErr=%v out=%+v", isErr, out)
	}

	// Empty frames -> tool error.
	if _, isErr := callToolJSON(t, srv, "rf_analyze_rolling_code", map[string]any{"frames": [][]int32{}}); !isErr {
		t.Fatal("expected tool error for empty frames")
	}
	// >8 frames -> tool error.
	nine := make([][]int32, 9)
	for i := range nine {
		nine[i] = kl
	}
	if _, isErr := callToolJSON(t, srv, "rf_analyze_rolling_code", map[string]any{"frames": nine}); !isErr {
		t.Fatal("expected tool error for 9 frames")
	}
}

func TestMCPDecodeSensor(t *testing.T) {
	srv, _, _ := newUpgradeTestServer(t)

	// Explicit TPMS pulses.
	tpms := sensors.EncodeSchraderTPMS(0x3A4F12, 235.0, 26.0, 0x00)
	out, isErr := callToolJSON(t, srv, "rf_decode_sensor", map[string]any{
		"pulses":  tpms,
		"freq_hz": 433920000,
		"preset":  "GFSK99",
	})
	if isErr || out["decoded"] != true {
		t.Fatalf("TPMS decode failed: isErr=%v out=%+v", isErr, out)
	}

	// sim_sample: true.
	simOut, isErr := callToolJSON(t, srv, "rf_decode_sensor", map[string]any{"sim_sample": true})
	if isErr || simOut["decoded"] != true || simOut["source"] != "sim" {
		t.Fatalf("sim_sample failed: isErr=%v out=%+v", isErr, simOut)
	}

	// Noise -> decoded: false.
	noiseOut, isErr := callToolJSON(t, srv, "rf_decode_sensor", map[string]any{
		"pulses": []int32{100, -100, 200, -200},
	})
	if isErr || noiseOut["decoded"] != false {
		t.Fatalf("expected decoded=false for noise, got isErr=%v out=%+v", isErr, noiseOut)
	}

	// Missing pulses and sim_sample=false -> tool error.
	if _, isErr := callToolJSON(t, srv, "rf_decode_sensor", map[string]any{}); !isErr {
		t.Fatal("expected error when pulses omitted and sim_sample=false")
	}
}

func TestMCPFoxhuntSample(t *testing.T) {
	srv, _, _ := newUpgradeTestServer(t)

	out, isErr := callToolJSON(t, srv, "rf_foxhunt_sample", map[string]any{
		"freq_hz":    433920000,
		"samples":    4,
		"reset_peak": true,
	})
	if isErr {
		t.Fatalf("rf_foxhunt_sample failed: %+v", out)
	}
	if _, ok := out["peak_dbm"]; !ok {
		t.Fatalf("missing peak_dbm: %+v", out)
	}
	if _, ok := out["geiger_hz"]; !ok {
		t.Fatalf("missing geiger_hz: %+v", out)
	}

	// Invalid frequency -> tool error.
	if _, isErr := callToolJSON(t, srv, "rf_foxhunt_sample", map[string]any{"freq_hz": 1000}); !isErr {
		t.Fatal("expected error for out-of-range freq_hz")
	}
}

func TestMCPTrimRawAndReadOnlyDBProtection(t *testing.T) {
	srv, capDir, dbDir := newUpgradeTestServer(t)

	// Seed a read-only external DB file in db:fans.
	dbFile := filepath.Join(dbDir, "fans", "noisy_fan.sub")
	dbContent := "Filetype: Flipper SubGhz RAW File\nVersion: 1\nFrequency: 433920000\nPreset: FuriHalSubGhzPresetOok650Async\nProtocol: RAW\nRAW_Data: -50000 35 -40000 350 -1050 1050 -350 350 -1050 -50000\n"
	if err := os.WriteFile(dbFile, []byte(dbContent), 0o644); err != nil {
		t.Fatal(err)
	}

	// Trim from read-only pack db:fans and save_as clean_fan.sub -> must land strictly in captures/.
	out, isErr := callToolJSON(t, srv, "rf_trim_raw", map[string]any{
		"pack":    "db:fans",
		"name":    "noisy_fan.sub",
		"repeats": 3,
		"save_as": "clean_fan.sub",
	})
	if isErr {
		t.Fatalf("rf_trim_raw failed: %+v", out)
	}
	if out["saved_name"] != "clean_fan.sub" {
		t.Fatalf("saved_name = %v, want clean_fan.sub", out["saved_name"])
	}
	if _, err := os.Stat(filepath.Join(capDir, "clean_fan.sub")); err != nil {
		t.Fatalf("expected clean_fan.sub in capturesDir: %v", err)
	}
	// Verify external DB file was NOT modified.
	gotDB, _ := os.ReadFile(dbFile)
	if string(gotDB) != dbContent {
		t.Fatal("external DB file was modified by rf_trim_raw!")
	}

	// Path traversal in save_as MUST reject.
	if _, isErr := callToolJSON(t, srv, "rf_trim_raw", map[string]any{
		"pulses":  []int32{350, -1050, 1050, -350},
		"save_as": "../databases/evil.sub",
	}); !isErr {
		t.Fatal("expected path traversal rejection in save_as")
	}

	// Preview mode (no save_as) writes nothing.
	prevOut, isErr := callToolJSON(t, srv, "rf_trim_raw", map[string]any{
		"pulses":  []int32{-40000, 350, -1050, 1050, -350, -40000},
		"repeats": 2,
	})
	if isErr || prevOut["repeats"].(float64) != 2 {
		t.Fatalf("preview trim failed: isErr=%v out=%+v", isErr, prevOut)
	}
}
