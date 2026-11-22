package rfml

import (
	_ "embed"
	"os"
	"path/filepath"
	"sync"
)

//go:embed models/pulsenet_rf_v1.czml
var embeddedPulseNetRF []byte

//go:embed models/spectrumnet_v1.czml
var embeddedSpectrumNet []byte

var (
	pulseNetOnce sync.Once
	pulseNetDef  *CZMLModel
	pulseNetInfo ModelInfo

	spectrumOnce sync.Once
	spectrumDef  *CZMLModel
	spectrumInfo ModelInfo

	pulseWS = sync.Pool{New: func() any { return new(PulseNetWorkspace) }}
	specWS  = sync.Pool{New: func() any { return new(SpectrumWorkspace) }}
)

// DefaultModelsDir returns $HOME/.config/rf-heatmap/models.
func DefaultModelsDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".config", "rf-heatmap", "models")
	}
	return filepath.Join(home, ".config", "rf-heatmap", "models")
}

// LoadDefaultPulseNet loads a verified override .czml from overridePath if valid,
// otherwise falling back cleanly to the embedded PulseNet-RF model.
func LoadDefaultPulseNet(overridePath string) (*CZMLModel, ModelInfo) {
	if overridePath != "" {
		if m, err := LoadCZMLFile(overridePath, DomainRFPulse); err == nil {
			if ValidatePulseNetModel(m) == nil {
				return m, m.Info("override")
			}
		}
	}
	m, err := LoadCZMLBytes(embeddedPulseNetRF, DomainRFPulse)
	if err != nil || ValidatePulseNetModel(m) != nil {
		return nil, ModelInfo{Source: "unavailable"}
	}
	return m, m.Info("embedded")
}

// LoadDefaultSpectrumNet loads a verified override SpectrumNet-2D .czml if valid,
// otherwise falling back cleanly to the embedded model.
func LoadDefaultSpectrumNet(overridePath string) (*CZMLModel, ModelInfo) {
	if overridePath != "" {
		if m, err := LoadCZMLFile(overridePath, DomainRFSpectrum); err == nil {
			if ValidateSpectrumModel(m) == nil {
				return m, m.Info("override")
			}
		}
	}
	m, err := LoadCZMLBytes(embeddedSpectrumNet, DomainRFSpectrum)
	if err != nil || ValidateSpectrumModel(m) != nil {
		return nil, ModelInfo{Source: "unavailable"}
	}
	return m, m.Info("embedded")
}

func defaultPulseNet() *CZMLModel {
	pulseNetOnce.Do(func() {
		override := filepath.Join(DefaultModelsDir(), "pulsenet_rf.czml")
		pulseNetDef, pulseNetInfo = LoadDefaultPulseNet(override)
	})
	return pulseNetDef
}

func defaultSpectrumNet() *CZMLModel {
	spectrumOnce.Do(func() {
		override := filepath.Join(DefaultModelsDir(), "spectrumnet.czml")
		spectrumDef, spectrumInfo = LoadDefaultSpectrumNet(override)
	})
	return spectrumDef
}

// ActivePulseNetInfo returns provenance metadata for the active RF PulseNet model.
func ActivePulseNetInfo() ModelInfo {
	_ = defaultPulseNet()
	return pulseNetInfo
}

// ActiveSpectrumNetInfo returns provenance metadata for the active SpectrumNet-2D model.
func ActiveSpectrumNetInfo() ModelInfo {
	_ = defaultSpectrumNet()
	return spectrumInfo
}

// DenoisePulsesNeural runs PulseNet's lazy 1D-UNet asymmetric AGC denoiser.
func DenoisePulsesNeural(raw []int32, freqHz uint32, preset string, snrDB float64) ([]int32, int, bool) {
	m := defaultPulseNet()
	if m == nil || len(raw) == 0 {
		return nil, 0, false
	}
	ws := pulseWS.Get().(*PulseNetWorkspace)
	defer pulseWS.Put(ws)
	dst := make([]int32, 0, len(raw))
	out, glitches := m.DenoiseNeuralInto(ws, dst, raw, freqHz, preset, snrDB)
	return out, glitches, len(out) > 0
}

// ClassifySpectrum classifies a [32][36] waterfall matrix using SpectrumNet-2D.
func ClassifySpectrum(waterfall *[SpectrumRows][SpectrumCols]int32, baselineFloor *[SpectrumCols]float32) (SpectrumPrediction, bool) {
	m := defaultSpectrumNet()
	if m == nil || waterfall == nil {
		return SpectrumPrediction{}, false
	}
	ws := specWS.Get().(*SpectrumWorkspace)
	defer specWS.Put(ws)
	return m.InferSpectrum(ws, waterfall, baselineFloor), true
}
