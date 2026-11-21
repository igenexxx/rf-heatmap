package ui

import (
	"fmt"
	"math"
	"math/rand"
	"time"
)

const (
	heatmapChartX      = 6
	heatmapChartW      = 308
	heatmapBarTop      = 32
	heatmapBarH        = 46
	heatmapWfTop       = 82
	heatmapLockPeakDBm = -85
	sweepSettleDelay   = 1200 * time.Microsecond
)

func (a *App) handleHeatmapKey(act KeyAction) {
	freqs := DefaultBands[a.BandIdx].FreqsHz
	switch act {
	case KeyLeft:
		a.moveHeatmapCursor(freqs, -1)
	case KeyRight:
		a.moveHeatmapCursor(freqs, 1)
	case KeyUp, KeyNav5:
		a.cycleBand()
	case KeyDown:
		a.clearPeakHold()
	case KeyNav4, KeySpace:
		a.Paused = !a.Paused
	case KeyNav6, KeyEnter:
		a.lockPeakOrCursor(freqs)
	case KeyNav7:
		a.cyclePreset()
	}
}

func (a *App) moveHeatmapCursor(freqs []uint32, delta int) {
	a.CursorCol = min(len(freqs)-1, max(0, a.CursorCol+delta))
	a.ActiveFreq = freqs[a.CursorCol]
	a.manualCursorCooldown = manualCursorHoldTick
}

func (a *App) clearPeakHold() {
	for i := range a.PeakHold {
		a.PeakHold[i] = defaultFloorDBm
	}
	a.PeakDBm = defaultWaterfallDBm
	a.SetToast("Peaks cleared")
}

func (a *App) lockPeakOrCursor(freqs []uint32) {
	if a.PeakDBm > heatmapLockPeakDBm {
		a.ActiveFreq = a.PeakFreqHz
	} else {
		a.ActiveFreq = freqs[a.CursorCol]
	}
	if a.LiveBaseline != nil {
		a.LiveBaseline.Reset()
	}
	a.Mode = ModePulse
	a.SetToast(fmt.Sprintf("Locked %.2f MHz", float64(a.ActiveFreq)/1e6))
}

func (a *App) stepHeatmap() {
	freqs := DefaultBands[a.BandIdx].FreqsHz
	n := min(len(freqs), maxCols)

	var rowMax int32 = -120
	rowMaxFreq := freqs[0]

	for i := range n {
		rssi := a.sampleChannelRSSI(freqs[i], i, n)
		a.SweepRSSI[i] = rssi
		a.updatePeakHold(i, rssi)
		if rssi > rowMax {
			rowMax = rssi
			rowMaxFreq = freqs[i]
		}
		a.Waterfall[a.WfHead][i] = rssi
	}
	a.WfHead = (a.WfHead + 1) % waterfallRows
	if rowMax >= a.PeakDBm || a.tick%20 == 0 {
		a.PeakDBm = rowMax
		a.PeakFreqHz = rowMaxFreq
	}
	a.updateSweepBaseline(freqs, n)
}

func (a *App) sampleChannelRSSI(freqHz uint32, idx, n int) int32 {
	if a.Radio == nil {
		return a.simSweepRSSI(freqHz, idx, n)
	}
	if err := a.Radio.StartReceive(freqHz); err == nil {
		time.Sleep(sweepSettleDelay)
		if v, err := a.Radio.ReadRSSIDBm(); err == nil {
			return v
		}
	}
	return -106
}

func (a *App) updatePeakHold(idx int, rssi int32) {
	if rssi > a.PeakHold[idx] {
		a.PeakHold[idx] = rssi
	} else if a.tick%12 == 0 && a.PeakHold[idx] > rssi {
		a.PeakHold[idx]--
	}
}

func (a *App) updateSweepBaseline(freqs []uint32, n int) {
	if a.SweepBaseline == nil {
		return
	}
	a.SweepBaseline.Update(a.SweepRSSI[:n])
	if a.manualCursorCooldown > 0 {
		a.manualCursorCooldown--
	}
	evt, ok := a.SweepBaseline.DetectBurst(a.SweepRSSI[:n])
	if !ok {
		a.HasBurst = false
		return
	}
	a.LastBurst = evt
	a.HasBurst = true
	if a.AutoLock && a.manualCursorCooldown == 0 && evt.Bin < n {
		a.CursorCol = evt.Bin
		a.ActiveFreq = freqs[evt.Bin]
	}
}

func (a *App) simSweepRSSI(freqHz uint32, idx, n int) int32 {
	noise := int32(-106 + rand.Intn(6))
	// Simulate realistic ISM signals: 433.92 MHz keyfob, 433.42 MHz Somfy, 868.35 MHz sensor.
	phase := float64(a.tick) * 0.25
	center := int(float64(n) * 0.5)
	if idx == center && (a.tick%14 < 5) {
		return -46 + int32(rand.Intn(6))
	}
	if idx == center-7 && math.Sin(phase) > 0.3 {
		return -62 + int32(rand.Intn(5))
	}
	if freqHz >= 868_200_000 && freqHz <= 868_500_000 && a.tick%10 < 4 {
		return -52 + int32(rand.Intn(6))
	}
	if idx == (int(a.tick/3)%n) && a.tick%7 == 0 {
		return -68 + int32(rand.Intn(8))
	}
	return noise
}

func (a *App) drawHeatmap(c *Canvas) {
	band := DefaultBands[a.BandIdx]
	n := min(len(band.FreqsHz), maxCols)
	a.drawHeatmapInfoStrip(c, band)
	a.drawHeatmapBars(c, n)
	a.drawHeatmapWaterfall(c, n)
}

func (a *App) drawHeatmapInfoStrip(c *Canvas, band BandPreset) {
	c.DrawText(heatmapChartX, 21, band.Name, ColCyan, 1)
	peakStr := fmt.Sprintf("PK %.2fM %ddBm", float64(a.PeakFreqHz)/1e6, a.PeakDBm)
	c.DrawText(72, 21, peakStr, ColAmber, 1)
	if a.HasBurst {
		zStr := fmt.Sprintf("Z+%.1f", a.LastBurst.ZScore)
		if len(zStr) > 6 {
			zStr = "Z+99!"
		}
		c.DrawText(178, 21, zStr, ColMagenta, 1)
	}
	curPrefix, curCol := "CUR", ColText
	if a.AutoLock && a.HasBurst && a.CursorCol == a.LastBurst.Bin {
		curPrefix, curCol = "LCK", ColGreen
	}
	curStr := fmt.Sprintf("%s %.2fM", curPrefix, float64(band.FreqsHz[a.CursorCol])/1e6)
	c.DrawText(224, 21, curStr, curCol, 1)
}

func (a *App) drawHeatmapBars(c *Canvas, n int) {
	c.FillRect(heatmapChartX, heatmapBarTop, heatmapChartW, heatmapBarH, ColPanel)
	c.DrawRect(heatmapChartX, heatmapBarTop, heatmapChartW, heatmapBarH, ColBorder)

	colW := heatmapChartW / n
	for i := range n {
		bx := heatmapChartX + i*colW + 1
		bw := max(2, colW-2)

		if h := rssiToHeight(a.SweepRSSI[i], heatmapBarH-4); h > 0 {
			c.FillRect(bx, heatmapBarTop+heatmapBarH-2-h, bw, h, ThermalColor(a.SweepRSSI[i]))
		}
		a.drawHeatmapBaselineTicks(c, i, bx, bw)
		if ph := rssiToHeight(a.PeakHold[i], heatmapBarH-4); ph > 1 {
			c.HLine(bx, heatmapBarTop+heatmapBarH-2-ph, bw, ColAmber)
		}
		if i == a.CursorCol {
			c.DrawRect(bx-1, heatmapBarTop+1, bw+2, heatmapBarH-2, ColCyan)
		}
	}
}

func (a *App) drawHeatmapBaselineTicks(c *Canvas, idx, bx, bw int) {
	if !a.AutoSquelch || a.SweepBaseline == nil || !a.SweepBaseline.Ready(idx) {
		return
	}
	meanH := rssiToHeight(int32(math.Round(a.SweepBaseline.MeanDBm(idx))), heatmapBarH-4)
	if meanH > 0 {
		c.HLine(bx, heatmapBarTop+heatmapBarH-2-meanH, bw, ColMuted)
	}
	sqH := rssiToHeight(a.SweepBaseline.SmartThreshold(idx, a.SquelchDBm), heatmapBarH-4)
	if sqH > 1 {
		c.HLine(bx, heatmapBarTop+heatmapBarH-2-sqH, bw, ColMagenta)
	}
}

func (a *App) drawHeatmapWaterfall(c *Canvas, n int) {
	colW := heatmapChartW / n
	c.DrawRect(heatmapChartX-1, heatmapWfTop-1, heatmapChartW+2, waterfallRows+2, ColBorder)
	for row := range waterfallRows {
		ringIdx := (a.WfHead - 1 - row + waterfallRows) % waterfallRows
		y := heatmapWfTop + row
		for i := range n {
			bx := heatmapChartX + i*colW
			c.HLine(bx, y, colW, ThermalColor(a.Waterfall[ringIdx][i]))
		}
	}
}
