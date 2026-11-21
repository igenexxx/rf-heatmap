package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"rf-heatmap/internal/config"
	"rf-heatmap/internal/subfile"
)

func TestToolPurgeNoise(t *testing.T) {
	dir := t.TempDir()
	noisePulses := make([]int32, 0, 80)
	for i := range 40 {
		noisePulses = append(noisePulses, int32(45+(i*37)%290), -int32(55+(i*53)%310))
	}
	noiseSub := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File", Version: 1, FreqHz: 433_920_000,
		Preset: "FuriHalSubGhzPresetOok650Async", Protocol: "RAW", RawData: noisePulses,
	}
	keySub := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File", Version: 1, FreqHz: 433_920_000,
		Preset: "FuriHalSubGhzPresetOok650Async", Protocol: "Princeton",
		Bit: 24, Key: "00 00 00 00 00 12 34 56", TE: 350,
	}
	noisePath := filepath.Join(dir, "noise_1.sub")
	keyPath := filepath.Join(dir, "keep_key.sub")
	if err := os.WriteFile(noisePath, []byte(noiseSub.Format()), 0o644); err != nil {
		t.Fatalf("write noise: %v", err)
	}
	if err := os.WriteFile(keyPath, []byte(keySub.Format()), 0o644); err != nil {
		t.Fatalf("write key: %v", err)
	}

	srv := NewServer(Options{
		CapturesDir:    dir,
		ExtraSavedDirs: []string{},
		Settings:       config.DefaultSettings(),
	})

	t.Run("dry_run_default_does_not_delete", func(t *testing.T) {
		res, rpcErr := srv.dispatchTool("rf_purge_noise", json.RawMessage(`{}`))
		if rpcErr != nil || res.IsError {
			t.Fatalf("unexpected error: rpcErr=%v res=%+v", rpcErr, res)
		}
		var payload struct {
			DryRun       bool `json:"dry_run"`
			MatchedCount int  `json:"matched_count"`
			DeletedCount int  `json:"deleted_count"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if !payload.DryRun || payload.MatchedCount != 1 || payload.DeletedCount != 0 {
			t.Fatalf("unexpected dry_run payload: %+v", payload)
		}
		if _, err := os.Stat(noisePath); err != nil {
			t.Fatalf("noise file should not be deleted in dry_run: %v", err)
		}
	})

	t.Run("refuses_delete_without_confirm", func(t *testing.T) {
		res, rpcErr := srv.dispatchTool("rf_purge_noise", json.RawMessage(`{"dry_run":false,"confirm":false}`))
		if rpcErr != nil || !res.IsError || !strings.Contains(res.Content[0].Text, "confirm=true") {
			t.Fatalf("expected confirm=true tool error, got rpcErr=%v res=%+v", rpcErr, res)
		}
	})

	t.Run("deletes_only_noise_when_confirmed", func(t *testing.T) {
		res, rpcErr := srv.dispatchTool("rf_purge_noise", json.RawMessage(`{"dry_run":false,"confirm":true}`))
		if rpcErr != nil || res.IsError {
			t.Fatalf("unexpected error: rpcErr=%v res=%+v", rpcErr, res)
		}
		var payload struct {
			DryRun       bool `json:"dry_run"`
			DeletedCount int  `json:"deleted_count"`
		}
		if err := json.Unmarshal([]byte(res.Content[0].Text), &payload); err != nil {
			t.Fatalf("unmarshal: %v", err)
		}
		if payload.DryRun || payload.DeletedCount != 1 {
			t.Fatalf("unexpected delete payload: %+v", payload)
		}
		if _, err := os.Stat(noisePath); !os.IsNotExist(err) {
			t.Fatalf("expected noise file deleted, err=%v", err)
		}
		if _, err := os.Stat(keyPath); err != nil {
			t.Fatalf("expected key file preserved, err=%v", err)
		}
	})

	t.Run("missing_captures_dir_returns_error", func(t *testing.T) {
		noDirSrv := NewServer(Options{ExtraSavedDirs: []string{}, Settings: config.DefaultSettings()})
		res, rpcErr := noDirSrv.dispatchTool("rf_purge_noise", json.RawMessage(`{}`))
		if rpcErr != nil || !res.IsError {
			t.Fatalf("expected tool error for unconfigured captures dir, got rpcErr=%v res=%+v", rpcErr, res)
		}
	})
}
