package main

import (
	"flag"
	"fmt"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/ui"
)

func main() {
	simFlag := flag.Bool("sim", false, "Force RF simulator mode without CC1101 hardware")
	fbPath := flag.String("fb", "/dev/fb0", "Linux framebuffer device path")
	spiPath := flag.String("spi", "/dev/spidev0.1", "CC1101 spidev path")
	capturesDir := flag.String("captures", defaultCapturesDir(), "Directory for saved .sub captures")
	flag.Parse()

	canvas, err := ui.NewCanvas(*fbPath)
	if err != nil {
		fmt.Fprintf(os.Stderr, "rf-heatmap: %v\n", err)
		os.Exit(1)
	}
	defer canvas.Close()

	var radio *cc1101.Radio
	if !*simFlag {
		if tr, err := cc1101.OpenLinuxTransport(*spiPath, 500_000); err == nil {
			if r, err := cc1101.Open(tr, cc1101.DefaultCrystalHz); err == nil {
				radio = r
				defer radio.Close()
			} else {
				_ = tr.Close()
			}
		}
	}

	keys, stopInput := ui.StartEvdevReader()
	defer stopInput()

	app := ui.NewApp(radio, *capturesDir)
	if radio != nil {
		info := radio.Info()
		app.SetToast(fmt.Sprintf("CC1101 v0x%02X Ready", info.Version))
	} else {
		app.SetToast("SIM Mode (No CC1101)")
	}

	sigCh := make(chan os.Signal, 2)
	signal.Notify(sigCh, syscall.SIGINT, syscall.SIGTERM)

	ticker := time.NewTicker(45 * time.Millisecond)
	defer ticker.Stop()

	app.Render(canvas)
	_ = canvas.Flush()

	for {
		select {
		case <-sigCh:
			return
		case act := <-keys:
			if app.HandleKey(act) {
				return
			}
			app.Render(canvas)
			_ = canvas.Flush()
		case <-ticker.C:
			app.Step()
			app.Render(canvas)
			_ = canvas.Flush()
		}
	}
}

func defaultCapturesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return "/tmp/rf-heatmap/captures"
	}
	return filepath.Join(home, "rf-heatmap", "captures")
}
