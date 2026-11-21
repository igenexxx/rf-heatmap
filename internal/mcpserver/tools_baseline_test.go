package mcpserver

import (
	"testing"

	"rf-heatmap/internal/config"
)

func TestRoomBaselineMCPToolsLifecycleAndGuards(t *testing.T) {
	srv := NewServer(Options{
		CapturesDir:    t.TempDir(),
		BaselinesDir:   t.TempDir(),
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
	})

	// 1. Empty list initially.
	res, isErr, rpcErr := callTool(t, srv, "rf_list_room_baselines", map[string]any{})
	if isErr || rpcErr != nil || int(res["count"].(float64)) != 0 {
		t.Fatalf("initial rf_list_room_baselines = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 2. Save baseline via live SIM sweep.
	res, isErr, rpcErr = callTool(t, srv, "rf_save_room_baseline", map[string]any{
		"name":   "office_433",
		"band":   0,
		"sweeps": 4,
	})
	if isErr || rpcErr != nil || res["saved"] != true || int(res["samples"].(float64)) != 4 || res["source"] != "sim" {
		t.Fatalf("rf_save_room_baseline(sim) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 3. Save baseline via inline frames.
	res, isErr, rpcErr = callTool(t, srv, "rf_save_room_baseline", map[string]any{
		"name": "lab_quiet",
		"band": 0,
		"frames": [][]float32{
			{-104, -103, -104, -105},
			{-102, -103, -104, -103},
		},
	})
	if isErr || rpcErr != nil || res["saved"] != true || int(res["bins"].(float64)) != 4 || res["source"] != "inline" {
		t.Fatalf("rf_save_room_baseline(inline) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 4. Reject invalid name and out-of-range band.
	if _, isErr, _ := callTool(t, srv, "rf_save_room_baseline", map[string]any{"name": "../evil", "band": 0}); !isErr {
		t.Fatal("expected path traversal rejection on rf_save_room_baseline")
	}
	if _, isErr, _ := callTool(t, srv, "rf_save_room_baseline", map[string]any{"name": "bad_band", "band": 99}); !isErr {
		t.Fatal("expected out-of-range band rejection")
	}

	// 5. List returns both saved baselines.
	res, isErr, rpcErr = callTool(t, srv, "rf_list_room_baselines", map[string]any{})
	if isErr || rpcErr != nil || int(res["count"].(float64)) != 2 {
		t.Fatalf("rf_list_room_baselines = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 6. Diff against inline live_dbm with one elevated (+5 dB) and one new emitter (+20 dB).
	res, isErr, rpcErr = callTool(t, srv, "rf_diff_room_baseline", map[string]any{
		"name":           "lab_quiet",
		"live_dbm":       []float32{-103, -98, -84, -104},
		"only_anomalies": true,
	})
	if isErr || rpcErr != nil {
		t.Fatalf("rf_diff_room_baseline(inline) failed: res=%+v err=%+v", res, rpcErr)
	}
	if int(res["new_emitters"].(float64)) != 1 || int(res["elevated"].(float64)) != 1 {
		t.Fatalf("unexpected anomaly counts: %+v", res)
	}
	bins := res["bins"].([]any)
	if len(bins) != 2 {
		t.Fatalf("only_anomalies=true returned %d bins, want 2", len(bins))
	}

	// 7. Diff via live SIM sweep fallback (no live_dbm provided).
	res, isErr, rpcErr = callTool(t, srv, "rf_diff_room_baseline", map[string]any{"name": "office_433"})
	if isErr || rpcErr != nil || res["source"] != "sim" || int(res["total_bins"].(float64)) != 36 {
		t.Fatalf("rf_diff_room_baseline(sim) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 8. Delete guard rejects confirm=false and path traversal.
	if _, isErr, _ := callTool(t, srv, "rf_delete_room_baseline", map[string]any{"name": "lab_quiet", "confirm": false}); !isErr {
		t.Fatal("rf_delete_room_baseline must reject confirm=false")
	}
	if _, isErr, _ := callTool(t, srv, "rf_delete_room_baseline", map[string]any{"name": "../lab_quiet", "confirm": true}); !isErr {
		t.Fatal("rf_delete_room_baseline must reject path traversal")
	}

	// 9. Delete succeeds with confirm=true.
	res, isErr, rpcErr = callTool(t, srv, "rf_delete_room_baseline", map[string]any{"name": "lab_quiet", "confirm": true})
	if isErr || rpcErr != nil || res["deleted"] != true {
		t.Fatalf("rf_delete_room_baseline(confirm=true) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}
	res, _, _ = callTool(t, srv, "rf_list_room_baselines", map[string]any{})
	if int(res["count"].(float64)) != 1 {
		t.Fatalf("expected 1 remaining baseline after delete, got %+v", res)
	}
}
