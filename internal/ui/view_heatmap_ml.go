package ui

import "rf-heatmap/internal/rfml"

const (
	spectrumBadgeX    = 248
	spectrumBadgeY    = heatmapWfTop + 2
	spectrumBadgeW    = 64
	spectrumBadgeH    = 11
	spectrumMinConf   = 0.55
	spectrumCadenceTk = 4
)

func (a *App) updateSpectrumScene(n int) {
	if a.tick < rfml.SpectrumRows || a.tick%spectrumCadenceTk != 0 || n <= 0 {
		return
	}
	var wf [rfml.SpectrumRows][rfml.SpectrumCols]int32
	for r := range rfml.SpectrumRows {
		ringIdx := (a.WfHead - rfml.SpectrumRows + r + waterfallRows) % waterfallRows
		for c := range rfml.SpectrumCols {
			if c < n {
				wf[r][c] = a.Waterfall[ringIdx][c]
			} else {
				wf[r][c] = defaultWaterfallDBm
			}
		}
	}
	var floor [rfml.SpectrumCols]float32
	for c := range rfml.SpectrumCols {
		floor[c] = float32(defaultFloorDBm)
		if a.SweepBaseline != nil && c < n && a.SweepBaseline.Ready(c) {
			floor[c] = float32(a.SweepBaseline.MeanDBm(c))
		}
	}
	if pred, ok := rfml.ClassifySpectrum(&wf, &floor); ok {
		a.LastSpectrum = pred
		a.HasSpectrum = true
	}
}

func (a *App) drawHeatmapSpectrumBadge(c *Canvas) {
	if !a.HasSpectrum || a.LastSpectrum.Confidence < spectrumMinConf {
		return
	}
	label, col := spectrumBadgeStyle(a.LastSpectrum.Class)
	if label == "" {
		return
	}
	c.FillRect(spectrumBadgeX, spectrumBadgeY, spectrumBadgeW, spectrumBadgeH, ColBg)
	c.DrawRect(spectrumBadgeX, spectrumBadgeY, spectrumBadgeW, spectrumBadgeH, col)
	c.DrawTextCentered(spectrumBadgeX+spectrumBadgeW/2, spectrumBadgeY+2, label, col, 1)
}

func spectrumBadgeStyle(cls rfml.SpectrumClass) (string, uint16) {
	switch cls {
	case rfml.SpectrumRollJamSuspect:
		return "ROLLJAM!", ColRed
	case rfml.SpectrumCWJammer:
		return "CW JAM!", ColRed
	case rfml.SpectrumFHSSHopper:
		return "FHSS", ColMagenta
	case rfml.SpectrumLoRaChirp:
		return "LORA", ColCyan
	case rfml.SpectrumWidebandEMI:
		return "EMI", ColAmber
	case rfml.SpectrumPeriodicBeacon:
		return "BEACON", ColGreen
	case rfml.SpectrumNarrowbandOOK:
		return "OOK", ColCyan
	default:
		return "", 0
	}
}
