package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/config"
	"rf-heatmap/internal/subfile"
)

func TestMCPDatabaseSeparationPromoteAndTransmit(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "captures")
	dbDir := filepath.Join(root, "databases")

	writeSub := func(path, key string) {
		t.Helper()
		sf := &subfile.SubFile{
			FileType: "Flipper SubGhz Key File",
			Version:  1,
			FreqHz:   433_920_000,
			Preset:   "FuriHalSubGhzPresetOok650Async",
			Protocol: "Princeton",
			Bit:      24,
			Key:      key,
			TE:       350,
		}
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(sf.Format()), 0o644); err != nil {
			t.Fatalf("write: %v", err)
		}
	}

	writeSub(filepath.Join(userDir, "my_key.sub"), "00 00 00 00 00 11 22 33")
	writeSub(filepath.Join(dbDir, "fans", "fan_pwr.sub"), "00 00 00 00 00 AA BB CC")

	radio := &stubRadio{info: cc1101.ChipInfo{Version: 0x14}}
	srv := NewServer(Options{
		Radio:          radio,
		CapturesDir:    userDir,
		DatabasesDir:   dbDir,
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
	})

	// 1. rf_list_databases returns external db:fans (RO) and user (RW).
	res, rpcErr := srv.dispatchTool("rf_list_databases", nil)
	if rpcErr != nil || res.IsError {
		t.Fatalf("rf_list_databases failed: res=%+v err=%+v", res, rpcErr)
	}
	if !strings.Contains(res.Content[0].Text, `"pack_id":"db:fans"`) || !strings.Contains(res.Content[0].Text, `"pack_id":"user"`) {
		t.Fatalf("unexpected databases output: %s", res.Content[0].Text)
	}

	// 2. rf_list_saved filtered by pack=db:fans.
	res, rpcErr = srv.dispatchTool("rf_list_saved", json.RawMessage(`{"pack":"db:fans"}`))
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, "fan_pwr.sub") {
		t.Fatalf("rf_list_saved(pack=db:fans) failed: %+v %+v", res, rpcErr)
	}

	// 3. rf_transmit_saved without confirm_rf=true MUST be rejected.
	res, rpcErr = srv.dispatchTool("rf_transmit_saved", json.RawMessage(`{"pack":"db:fans","name":"fan_pwr.sub","confirm_rf":false}`))
	if rpcErr != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "confirm_rf=true") {
		t.Fatalf("expected confirm_rf guard, got res=%+v err=%+v", res, rpcErr)
	}

	// 4. rf_transmit_saved with confirm_rf=true succeeds and invokes radio.
	res, rpcErr = srv.dispatchTool("rf_transmit_saved", json.RawMessage(`{"pack":"db:fans","name":"fan_pwr.sub","confirm_rf":true}`))
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"transmitted":true`) {
		t.Fatalf("rf_transmit_saved failed: res=%+v err=%+v", res, rpcErr)
	}
	if radio.lastFreq != 433_920_000 {
		t.Fatalf("expected radio tuned to 433920000, got %d", radio.lastFreq)
	}

	// 5. rf_promote_to_user copies fan_pwr.sub into captures/ safely.
	res, rpcErr = srv.dispatchTool("rf_promote_to_user", json.RawMessage(`{"pack":"db:fans","name":"fan_pwr.sub"}`))
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"promoted":true`) {
		t.Fatalf("rf_promote_to_user failed: res=%+v err=%+v", res, rpcErr)
	}
	if _, err := os.Stat(filepath.Join(userDir, "fan_pwr.sub")); err != nil {
		t.Fatalf("expected promoted file in userDir: %v", err)
	}

	// 6. Virtual Generator Pack: rf_transmit_saved & rf_promote_to_user on gen:came12-433 (no file on disk).
	res, rpcErr = srv.dispatchTool("rf_transmit_saved", json.RawMessage(`{"pack":"gen:came12-433","name":"came12_12A.sub","confirm_rf":true}`))
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"protocol":"CAME"`) {
		t.Fatalf("rf_transmit_saved(gen:came12-433) failed: res=%+v err=%+v", res, rpcErr)
	}
	res, rpcErr = srv.dispatchTool("rf_promote_to_user", json.RawMessage(`{"pack":"gen:came12-433","name":"came12_12A.sub"}`))
	if rpcErr != nil || res.IsError || !strings.Contains(res.Content[0].Text, `"promoted":true`) {
		t.Fatalf("rf_promote_to_user(gen:came12-433) failed: res=%+v err=%+v", res, rpcErr)
	}
	if _, err := os.Stat(filepath.Join(userDir, "came12_12A.sub")); err != nil {
		t.Fatalf("expected promoted generated .sub in userDir: %v", err)
	}
}
