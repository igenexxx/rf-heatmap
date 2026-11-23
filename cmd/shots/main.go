package main

import (
	"flag"
	"image"
	"image/color"
	"image/png"
	"math"
	"os"
	"path/filepath"

	"rf-heatmap/internal/library"
	"rf-heatmap/internal/subfile"
	"rf-heatmap/internal/ui"
)

func main() {
	outDir := flag.String("out", "docs/images", "Output directory for screenshots")
	scale := flag.Int("scale", 2, "Integer scale factor (1 = 320x170 Store size, 2 = 640x340 docs)")
	iconPath := flag.String("icon", "", "Optional output path for 256x256 Store PNG icon")
	flag.Parse()

	if err := os.MkdirAll(*outDir, 0o755); err != nil {
		panic(err)
	}

	tmpRoot, err := os.MkdirTemp("", "rfshots")
	if err != nil {
		panic(err)
	}
	defer os.RemoveAll(tmpRoot)
	tmpCaps := filepath.Join(tmpRoot, "captures")
	tmpDBs := filepath.Join(tmpRoot, "databases")
	_ = os.MkdirAll(tmpCaps, 0o755)
	_ = os.MkdirAll(filepath.Join(tmpDBs, "fans"), 0o755)

	sample1 := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   433_920_000,
		Preset:   "FuriHalSubGhzPresetOok650Async",
		Protocol: "Princeton",
		Bit:      24,
		Key:      "00 00 00 00 00 A5 5A 01",
		TE:       350,
	}
	sample2 := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File",
		Version:  1,
		FreqHz:   868_350_000,
		Preset:   "FuriHalSubGhzPresetOok270Async",
		Protocol: "RAW",
		RawData:  []int32{-10500, 350, -1050, 1050, -350},
	}
	sample3 := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   433_920_000,
		Preset:   "FuriHalSubGhzPresetOok650Async",
		Protocol: "CAME",
		Bit:      12,
		Key:      "00 00 00 00 00 00 0A 5F",
		TE:       320,
	}
	_ = os.WriteFile(filepath.Join(tmpCaps, "garage_gate.sub"), []byte(sample1.Format()), 0o644)
	_ = os.WriteFile(filepath.Join(tmpCaps, "weather_868.sub"), []byte(sample2.Format()), 0o644)
	_ = os.WriteFile(filepath.Join(tmpCaps, "barrier_came.sub"), []byte(sample3.Format()), 0o644)

	// Sample external read-only database pack for PROBE screenshot.
	_ = os.WriteFile(filepath.Join(tmpDBs, "fans", "fan_01_power.sub"), []byte(sample1.Format()), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDBs, "fans", "fan_02_low.sub"), []byte(sample1.Format()), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDBs, "fans", "fan_03_med.sub"), []byte(sample1.Format()), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDBs, "fans", "fan_04_high.sub"), []byte(sample1.Format()), 0o644)
	_ = os.WriteFile(filepath.Join(tmpDBs, "fans", "fan_05_light.sub"), []byte(sample3.Format()), 0o644)

	app := ui.NewApp(nil, tmpCaps)
	defer app.Close()
	app.SetLibraryStore(library.NewStore(tmpCaps, tmpDBs, nil))
	c, err := ui.NewCanvas("")
	if err != nil {
		panic(err)
	}

	// 1. HOME (Simple Mode)
	app.Settings.ExpertMode = false
	app.SwitchMode(ui.ModeHome)
	app.ToastText = ""
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "home.png"), *scale)

	// 2. READ & REPLAY (Step 2 captured Princeton key)
	app.SwitchMode(ui.ModeRead)
	app.ActiveFreq = 433_920_000
	app.LastDecoded = &subfile.DecodedKey{
		Protocol: "Princeton", Bits: 24, KeyHex: "00 00 00 00 00 A5 5A 01", TE: 350,
	}
	app.ReadCaptured = true
	app.ToastText = ""
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "read.png"), *scale)
	app.ReadCaptured = false
	app.LastDecoded = nil

	// 3. SPECTRUM MAP (HEATMAP)
	app.SwitchMode(ui.ModeHeatmap)
	for range 56 {
		app.Step()
	}
	app.ToastText = ""
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "heatmap.png"), *scale)

	// 4. MY CAPTURES (Simple Mode)
	app.SwitchMode(ui.ModeSaved)
	app.ToastText = ""
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "saved.png"), *scale)

	// 5. SENSORS (TPMS / Weather)
	app.EnterSensors()
	for range 25 {
		app.Step()
	}
	app.ToastText = ""
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "sensors.png"), *scale)
	app.ExitSensors()

	// 6. SETTINGS (Simple Mode)
	app.SwitchMode(ui.ModeSettings)
	app.SettingsIdx = 0
	app.ToastText = ""
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "settings.png"), *scale)

	// Expert Mode screens for Learning Portal & docs
	app.Settings.ExpertMode = true
	app.SwitchMode(ui.ModePulse)
	app.HandleKey(ui.KeyNav4)
	for range 48 {
		app.Step()
	}
	app.ToastText = ""
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "pulse.png"), *scale)

	app.SwitchMode(ui.ModeDecode)
	app.ToastText = ""
	for range 12 {
		app.Step()
	}
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "decode.png"), *scale)

	// ModeProbe: simulate paused auto-probe after 3 sent signals, rewound to #2.
	app.SwitchMode(ui.ModeProbe)
	app.Probe.OnTXStarted(0)
	app.Probe.OnTXStarted(1)
	app.Probe.OnTXStarted(2)
	app.Probe.Status = library.ProbePaused
	app.Probe.ActiveTX = false
	app.Probe.CursorIdx = 1
	app.ToastText = ""
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "probe.png"), *scale)

	app.Settings.ExpertMode = false
	app.SwitchMode(ui.ModeHome)
	app.ToastText = ""
	app.HandleKey(ui.KeyHelp)
	app.Render(c)
	savePNG(c, filepath.Join(*outDir, "help.png"), *scale)

	if *iconPath != "" {
		if err := os.MkdirAll(filepath.Dir(*iconPath), 0o755); err != nil {
			panic(err)
		}
		generateIcon(*iconPath)
	}
}

func savePNG(c *ui.Canvas, path string, scale int) {
	if scale < 1 {
		scale = 1
	}
	img := image.NewRGBA(image.Rect(0, 0, ui.ScreenW*scale, ui.ScreenH*scale))
	for y := 0; y < ui.ScreenH; y++ {
		for x := 0; x < ui.ScreenW; x++ {
			rgb := rgb565ToRGBA(c.PixelAt(x, y))
			for dy := 0; dy < scale; dy++ {
				for dx := 0; dx < scale; dx++ {
					img.SetRGBA(x*scale+dx, y*scale+dy, rgb)
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func generateIcon(path string) {
	const sz = 256
	img := image.NewRGBA(image.Rect(0, 0, sz, sz))
	// Full-bleed dark slate background (no rounded corners/borders per Store guidelines).
	for y := 0; y < sz; y++ {
		for x := 0; x < sz; x++ {
			img.SetRGBA(x, y, color.RGBA{R: 8, G: 12, B: 22, A: 255})
		}
	}
	// Draw stylized RF spectrum bars + thermal heatmap glow.
	barHeights := []int{34, 52, 92, 138, 178, 120, 68, 108, 154, 86, 48, 30}
	barW := 16
	gap := 4
	startX := (sz - len(barHeights)*(barW+gap) + gap) / 2
	baseY := 214
	for i, h := range barHeights {
		bx := startX + i*(barW+gap)
		for dy := 0; dy < h; dy++ {
			t := float64(dy) / 180.0
			dbm := int32(-105 + t*70)
			col := rgb565ToRGBA(ui.ThermalColor(dbm))
			for dx := 0; dx < barW; dx++ {
				img.SetRGBA(bx+dx, baseY-dy, col)
			}
		}
		// Cyan peak cap on top.
		for dx := 0; dx < barW; dx++ {
			for k := 0; k < 4; k++ {
				img.SetRGBA(bx+dx, baseY-h-k, color.RGBA{R: 0, G: 235, B: 255, A: 255})
			}
		}
	}
	// Subtle concentric RF wave arcs in top-right.
	cx, cy := 128.0, 56.0
	for y := 12; y < 95; y++ {
		for x := 40; x < 216; x++ {
			d := math.Hypot(float64(x)-cx, float64(y)-cy)
			if math.Abs(d-26) < 2.2 || math.Abs(d-44) < 2.2 {
				if float64(y) < cy+10 {
					img.SetRGBA(x, y, color.RGBA{R: 0, G: 235, B: 255, A: 255})
				}
			}
		}
	}
	f, err := os.Create(path)
	if err != nil {
		panic(err)
	}
	defer f.Close()
	if err := png.Encode(f, img); err != nil {
		panic(err)
	}
}

func rgb565ToRGBA(px uint16) color.RGBA {
	r := uint8((px >> 11) & 0x1F)
	g := uint8((px >> 5) & 0x3F)
	b := uint8(px & 0x1F)
	return color.RGBA{
		R: (r << 3) | (r >> 2),
		G: (g << 2) | (g >> 4),
		B: (b << 3) | (b >> 2),
		A: 255,
	}
}
