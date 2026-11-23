package ui

import (
	"fmt"
	"math"
	"math/rand"
	"time"

	"rf-heatmap/internal/rfml"
)

const (
	foxhuntBarX       = 8
	foxhuntBarY       = 68
	foxhuntBarW       = 304
	foxhuntBarH       = 28
	foxhuntMinDBm     = -110
	foxhuntMaxDBm     = -25
	foxhuntGeigerSegs = 12
)

// EnterFoxhunt opens the full-screen Foxhunt/Geiger RSSI view locked on ActiveFreq.
func (a *App) EnterFoxhunt(returnMode Mode) {
	a.foxReturnMode = returnMode
	a.ShowFoxhunt = true
	if a.Tracker == nil {
		a.Tracker = rfml.NewFoxhuntTracker(rfml.DefaultKalmanQ, rfml.DefaultKalmanR)
	}
	a.Tracker.Reset(a.ActiveFreq)
	a.LastFoxSample = rfml.FoxhuntSample{}
	a.GeigerFlashOn = false
	a.nextGeigerTick = time.Time{}
	a.SetToast(fmt.Sprintf("Foxhunt %.2f MHz", float64(a.ActiveFreq)/1e6))
}

// ExitFoxhunt returns immediately to the previous mode without exiting the app.
func (a *App) ExitFoxhunt() {
	if !a.ShowFoxhunt {
		return
	}
	a.ShowFoxhunt = false
	a.Mode = a.foxReturnMode
}

func (a *App) handleFoxhuntKey(act KeyAction) {
	switch act {
	case KeyQuit, KeyBack, KeyNav8:
		a.ExitFoxhunt()
	case KeyNav4:
		if a.Tracker != nil {
			a.Tracker.ResetPeak()
		}
		a.LastFoxSample.PeakDBm = -120
		a.SetToast("Peak reset")
	case KeyNav5:
		a.GeigerMuted = !a.GeigerMuted
		if a.GeigerMuted {
			a.SetToast("Geiger muted")
		} else {
			a.SetToast("Geiger click ON")
		}
	case KeyNav6, KeyBand:
		a.cycleBand()
		if a.Tracker != nil {
			a.Tracker.Reset(a.ActiveFreq)
		}
	case KeyNav7, KeyMod:
		a.cyclePreset()
	case KeyLeft:
		a.nudgeActiveFreq(-pulseFreqStepHz)
		if a.Tracker != nil {
			a.Tracker.Reset(a.ActiveFreq)
		}
	case KeyRight:
		a.nudgeActiveFreq(pulseFreqStepHz)
		if a.Tracker != nil {
			a.Tracker.Reset(a.ActiveFreq)
		}
	}
}

func (a *App) stepFoxhunt() {
	if a.Radio != nil {
		_ = a.Radio.StartReceive(a.ActiveFreq)
		if v, err := a.Radio.ReadRSSIDBm(); err == nil {
			a.LiveRSSI = v
		}
	} else {
		// Smooth walking-closer/farther simulation wave.
		wave := math.Sin(float64(a.tick) * 0.14)
		a.LiveRSSI = int32(-72 + wave*26 + float64(rand.Intn(5)-2))
	}
	if a.LiveBaseline != nil {
		a.LiveBaseline.UpdateBin(0, a.LiveRSSI)
	}
	floorDBm := int32(defaultFloorDBm)
	if a.LiveBaseline != nil && a.LiveBaseline.Ready(0) {
		floorDBm = int32(math.Round(a.LiveBaseline.MeanDBm(0)))
	}
	if a.Tracker == nil {
		a.Tracker = rfml.NewFoxhuntTracker(rfml.DefaultKalmanQ, rfml.DefaultKalmanR)
	}
	a.LastFoxSample = a.Tracker.Update(a.LiveRSSI, floorDBm)
	a.advanceGeigerPulse()
}

func (a *App) advanceGeigerPulse() {
	intervalMs := a.LastFoxSample.GeigerIntervalMs
	if intervalMs <= 0 {
		a.GeigerFlashOn = false
		return
	}
	now := time.Now()
	if a.nextGeigerTick.IsZero() || !now.Before(a.nextGeigerTick) {
		a.GeigerFlashOn = true
		a.nextGeigerTick = now.Add(time.Duration(intervalMs) * time.Millisecond)
		if a.Clicker != nil && !a.GeigerMuted {
			a.Clicker.Click(1800, 12)
		}
		return
	}
	a.GeigerFlashOn = false
}

func (a *App) drawFoxhunt(c *Canvas) {
	s := a.LastFoxSample
	a.drawFoxhuntHeaderRow(c, s)
	a.drawFoxhuntPeakRow(c, s)
	a.drawFoxhuntBar(c, s)
	a.drawFoxhuntGeigerLEDs(c, s)
}

func (a *App) drawFoxhuntHeaderRow(c *Canvas, s rfml.FoxhuntSample) {
	c.DrawText(8, 22, fmt.Sprintf("%.2f MHz", float64(a.ActiveFreq)/1e6), ColCyan, 2)
	rssiCol := ColMuted
	if s.RawDBm >= a.EffectiveSquelchDBm() {
		rssiCol = ColGreen
	}
	c.DrawText(198, 22, fmt.Sprintf("%4ddBm", s.RawDBm), rssiCol, 2)
}

func (a *App) drawFoxhuntPeakRow(c *Canvas, s rfml.FoxhuntSample) {
	c.DrawText(8, 48, fmt.Sprintf("PK:%ddBm", s.PeakDBm), ColAmber, 1)
	trendCol, arrow := ColMuted, "="
	switch s.Kalman.Trend {
	case "CLOSER":
		trendCol, arrow = ColGreen, "^"
	case "FARTHER":
		trendCol, arrow = ColAmber, "v"
	}
	c.DrawText(92, 48, fmt.Sprintf("%s %s %d%%", arrow, s.Kalman.Trend, s.Kalman.ProximityPct), trendCol, 1)
	geigerCol := ColMuted
	if a.GeigerFlashOn {
		geigerCol = ColMagenta
	} else if s.GeigerHz > 0 {
		geigerCol = ColCyan
	}
	c.DrawText(214, 48, fmt.Sprintf("GEIGER:%4.1fHz", s.GeigerHz), geigerCol, 1)
}

func (a *App) drawFoxhuntBar(c *Canvas, s rfml.FoxhuntSample) {
	c.FillRect(foxhuntBarX, foxhuntBarY, foxhuntBarW, foxhuntBarH, ColPanel)
	c.DrawRect(foxhuntBarX, foxhuntBarY, foxhuntBarW, foxhuntBarH, ColBorder)

	fillW := dbmToBarX(s.RawDBm, foxhuntBarW-4)
	if fillW > 0 {
		c.FillRect(foxhuntBarX+2, foxhuntBarY+2, fillW, foxhuntBarH-4, ThermalColor(s.RawDBm))
	}
	sqX := foxhuntBarX + 2 + dbmToBarX(a.EffectiveSquelchDBm(), foxhuntBarW-4)
	c.VLine(sqX, foxhuntBarY+1, foxhuntBarH-2, ColCyan)

	pkX := foxhuntBarX + 2 + dbmToBarX(s.PeakDBm, foxhuntBarW-4)
	c.FillRect(pkX, foxhuntBarY+1, 2, foxhuntBarH-2, ColAmber)

	c.DrawText(foxhuntBarX, 99, "-110", ColMuted, 1)
	c.DrawText(148, 99, "-70", ColMuted, 1)
	c.DrawText(290, 99, "-30", ColMuted, 1)
}

func (a *App) drawFoxhuntGeigerLEDs(c *Canvas, s rfml.FoxhuntSample) {
	c.FillRect(8, 112, 304, 24, ColPanel)
	c.DrawRect(8, 112, 304, 24, ColBorder)
	lit := int(math.Round(s.GeigerHz / 20.0 * foxhuntGeigerSegs))
	segW := 22
	for i := range foxhuntGeigerSegs {
		sx := 14 + i*(segW+3)
		col := ColPanelAlt
		if i < lit {
			col = ColGreen
			if i >= 8 {
				col = ColAmber
			}
			if a.GeigerFlashOn {
				col = ColMagenta
			}
		}
		c.FillRect(sx, 116, segW, 16, col)
	}
}

func dbmToBarX(dbm int32, maxW int) int {
	t := float64(dbm-foxhuntMinDBm) / float64(foxhuntMaxDBm-foxhuntMinDBm)
	if t <= 0 {
		return 0
	}
	if t >= 1 {
		return maxW
	}
	return int(math.Round(t * float64(maxW)))
}
