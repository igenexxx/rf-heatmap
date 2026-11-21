package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"rf-heatmap/internal/config"
	"rf-heatmap/internal/dbsync"
	"rf-heatmap/internal/runner"
)

func main() {
	cfg := runner.Config{}
	flag.BoolVar(&cfg.SimMode, "sim", false, "Force RF simulator mode without CC1101 hardware")
	flag.BoolVar(&cfg.ProbeOnly, "probe", false, "Probe CC1101 & Smart Squelch baseline on stdout, then exit")
	flag.IntVar(&cfg.ProbeBand, "band", 0, "Band index for --probe (0=433, 1=868, 2=315, 3=915, 4=ALL ISM)")
	flag.IntVar(&cfg.ProbeSweeps, "sweeps", 10, "Number of sweeps for --probe")
	flag.BoolVar(&cfg.MCPMode, "mcp", false, "Run stdio MCP server (requires opt-in in SET settings)")
	flag.BoolVar(&cfg.SyncOnly, "sync", false, "Sync enabled remote .sub database sources and exit")
	flag.StringVar(&cfg.ConfigPath, "config", config.DefaultPath(), "Path to settings JSON file")
	flag.StringVar(&cfg.SourcesPath, "sources", dbsync.DefaultSourcesPath(), "Path to remote DB sources JSON file")
	flag.StringVar(&cfg.FBPath, "fb", "/dev/fb0", "Linux framebuffer device path")
	flag.StringVar(&cfg.SPIPath, "spi", "/dev/spidev0.1", "CC1101 spidev path")
	flag.StringVar(&cfg.CapturesDir, "captures", runner.DefaultCapturesDir(), "Directory for saved .sub captures")
	flag.Parse()

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	if err := runner.Run(ctx, cfg, os.Stdout, os.Stderr); err != nil {
		fmt.Fprintf(os.Stderr, "rf-heatmap: %v\n", err)
		os.Exit(1)
	}
}
