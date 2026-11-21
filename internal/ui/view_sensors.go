package ui

import (
	"fmt"
	"math/rand"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/sensors"
)

type sensorProfileSpec struct {
	Label  string
	FreqHz uint32
	Preset cc1101.Preset
}

var sensorProfiles = []sensorProfileSpec{
	{Label: "TPMS 433 (GFSK99)", FreqHz: 433_920_000, Preset: cc1101.PresetGFSK999},
	{Label: "TPMS 315 (GFSK99)", FreqHz: 315_000_000, Preset: cc1101.PresetGFSK999},
	{Label: "Weather 433 (AM650)", FreqHz: 433_920_000, Preset: cc1101.PresetAM650},
}

// EnterSensors opens the SENSORS sub-view from DECODE and tunes to the active sensor profile.
func (a *App) EnterSensors() {
	if a.ShowSensors {
		return
	}
	a.prevPreset = a.Preset
	a.prevFreqHz = a.ActiveFreq
	a.ShowSensors = true
	a.applySensorProfile()
	a.SetToast("Sensors: " + sensorProfiles[a.SensorProfile].Label)
}

// ExitSensors closes the SENSORS sub-view and restores previous frequency and modem preset.
func (a *App) ExitSensors() {
	if !a.ShowSensors {
		return
	}
	a.ShowSensors = false
	a.ActiveFreq = a.prevFreqHz
	a.Preset = a.prevPreset
	if a.Radio != nil {
		_ = a.Radio.ApplyPreset(a.Preset)
	}
}

func (a *App) applySensorProfile() {
	p := sensorProfiles[a.SensorProfile%len(sensorProfiles)]
	a.ActiveFreq = p.FreqHz
	a.Preset = p.Preset
	if a.Radio != nil {
		_ = a.Radio.ApplyPreset(p.Preset)
	}
}

func (a *App) handleSensorsKey(act KeyAction) {
	switch act {
	case KeyQuit, KeyNav8:
		a.ExitSensors()
	case KeyNav4, KeySpace:
		a.Paused = !a.Paused
	case KeyNav5:
		a.ImperialUnits = !a.ImperialUnits
		if a.ImperialUnits {
			a.SetToast("Units: PSI / F")
		} else {
			a.SetToast("Units: kPa / C")
		}
	case KeyNav6, KeyBand, KeyMod:
		a.SensorProfile = (a.SensorProfile + 1) % len(sensorProfiles)
		a.applySensorProfile()
		a.SetToast(sensorProfiles[a.SensorProfile].Label)
	case KeyNav7:
		a.SensorEntries = nil
		a.SensorIdx = 0
		a.SetToast("Sensors cleared")
	case KeyUp:
		if a.SensorIdx > 0 {
			a.SensorIdx--
		}
	case KeyDown:
		if a.SensorIdx < len(a.SensorEntries)-1 {
			a.SensorIdx++
		}
	}
}

func (a *App) stepSensors() {
	if !a.SimMode {
		return
	}
	if a.tick%10 != 1 {
		return
	}
	pulses, freqHz, preset := sensors.SimFrame(a.tick / 10)
	if r, ok := sensors.Decode(pulses, freqHz, preset); ok {
		r.RSSIDBm = -58 + int32(rand.Intn(14))
		a.recordSensorReading(r)
	}
}

func (a *App) recordSensorReading(r sensors.Reading) {
	r.SeenAt = time.Now()
	for i := range a.SensorEntries {
		e := &a.SensorEntries[i]
		if e.Protocol == r.Protocol && e.SensorID == r.SensorID {
			r.SeenCount = e.SeenCount + 1
			*e = r
			return
		}
	}
	r.SeenCount = 1
	a.SensorEntries = append([]sensors.Reading{r}, a.SensorEntries...)
	if len(a.SensorEntries) > 12 {
		a.SensorEntries = a.SensorEntries[:12]
	}
}

func (a *App) drawSensors(c *Canvas) {
	prof := sensorProfiles[a.SensorProfile%len(sensorProfiles)]
	c.FillRect(6, 20, 308, 13, ColPanelAlt)
	c.DrawRect(6, 20, 308, 13, ColBorder)
	c.DrawText(10, 23, fmt.Sprintf("Profile: %s  [6:PROF 5:UNIT]", prof.Label), ColCyan, 1)

	c.FillRect(6, 36, 308, 104, ColPanel)
	c.DrawRect(6, 36, 308, 104, ColBorder)

	if len(a.SensorEntries) == 0 {
		c.DrawTextCentered(ScreenW/2, 76, "Listening for TPMS & Weather...", ColMuted, 1)
		c.DrawTextCentered(ScreenW/2, 90, "Schrader / Toyota / Oregon / LaCrosse", ColCyan, 1)
		return
	}
	start := max(0, a.SensorIdx-2)
	visible := min(4, len(a.SensorEntries)-start)
	for row := range visible {
		a.drawSensorRow(c, start+row, row)
	}
}

func (a *App) drawSensorRow(c *Canvas, idx, row int) {
	e := a.SensorEntries[idx]
	y := 39 + row*25
	if idx == a.SensorIdx {
		c.FillRect(8, y, 304, 23, ColNavActive)
		c.DrawRect(8, y, 304, 23, ColCyan)
	}
	badge, badgeCol := "WTHR", ColGreen
	if e.Kind == sensors.KindTPMS {
		badge, badgeCol = "TPMS", ColCyan
	}
	c.DrawText(11, y+3, badge, badgeCol, 1)
	c.DrawText(40, y+3, truncate(fmt.Sprintf("%s %s", e.Protocol, e.SensorID), 18), ColText, 1)

	batTxt, batCol := "BAT:OK", ColGreen
	if !e.BatteryOK {
		batTxt, batCol = "BAT:LO", ColRed
	}
	c.DrawText(254, y+3, batTxt, batCol, 1)
	c.DrawText(11, y+13, a.formatSensorTelemetry(e), ColMuted, 1)
}

func (a *App) formatSensorTelemetry(e sensors.Reading) string {
	tempVal, tempUnit := e.TempC, "C"
	if a.ImperialUnits {
		tempVal, tempUnit = sensors.CelsiusToFahrenheit(e.TempC), "F"
	}
	if e.Kind == sensors.KindTPMS {
		if a.ImperialUnits {
			return fmt.Sprintf("%.1f PSI (%.0fkPa)  %.1f%s  #%d", e.PressurePSI, e.PressureKPa, tempVal, tempUnit, e.SeenCount)
		}
		return fmt.Sprintf("%.0fkPa (%.1fPSI)  %.1f%s  #%d", e.PressureKPa, e.PressurePSI, tempVal, tempUnit, e.SeenCount)
	}
	if e.HasHumidity {
		return fmt.Sprintf("CH%d  %.1f%s  %d%%RH  #%d", e.Channel, tempVal, tempUnit, e.HumidityPct, e.SeenCount)
	}
	return fmt.Sprintf("CH%d  %.1f%s  #%d", e.Channel, tempVal, tempUnit, e.SeenCount)
}
