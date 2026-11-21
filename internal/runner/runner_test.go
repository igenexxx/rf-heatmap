package runner

import (
	"bytes"
	"context"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"rf-heatmap/internal/config"
)

func TestDefaultCapturesDir(t *testing.T) {
	dir := DefaultCapturesDir()
	if dir == "" || !strings.HasSuffix(dir, "captures") {
		t.Fatalf("unexpected DefaultCapturesDir: %q", dir)
	}
}

func TestRunSimModeContextCancel(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 120*time.Millisecond)
	defer cancel()

	var out, errBuf bytes.Buffer
	err := Run(ctx, Config{
		SimMode:     true,
		FBPath:      "",
		CapturesDir: t.TempDir(),
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run(SimMode) returned unexpected error: %v", err)
	}
}

func TestRunProbeInvalidSPI(t *testing.T) {
	var out, errBuf bytes.Buffer
	err := Run(context.Background(), Config{
		ProbeOnly: true,
		SPIPath:   "/dev/nonexistent_cc1101_test",
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected error probing nonexistent SPI device")
	}
}

func TestRunMCPDeniedWhenDisabled(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	// Missing config defaults to mcp_enabled=false.
	var out, errBuf bytes.Buffer
	err := Run(context.Background(), Config{
		MCPMode:    true,
		SimMode:    true,
		ConfigPath: cfgPath,
		Stdin:      strings.NewReader(""),
	}, &out, &errBuf)
	if err == nil {
		t.Fatal("expected error when MCP is disabled")
	}
	const want = "MCP server is disabled. Enable it in RF Heatmap -> SET (Settings) first."
	if err.Error() != want {
		t.Fatalf("error = %q, want %q", err.Error(), want)
	}

	// Explicitly saved mcp_enabled=false must also be denied.
	if err := config.Save(cfgPath, config.Settings{MCPEnabled: false, AutoSquelch: true, AutoLock: true, SquelchK: 3.5}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}
	err = Run(context.Background(), Config{
		MCPMode:    true,
		SimMode:    true,
		ConfigPath: cfgPath,
		Stdin:      strings.NewReader(""),
	}, &out, &errBuf)
	if err == nil || err.Error() != want {
		t.Fatalf("explicitly disabled MCP error = %v, want %q", err, want)
	}
}

func TestRunMCPAllowedWhenEnabled(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	if err := config.Save(cfgPath, config.Settings{
		MCPEnabled:  true,
		AutoSquelch: true,
		AutoLock:    true,
		SquelchK:    4.0,
	}); err != nil {
		t.Fatalf("config.Save: %v", err)
	}

	input := strings.Join([]string{
		`{"jsonrpc":"2.0","id":1,"method":"initialize","params":{}}`,
		`{"jsonrpc":"2.0","method":"notifications/initialized"}`,
		`{"jsonrpc":"2.0","id":2,"method":"tools/list"}`,
	}, "\n") + "\n"

	var out, errBuf bytes.Buffer
	err := Run(context.Background(), Config{
		MCPMode:     true,
		SimMode:     true,
		ConfigPath:  cfgPath,
		CapturesDir: t.TempDir(),
		Stdin:       strings.NewReader(input),
	}, &out, &errBuf)
	if err != nil {
		t.Fatalf("Run(MCPMode, enabled) returned unexpected error: %v", err)
	}

	lines := strings.Split(strings.TrimSpace(out.String()), "\n")
	if len(lines) != 2 {
		t.Fatalf("expected 2 JSON-RPC responses, got %d: %s", len(lines), out.String())
	}
	var initResp map[string]any
	if err := json.Unmarshal([]byte(lines[0]), &initResp); err != nil {
		t.Fatalf("invalid JSON in init response: %v", err)
	}
	res := initResp["result"].(map[string]any)
	if res["protocolVersion"] != "2024-11-05" {
		t.Fatalf("unexpected protocolVersion: %v", res["protocolVersion"])
	}
}
