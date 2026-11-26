package mcpserver

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/config"
)

type stubRadio struct {
	mu           sync.Mutex
	info         cc1101.ChipInfo
	preset       cc1101.Preset
	lastFreq     uint32
	rssiFn       func(freq uint32, call int) (int32, error)
	presetErr    error
	rxErr        error
	calls        int
	idleCalls    int
	activeChecks int
}

func (r *stubRadio) Info() cc1101.ChipInfo {
	r.mu.Lock()
	defer r.mu.Unlock()
	return r.info
}

func (r *stubRadio) ApplyPreset(p cc1101.Preset) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.presetErr != nil {
		return r.presetErr
	}
	r.preset = p
	return nil
}

func (r *stubRadio) StartReceive(freqHz uint32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.rxErr != nil {
		return r.rxErr
	}
	r.lastFreq = freqHz
	return nil
}

func (r *stubRadio) ReadRSSIDBm() (int32, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.calls++
	if r.rssiFn != nil {
		return r.rssiFn(r.lastFreq, r.calls)
	}
	return -102, nil
}

func (r *stubRadio) Idle() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.idleCalls++
	return nil
}

func (r *stubRadio) TransmitPulses(ctx context.Context, freqHz uint32, preset cc1101.Preset, pulses []int32) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.lastFreq = freqHz
	r.preset = preset
	return ctx.Err()
}

// fakeSPITransport exercises CC1101Backend wrapping a real *cc1101.Radio without hardware.
type fakeSPITransport struct{}

func (f *fakeSPITransport) Transfer(tx []byte) ([]byte, error) {
	rx := make([]byte, len(tx))
	if len(tx) >= 2 {
		addr := tx[0] & 0x3F
		switch addr {
		case 0x30: // PARTNUM
			rx[1] = 0x00
		case 0x31: // VERSION
			rx[1] = 0x14
		case 0x34: // RSSI raw -> -74 dBm
			rx[1] = 0x00
		case 0x35: // MARCSTATE -> TX
			rx[1] = 0x13
		}
	}
	return rx, nil
}
func (f *fakeSPITransport) SetRFSwitch(bool) error                       { return nil }
func (f *fakeSPITransport) TransmitPulses(context.Context, []int32) error { return nil }
func (f *fakeSPITransport) Close() error                                 { return nil }

func TestCC1101BackendAdapter(t *testing.T) {
	if got := NewCC1101Backend(nil); got != nil {
		t.Fatalf("NewCC1101Backend(nil) = %v, want nil", got)
	}
	radio, err := cc1101.Open(&fakeSPITransport{}, cc1101.DefaultCrystalHz)
	if err != nil {
		t.Fatalf("cc1101.Open failed: %v", err)
	}
	defer radio.Close()

	be := NewCC1101Backend(radio)
	if info := be.Info(); info.Version != 0x14 {
		t.Fatalf("Info().Version = 0x%02X, want 0x14", info.Version)
	}
	if err := be.ApplyPreset(cc1101.PresetAM270); err != nil {
		t.Fatalf("ApplyPreset failed: %v", err)
	}
	if err := be.StartReceive(433_920_000); err != nil {
		t.Fatalf("StartReceive failed: %v", err)
	}
	if rssi, err := be.ReadRSSIDBm(); err != nil || rssi != -74 {
		t.Fatalf("ReadRSSIDBm = (%d, %v), want (-74, nil)", rssi, err)
	}
	if err := be.Idle(); err != nil {
		t.Fatalf("Idle failed: %v", err)
	}
}

func runRPC(t *testing.T, srv *Server, lines ...string) []map[string]any {
	t.Helper()
	input := strings.Join(lines, "\n") + "\n"
	var out bytes.Buffer
	if err := srv.ServeStdio(context.Background(), strings.NewReader(input), &out); err != nil {
		t.Fatalf("ServeStdio failed: %v", err)
	}
	var resps []map[string]any
	for _, line := range strings.Split(strings.TrimSpace(out.String()), "\n") {
		if strings.TrimSpace(line) == "" {
			continue
		}
		var m map[string]any
		if err := json.Unmarshal([]byte(line), &m); err != nil {
			t.Fatalf("invalid JSON response %q: %v", line, err)
		}
		resps = append(resps, m)
	}
	return resps
}

func callTool(t *testing.T, srv *Server, name string, args any) (map[string]any, bool, *rpcError) {
	t.Helper()
	req := map[string]any{
		"jsonrpc": "2.0",
		"id":      1,
		"method":  "tools/call",
		"params": map[string]any{
			"name":      name,
			"arguments": args,
		},
	}
	raw, err := json.Marshal(req)
	if err != nil {
		t.Fatalf("marshal req: %v", err)
	}
	resps := runRPC(t, srv, string(raw))
	if len(resps) != 1 {
		t.Fatalf("expected 1 response, got %d", len(resps))
	}
	if errObj, ok := resps[0]["error"].(map[string]any); ok {
		return nil, false, &rpcError{
			Code:    int(errObj["code"].(float64)),
			Message: errObj["message"].(string),
		}
	}
	result := resps[0]["result"].(map[string]any)
	isErr, _ := result["isError"].(bool)
	content := result["content"].([]any)
	text := content[0].(map[string]any)["text"].(string)
	if isErr {
		return map[string]any{"error": text}, true, nil
	}
	var parsed map[string]any
	if err := json.Unmarshal([]byte(text), &parsed); err != nil {
		t.Fatalf("tool %s returned non-JSON text %q: %v", name, text, err)
	}
	return parsed, false, nil
}

func TestJSONRPCProtocolHandshakeAndErrors(t *testing.T) {
	srv := NewServer(Options{
		CapturesDir:    t.TempDir(),
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
	})

	resps := runRPC(t, srv,
		"",
		`{invalid json`,
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"ping"}`,
		`{"jsonrpc":"2.0","id":3,"method":"tools/list"}`,
		`{"jsonrpc":"2.0","id":4,"method":"unknown/method"}`,
		`{"jsonrpc":"2.0","id":5,"method":"tools/call","params":"not-an-object"}`,
		`{"jsonrpc":"2.0","id":6,"method":"tools/call","params":{"name":"nonexistent_tool"}}`,
		`{"jsonrpc":"2.0","id":7,"method":"tools/call","params":{"name":"rf_read_rssi","arguments":"bad"}}`,
	)

	// Expect 8 responses (empty line and notifications/initialized produce no output).
	if len(resps) != 8 {
		t.Fatalf("got %d responses, want 8: %+v", len(resps), resps)
	}

	// 0: parse error -32700
	if code := int(resps[0]["error"].(map[string]any)["code"].(float64)); code != errCodeParseError {
		t.Fatalf("parse error code = %d, want %d", code, errCodeParseError)
	}
	// 1: initialize
	initRes := resps[1]["result"].(map[string]any)
	if initRes["protocolVersion"] != ProtocolVersion {
		t.Fatalf("protocolVersion = %v, want %s", initRes["protocolVersion"], ProtocolVersion)
	}
	// 2: ping
	if _, ok := resps[2]["result"].(map[string]any); !ok {
		t.Fatalf("ping result missing: %+v", resps[2])
	}
	// 3: tools/list has all 40 tools
	tools := resps[3]["result"].(map[string]any)["tools"].([]any)
	if len(tools) != 40 {
		t.Fatalf("tools/list count = %d, want 40", len(tools))
	}
	wantTools := map[string]bool{
		"rf_signal_snapshot":      false,
		"rf_model_info":           false,
		"rf_classify_spectrum":    false,
		"rf_get_status":           false,
		"rf_sweep_band":           false,
		"rf_read_rssi":            false,
		"rf_decode_ook":           false,
		"rf_analyze_pulses":       false,
		"rf_classify_signal":      false,
		"rf_match_fingerprint":    false,
		"rf_foxhunt_track":        false,
		"rf_list_databases":       false,
		"rf_promote_to_user":      false,
		"rf_transmit_saved":       false,
		"rf_list_sources":         false,
		"rf_add_source":           false,
		"rf_remove_source":        false,
		"rf_sync_sources":         false,
		"rf_delete_pack_files":    false,
		"rf_rank_probe_pack":      false,
		"rf_list_saved":           false,
		"rf_purge_noise":          false,
		"rf_read_saved":           false,
		"rf_save_capture":         false,
		"rf_delete_saved":         false,
		"rf_analyze_rolling_code": false,
		"rf_decode_sensor":        false,
		"rf_foxhunt_sample":       false,
		"rf_trim_raw":             false,
		"rf_measure_freq_offset":  false,
		"rf_set_tx_power":         false,
		"rf_wor_listen":           false,
		"rf_send_packet":          false,
		"rf_recv_packet":          false,
		"rf_decode_pocsag":        false,
		"rf_decode_wmbus":         false,
		"rf_save_room_baseline":   false,
		"rf_list_room_baselines":  false,
		"rf_diff_room_baseline":   false,
		"rf_delete_room_baseline": false,
	}
	for _, rawT := range tools {
		tm := rawT.(map[string]any)
		name := tm["name"].(string)
		if _, ok := wantTools[name]; !ok {
			t.Fatalf("unexpected tool %q", name)
		}
		wantTools[name] = true
		if _, ok := tm["inputSchema"].(map[string]any); !ok {
			t.Fatalf("tool %q missing inputSchema", name)
		}
	}
	for name, seen := range wantTools {
		if !seen {
			t.Fatalf("missing required tool %q", name)
		}
	}
	// 4: unknown method -32601
	if code := int(resps[4]["error"].(map[string]any)["code"].(float64)); code != errCodeMethodNotFound {
		t.Fatalf("unknown method code = %d, want %d", code, errCodeMethodNotFound)
	}
	// 5, 6, 7: invalid params -32602
	for _, idx := range []int{5, 6, 7} {
		if code := int(resps[idx]["error"].(map[string]any)["code"].(float64)); code != errCodeInvalidParams {
			t.Fatalf("resp[%d] code = %d, want %d", idx, code, errCodeInvalidParams)
		}
	}
}

func TestServeStdioContextCanceled(t *testing.T) {
	srv := NewServer(Options{CapturesDir: t.TempDir(), ExtraSavedDirs: []string{}})
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	var out bytes.Buffer
	err := srv.ServeStdio(ctx, strings.NewReader(`{"jsonrpc":"2.0","id":1,"method":"ping"}`+"\n"), &out)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}
}

func TestToolGetStatusHardwareAndSim(t *testing.T) {
	simSrv := NewServer(Options{
		CapturesDir:    t.TempDir(),
		ExtraSavedDirs: []string{},
		ConfigPath:     "/tmp/test-config.json",
		Settings:       config.Settings{MCPEnabled: true, AutoSquelch: true, AutoLock: true, SquelchK: 4.0},
	})
	res, isErr, rpcErr := callTool(t, simSrv, "rf_get_status", map[string]any{})
	if isErr || rpcErr != nil {
		t.Fatalf("sim rf_get_status failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	if res["mode"] != "sim" || res["hardware_connected"] != false {
		t.Fatalf("unexpected sim status: %+v", res)
	}

	hwSrv := NewServer(Options{
		Radio:          &stubRadio{info: cc1101.ChipInfo{PartNumber: 0x00, Version: 0x14}},
		CapturesDir:    t.TempDir(),
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
		SettleDelay:    0,
	})
	res, isErr, rpcErr = callTool(t, hwSrv, "rf_get_status", map[string]any{})
	if isErr || rpcErr != nil {
		t.Fatalf("hw rf_get_status failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	if res["mode"] != "hardware" || res["hardware_connected"] != true {
		t.Fatalf("unexpected hw status: %+v", res)
	}
	chip := res["chip"].(map[string]any)
	if chip["version"] != "0x14" {
		t.Fatalf("unexpected chip version: %+v", chip)
	}
}

func TestToolSweepBandAndReadRSSI(t *testing.T) {
	// Mock radio that injects a +45dB burst on sweep >= 6 at 433.92 MHz (bin ~18).
	radio := &stubRadio{
		info: cc1101.ChipInfo{PartNumber: 0x00, Version: 0x14},
		rssiFn: func(freq uint32, call int) (int32, error) {
			if call > 36*5 && freq >= 433_900_000 && freq <= 433_960_000 {
				return -52, nil
			}
			return -104, nil
		},
	}
	srv := NewServer(Options{
		Radio:          radio,
		CapturesDir:    t.TempDir(),
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
		SettleDelay:    0,
	})

	// Sweep band 0 with 8 sweeps -> baseline ready + burst detected.
	res, isErr, rpcErr := callTool(t, srv, "rf_sweep_band", map[string]any{
		"band":      0,
		"sweeps":    8,
		"squelch_k": 3.0,
	})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_sweep_band failed: isErr=%v rpcErr=%+v res=%+v", isErr, rpcErr, res)
	}
	if res["ready"] != true {
		t.Fatalf("expected ready=true, got %+v", res["ready"])
	}
	bursts := res["bursts"].([]any)
	if len(bursts) == 0 {
		t.Fatalf("expected at least 1 burst detected, got 0")
	}

	// Read RSSI with preset FM238.
	res, isErr, rpcErr = callTool(t, srv, "rf_read_rssi", map[string]any{
		"freq_hz": 433_920_000,
		"preset":  "FM238",
		"samples": 6,
	})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_read_rssi failed: isErr=%v rpcErr=%+v res=%+v", isErr, rpcErr, res)
	}
	if res["preset"] != "FM238" || res["ready"] != true {
		t.Fatalf("unexpected rf_read_rssi result: %+v", res)
	}

	// Adversarial validation cases.
	badCases := []struct {
		tool string
		args map[string]any
	}{
		{"rf_sweep_band", map[string]any{"band": -1}},
		{"rf_sweep_band", map[string]any{"band": 99}},
		{"rf_sweep_band", map[string]any{"band": 0, "sweeps": 0}},
		{"rf_sweep_band", map[string]any{"band": 0, "sweeps": 31}},
		{"rf_read_rssi", map[string]any{"freq_hz": 100_000_000}},
		{"rf_read_rssi", map[string]any{"freq_hz": 999_000_000}},
		{"rf_read_rssi", map[string]any{"freq_hz": 433_920_000, "preset": "INVALID"}},
		{"rf_read_rssi", map[string]any{"freq_hz": 433_920_000, "samples": 0}},
		{"rf_read_rssi", map[string]any{"freq_hz": 433_920_000, "samples": 33}},
	}
	for _, tc := range badCases {
		_, isErr, rpcErr := callTool(t, srv, tc.tool, tc.args)
		if rpcErr != nil || !isErr {
			t.Fatalf("%s(%+v): expected tool isError=true, got isErr=%v rpcErr=%+v", tc.tool, tc.args, isErr, rpcErr)
		}
	}

	// Radio error propagation.
	radio.rxErr = errors.New("spi bus timeout")
	if _, isErr, _ := callTool(t, srv, "rf_sweep_band", map[string]any{"band": 0, "sweeps": 1}); !isErr {
		t.Fatal("expected error when radio StartReceive fails during sweep")
	}
	if _, isErr, _ := callTool(t, srv, "rf_read_rssi", map[string]any{"freq_hz": 433_920_000, "samples": 1}); !isErr {
		t.Fatal("expected error when radio StartReceive fails during read_rssi")
	}
	radio.rxErr = nil
	radio.presetErr = errors.New("preset write err")
	if _, isErr, _ := callTool(t, srv, "rf_read_rssi", map[string]any{"freq_hz": 433_920_000, "samples": 1}); !isErr {
		t.Fatal("expected error when ApplyPreset fails")
	}
}

func TestToolSweepBandSimMode(t *testing.T) {
	srv := NewServer(Options{
		Radio:          nil,
		CapturesDir:    t.TempDir(),
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
	})
	res, isErr, rpcErr := callTool(t, srv, "rf_sweep_band", map[string]any{"band": 0, "sweeps": 8})
	if isErr || rpcErr != nil {
		t.Fatalf("sim sweep failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	if bursts := res["bursts"].([]any); len(bursts) == 0 {
		t.Fatal("expected simulated burst in sim mode")
	}
}

func TestToolDecodeOOK(t *testing.T) {
	srv := NewServer(Options{CapturesDir: t.TempDir(), ExtraSavedDirs: []string{}})

	// Build a valid 24-bit Princeton frame: sync gap -10500, then 24 bits (0xA55A01).
	pulses := []int32{-10500}
	var key uint32 = 0xA55A01
	for i := 23; i >= 0; i-- {
		if (key>>uint(i))&1 == 1 {
			pulses = append(pulses, 1050, -350)
		} else {
			pulses = append(pulses, 350, -1050)
		}
	}
	pulses = append(pulses, 350, -10500)

	res, isErr, rpcErr := callTool(t, srv, "rf_decode_ook", map[string]any{"pulses": pulses})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_decode_ook failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	if res["decoded"] != true || res["protocol"] != "Princeton" || res["key_hex"] != "00 00 00 00 00 A5 5A 01" {
		t.Fatalf("unexpected decode result: %+v", res)
	}

	// Noise pulses -> decoded=false (not an error).
	res, isErr, rpcErr = callTool(t, srv, "rf_decode_ook", map[string]any{"pulses": []int32{100, -100, 100}})
	if isErr || rpcErr != nil || res["decoded"] != false {
		t.Fatalf("expected decoded=false for noise, got res=%+v isErr=%v rpcErr=%+v", res, isErr, rpcErr)
	}

	// Empty pulses -> error.
	if _, isErr, _ := callTool(t, srv, "rf_decode_ook", map[string]any{"pulses": []int32{}}); !isErr {
		t.Fatal("expected error for empty pulses")
	}

	// rf_analyze_pulses on jittery/glitched Princeton frame recovers via 1D K-Means.
	noisy := []int32{-10500}
	for b := 23; b >= 0; b-- {
		bit := (uint64(0xA55A01) >> uint(b)) & 1
		if b == 12 {
			// Inject 25us EMI dropout splitting pulse so strict pass fails.
			noisy = append(noisy, 500, -25, 525, -380)
			continue
		}
		if bit == 1 {
			noisy = append(noisy, 920, -470)
		} else {
			noisy = append(noisy, 440, -860)
		}
	}
	noisy = append(noisy, 350, -10500)

	ares, isErr, rpcErr := callTool(t, srv, "rf_analyze_pulses", map[string]any{"pulses": noisy})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_analyze_pulses failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	dec := ares["decode"].(map[string]any)
	if dec["decoded"] != true || dec["denoised"] != true || dec["protocol"] != "Princeton" {
		t.Fatalf("expected K-Means denoised Princeton decode, got %+v", dec)
	}
	cl := ares["clusters"].(map[string]any)
	if cl["valid"] != true || cl["glitches_removed"].(float64) < 1 {
		t.Fatalf("expected valid clusters with >=1 glitch removed, got %+v", cl)
	}
	if _, isErr, _ := callTool(t, srv, "rf_analyze_pulses", map[string]any{"pulses": []int32{}}); !isErr {
		t.Fatal("expected error for empty pulses in rf_analyze_pulses")
	}

	// rf_classify_signal on atmospheric noise -> Noise with is_noise=true.
	noisePulses := []int32{25, -40, 190, -15, 780, -30, 120, -20, 1450, -35, 95, -18, 620, -22, 310, -45}
	cres, isErr, rpcErr := callTool(t, srv, "rf_classify_signal", map[string]any{
		"pulses":  noisePulses,
		"freq_hz": 433_920_000,
		"snr_db":  3.0,
	})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_classify_signal failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	pred := cres["prediction"].(map[string]any)
	if pred["label"] != "Noise" || pred["is_noise"] != true {
		t.Fatalf("expected Noise with is_noise=true, got %+v", pred)
	}
	if _, isErr, _ := callTool(t, srv, "rf_classify_signal", map[string]any{"pulses": []int32{}}); !isErr {
		t.Fatal("expected error for empty pulses in rf_classify_signal")
	}

	// rf_match_fingerprint against a saved Princeton capture.
	_, isErr, rpcErr = callTool(t, srv, "rf_save_capture", map[string]any{
		"name":     "gate_ref.sub",
		"freq_hz":  433_920_000,
		"preset":   "AM650",
		"protocol": "RAW",
		"raw_data": pulses,
	})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_save_capture failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	mres, isErr, rpcErr := callTool(t, srv, "rf_match_fingerprint", map[string]any{
		"pulses":  pulses,
		"freq_hz": 433_920_000,
		"preset":  "AM650",
	})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_match_fingerprint failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	if mres["matched"] != true {
		t.Fatalf("expected fingerprint match, got %+v", mres)
	}
	best := mres["best_match"].(map[string]any)
	if best["name"] != "gate_ref.sub" || best["similarity"].(float64) < 0.99 {
		t.Fatalf("unexpected best_match: %+v", best)
	}
	if _, isErr, _ := callTool(t, srv, "rf_match_fingerprint", map[string]any{"pulses": []int32{}}); !isErr {
		t.Fatal("expected error for empty pulses in rf_match_fingerprint")
	}

	// rf_foxhunt_track on 433.92 MHz returns Kalman state and activity report.
	fres, isErr, rpcErr := callTool(t, srv, "rf_foxhunt_track", map[string]any{
		"freq_hz":     433_920_000,
		"samples":     12,
		"interval_ms": 0,
	})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_foxhunt_track failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	kf := fres["kalman"].(map[string]any)
	if kf["initialized"] != true || kf["trend"] == "" {
		t.Fatalf("unexpected kalman state: %+v", kf)
	}
	if _, isErr, _ := callTool(t, srv, "rf_foxhunt_track", map[string]any{"freq_hz": 100}); !isErr {
		t.Fatal("expected error for out-of-range freq_hz in rf_foxhunt_track")
	}
}

func TestToolsSavedFileLifecycleAndSecurity(t *testing.T) {
	capsDir := t.TempDir()
	subgDir := t.TempDir()

	// Seed a file in extra ~/subg/files dir (must be readable/listed, NEVER deletable).
	externalSub := "Filetype: Flipper SubGhz Key File\nVersion: 1\nFrequency: 433920000\nPreset: FuriHalSubGhzPresetOok650Async\nProtocol: CAME\nBit: 12\nKey: 00 00 00 00 00 00 0A 5F\nTE: 320\n"
	if err := os.WriteFile(filepath.Join(subgDir, "external_gate.sub"), []byte(externalSub), 0o644); err != nil {
		t.Fatalf("write external sub: %v", err)
	}
	// Seed a non-.sub file that must be ignored.
	_ = os.WriteFile(filepath.Join(capsDir, "notes.txt"), []byte("ignore me"), 0o644)

	srv := NewServer(Options{
		CapturesDir:    capsDir,
		ExtraSavedDirs: []string{subgDir},
		Settings:       config.DefaultSettings(),
	})

	// 1. Save a Key capture (without .sub suffix -> auto-appended).
	res, isErr, rpcErr := callTool(t, srv, "rf_save_capture", map[string]any{
		"name":     "garage_key",
		"freq_hz":  433_920_000,
		"preset":   "AM650",
		"protocol": "Princeton",
		"key_hex":  "00 00 00 00 00 A5 5A 01",
		"bits":     24,
		"te":       350,
	})
	if isErr || rpcErr != nil || res["name"] != "garage_key.sub" {
		t.Fatalf("rf_save_capture(key) failed: res=%+v isErr=%v rpcErr=%+v", res, isErr, rpcErr)
	}

	// 2. Save a RAW capture.
	res, isErr, rpcErr = callTool(t, srv, "rf_save_capture", map[string]any{
		"name":     "raw_test.sub",
		"freq_hz":  868_350_000,
		"raw_data": []int32{-10500, 350, -1050, 1050, -350},
	})
	if isErr || rpcErr != nil || res["saved"] != true {
		t.Fatalf("rf_save_capture(raw) failed: res=%+v isErr=%v rpcErr=%+v", res, isErr, rpcErr)
	}

	// 3. List saved -> expects 3 .sub files (2 in captures, 1 in subg).
	res, isErr, rpcErr = callTool(t, srv, "rf_list_saved", map[string]any{})
	if isErr || rpcErr != nil || int(res["count"].(float64)) != 3 {
		t.Fatalf("rf_list_saved: res=%+v isErr=%v rpcErr=%+v", res, isErr, rpcErr)
	}

	// 4. Read saved from captures and from subg.
	res, isErr, rpcErr = callTool(t, srv, "rf_read_saved", map[string]any{"name": "garage_key.sub"})
	if isErr || rpcErr != nil || res["protocol"] != "Princeton" {
		t.Fatalf("rf_read_saved(garage_key.sub) failed: res=%+v isErr=%v", res, isErr)
	}
	res, isErr, rpcErr = callTool(t, srv, "rf_read_saved", map[string]any{"name": "external_gate.sub"})
	if isErr || rpcErr != nil || res["source"] != "subg" {
		t.Fatalf("rf_read_saved(external_gate.sub) failed: res=%+v isErr=%v", res, isErr)
	}

	// 5. Security & adversarial checks.
	traversalNames := []string{"../secret.sub", "sub/dir.sub", `a\b.sub`, "", ".sub", "noext"}
	for _, bad := range traversalNames {
		if _, isErr, _ := callTool(t, srv, "rf_read_saved", map[string]any{"name": bad}); !isErr {
			t.Fatalf("rf_read_saved(%q) should fail", bad)
		}
		if _, isErr, _ := callTool(t, srv, "rf_delete_saved", map[string]any{"name": bad, "confirm": true}); !isErr {
			t.Fatalf("rf_delete_saved(%q) should fail", bad)
		}
	}
	for _, bad := range []string{"../escape.sub", "sub/escape.sub", ""} {
		if _, isErr, _ := callTool(t, srv, "rf_save_capture", map[string]any{
			"name":    bad,
			"freq_hz": 433_920_000,
			"key_hex": "01",
		}); !isErr {
			t.Fatalf("rf_save_capture(%q) should fail", bad)
		}
	}
	// Missing both key_hex and raw_data -> error.
	if _, isErr, _ := callTool(t, srv, "rf_save_capture", map[string]any{
		"name":    "empty.sub",
		"freq_hz": 433_920_000,
	}); !isErr {
		t.Fatal("rf_save_capture without key_hex or raw_data should fail")
	}

	// 6. Delete without confirm=true -> MUST be rejected, file remains.
	if _, isErr, _ := callTool(t, srv, "rf_delete_saved", map[string]any{
		"name":    "garage_key.sub",
		"confirm": false,
	}); !isErr {
		t.Fatal("rf_delete_saved(confirm=false) must fail")
	}
	if _, err := os.Stat(filepath.Join(capsDir, "garage_key.sub")); err != nil {
		t.Fatalf("file was deleted despite confirm=false: %v", err)
	}

	// 7. Delete file that only exists in external subgDir -> MUST fail (only deletes inside capturesDir).
	if _, isErr, _ := callTool(t, srv, "rf_delete_saved", map[string]any{
		"name":    "external_gate.sub",
		"confirm": true,
	}); !isErr {
		t.Fatal("rf_delete_saved must not delete files outside capturesDir")
	}
	if _, err := os.Stat(filepath.Join(subgDir, "external_gate.sub")); err != nil {
		t.Fatalf("external file was deleted: %v", err)
	}

	// 8. Delete with confirm=true inside capturesDir -> succeeds.
	res, isErr, rpcErr = callTool(t, srv, "rf_delete_saved", map[string]any{
		"name":    "garage_key.sub",
		"confirm": true,
	})
	if isErr || rpcErr != nil || res["deleted"] != true {
		t.Fatalf("rf_delete_saved(confirm=true) failed: res=%+v isErr=%v rpcErr=%+v", res, isErr, rpcErr)
	}
	if _, err := os.Stat(filepath.Join(capsDir, "garage_key.sub")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected garage_key.sub to be deleted, stat err=%v", err)
	}
}

func TestConcurrentRadioToolCallsRaceSafe(t *testing.T) {
	radio := &stubRadio{info: cc1101.ChipInfo{PartNumber: 0x00, Version: 0x14}}
	srv := NewServer(Options{
		Radio:          radio,
		CapturesDir:    t.TempDir(),
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
		SettleDelay:    0,
	})

	var wg sync.WaitGroup
	for i := range 6 {
		wg.Add(1)
		go func(idx int) {
			defer wg.Done()
			switch idx % 3 {
			case 0:
				srv.handleLine([]byte(`{"jsonrpc":"2.0","id":1,"method":"tools/call","params":{"name":"rf_sweep_band","arguments":{"band":0,"sweeps":2}}}`))
			case 1:
				srv.handleLine([]byte(`{"jsonrpc":"2.0","id":2,"method":"tools/call","params":{"name":"rf_read_rssi","arguments":{"freq_hz":433920000,"samples":4}}}`))
			case 2:
				srv.handleLine([]byte(`{"jsonrpc":"2.0","id":3,"method":"tools/call","params":{"name":"rf_get_status","arguments":{}}}`))
			}
		}(i)
	}
	wg.Wait()
}

func TestPulseNetAndSpectrumMCPTools(t *testing.T) {
	srv := NewServer(Options{
		CapturesDir:    t.TempDir(),
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
		SettleDelay:    0,
	})

	infoRes, isErr, rpcErr := callTool(t, srv, "rf_model_info", map[string]any{})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_model_info failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	pulseInfo, ok := infoRes["pulsenet_rf"].(map[string]any)
	if !ok || pulseInfo["source"] != "embedded" {
		t.Fatalf("unexpected pulsenet_rf info: %+v", infoRes)
	}
	specInfo, ok := infoRes["spectrumnet"].(map[string]any)
	if !ok || specInfo["source"] != "embedded" {
		t.Fatalf("unexpected spectrumnet info: %+v", infoRes)
	}

	// Build a 32x36 CW Jammer waterfall to verify rf_classify_spectrum.
	wf := make([][]int, 32)
	for r := range 32 {
		row := make([]int, 36)
		for c := range 36 {
			row[c] = -104
		}
		row[18] = -48
		row[17] = -72
		row[19] = -72
		wf[r] = row
	}
	specRes, isErr, rpcErr := callTool(t, srv, "rf_classify_spectrum", map[string]any{"waterfall_dbm": wf})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_classify_spectrum failed: isErr=%v rpcErr=%+v", isErr, rpcErr)
	}
	pred, ok := specRes["prediction"].(map[string]any)
	if !ok || pred["label"] != "CW_Jammer" || pred["is_alert"] != true {
		t.Fatalf("expected CW_Jammer alert, got %+v", specRes)
	}

	// Empty waterfall must return tool error cleanly.
	if _, isErr, _ := callTool(t, srv, "rf_classify_spectrum", map[string]any{"waterfall_dbm": [][]int{}}); !isErr {
		t.Fatal("expected tool error for empty waterfall")
	}
}
