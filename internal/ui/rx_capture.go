package ui

import (
	"context"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/sensors"
	"rf-heatmap/internal/subfile"
)

const (
	rxPulseChanBuf  = 1024
	maxLiveRawBuf   = 4096
	decodeWindowMin = 48
)

// syncRXCapture starts or stops the hardware GDO0 (GPIO 15) edge-event capture goroutine
// so live PULSE recording and DECODE listen to real hardware pulses without blocking the UI
// and never collide with GDO0 TX.
func (a *App) syncRXCapture() {
	wantRX := a.Radio != nil && !a.TXBusy &&
		((a.Mode == ModeRead && !a.ReadCaptured && (!a.ReadAutoScan || a.ReadAutoLocked || a.Recording)) ||
			(a.Mode == ModePulse && a.Recording) ||
			(a.Mode == ModeDecode && !a.Paused) ||
			(a.ShowSensors && !a.Paused))
	if wantRX && a.rxActive && (a.rxFreq != a.ActiveFreq || a.rxPreset != a.Preset) {
		a.stopRXCapture()
	}
	if wantRX && !a.rxActive {
		a.startRXCapture()
	} else if !wantRX && a.rxActive {
		a.stopRXCapture()
	}
}

func (a *App) startRXCapture() {
	if a.Radio == nil || a.rxActive {
		return
	}
	if a.rxPulseCh == nil {
		a.rxPulseCh = make(chan int32, rxPulseChanBuf)
	}
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	a.rxCancel = cancel
	a.rxDone = done
	a.rxActive = true
	a.rxFreq = a.ActiveFreq
	a.rxPreset = a.Preset

	freq, preset := a.ActiveFreq, a.Preset
	go func() {
		defer close(done)
		_ = a.Radio.CapturePulses(ctx, freq, preset, a.rxPulseCh)
	}()
}

func (a *App) stopRXCapture() {
	if !a.rxActive {
		return
	}
	if a.rxCancel != nil {
		a.rxCancel()
		a.rxCancel = nil
	}
	if a.rxDone != nil {
		<-a.rxDone
		a.rxDone = nil
	}
	a.rxActive = false
}

func (a *App) drainRXPulses() {
	if a.rxPulseCh == nil {
		return
	}
	got := 0
	for {
		select {
		case p := <-a.rxPulseCh:
			a.CapturedRaw = append(a.CapturedRaw, p)
			if len(a.CapturedRaw) > maxLiveRawBuf {
				a.CapturedRaw = a.CapturedRaw[len(a.CapturedRaw)-maxLiveRawBuf:]
			}
			got++
		default:
			if got > 0 && a.Mode == ModeRead && !a.ReadCaptured {
				a.readLastPulseAt = time.Now()
			}
			if got > 0 && a.ShowSensors && len(a.CapturedRaw) >= decodeWindowMin {
				a.tryDecodeSensorWindow()
			} else if got > 0 && (a.Mode == ModeDecode || (a.Mode == ModeRead && !a.ReadCaptured)) && len(a.CapturedRaw) >= decodeWindowMin {
				a.tryDecodeLiveWindow()
				if a.Mode == ModeRead && a.LastDecoded != nil {
					a.ReadCaptured = true
					a.ReadAutoLocked = false
					a.Recording = false
				}
			}
			return
		}
	}
}

func (a *App) tryDecodeLiveWindow() {
	win := a.CapturedRaw
	if len(win) > 256 {
		win = win[len(win)-256:]
	}
	k, ok := subfile.DecodeOOK(win)
	if a.RollingDetector != nil {
		rep := a.RollingDetector.Observe(a.ActiveFreq, win)
		if rep.IsRolling {
			a.LastRolling = rep
			if ok {
				k.IsRolling = true
			}
		}
	}
	if !ok {
		return
	}
	if a.LastDecoded != nil && a.LastDecoded.Protocol == k.Protocol && a.LastDecoded.KeyHex == k.KeyHex {
		return
	}
	a.LastDecoded = &k
	a.DecodedLog = append([]subfile.DecodedKey{k}, a.DecodedLog...)
	if len(a.DecodedLog) > maxDecodedLogEntries {
		a.DecodedLog = a.DecodedLog[:maxDecodedLogEntries]
	}
}

func (a *App) tryDecodeSensorWindow() {
	win := a.CapturedRaw
	if len(win) > 512 {
		win = win[len(win)-512:]
	}
	if r, ok := sensors.Decode(win, a.ActiveFreq, cc1101.GetPresetSpec(a.Preset).Name); ok {
		r.RSSIDBm = a.LiveRSSI
		a.recordSensorReading(r)
	}
}
