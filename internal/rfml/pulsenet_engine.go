package rfml

import (
	"fmt"
	"math"
)

const (
	// PulseNetSeqLen is the fixed pulse window length for PulseNet 1D-CNN inference.
	PulseNetSeqLen = 256
	// PulseNetInChannels is the input channel count (sign, log_mag, delta_log).
	PulseNetInChannels = 3
	// PulseNetMetaDim is the conditioning metadata vector dimension.
	PulseNetMetaDim = 6
	// PulseNetEmbedDim is the learned L2-normalized RF fingerprint dimension.
	PulseNetEmbedDim = 32

	pulseNetLogMaxUs        = 11.512925 // math.Log1p(100000)
	pulseNetCoreLayerCount  = 15
	pulseNetFullLayerCount  = 19
	pulseNetDenoiseMinUs    = 60
	pulseNetDenoiseMaxUs    = 100_000
	pulseNetMaxBufElems     = 96 * 128 // 12288 float32 = 48 KB
	pulseNetDenoiseBufElems = 35 * 256 // 8960 float32 = 35 KB
)

// PulseNetWorkspace holds reusable fixed-size scratch buffers for 0-alloc inference.
type PulseNetWorkspace struct {
	input    [PulseNetInChannels * PulseNetSeqLen]float32
	meta     [PulseNetMetaDim]float32
	bufA     [pulseNetMaxBufElems]float32
	bufB     [pulseNetMaxBufElems]float32
	skip128  [32 * 128]float32
	skip64   [64 * 64]float32
	res96    [96 * 32]float32
	qBuf     [pulseNetMaxBufElems]int8
	pooled   [198]float32
	neck     [128]float32
	fineLog  [czmlMaxFineClasses]float32
	fineProb [czmlMaxFineClasses]float32
	crsLog   [mlpOutputDim]float32
	crsProb  [mlpOutputDim]float32
	embRaw   [PulseNetEmbedDim]float32
	denOut   [2 * PulseNetSeqLen]float32
}

// PulseNetResult holds all outputs from one zero-alloc PulseNet RF forward pass.
type PulseNetResult struct {
	FineClassID        uint16
	ProtocolLabel      string
	ProtocolConfidence float32
	CoarseClass        SignalClass
	CoarseConfidence   float32
	CoarseProbs        [mlpOutputDim]float32
	Embedding          Embedding
	ModelVersion       uint32
}

// ValidatePulseNetModel verifies that m matches the canonical PulseNet-RF topology.
func ValidatePulseNetModel(m *CZMLModel) error {
	if m == nil || m.Domain != DomainRFPulse {
		return fmt.Errorf("%w: expected RF PulseNet domain", ErrCZMLDomainMismatch)
	}
	if len(m.Layers) < pulseNetCoreLayerCount {
		return fmt.Errorf("%w: expected >= %d layers, got %d", ErrInvalidCZML, pulseNetCoreLayerCount, len(m.Layers))
	}
	if m.Layers[13].OutChannels != mlpOutputDim || m.Layers[14].OutChannels != PulseNetEmbedDim {
		return fmt.Errorf("%w: incompatible head dimensions", ErrInvalidCZML)
	}
	if int(m.Layers[12].OutChannels) != len(m.Labels) {
		return fmt.Errorf("%w: fine head mismatch with label table", ErrInvalidCZML)
	}
	return nil
}

// Infer runs zero-allocation INT8 PulseNet inference using caller-owned ws.
func (m *CZMLModel) Infer(ws *PulseNetWorkspace, raw []int32, freqHz uint32, preset string, snrDB float64) PulseNetResult {
	tensorizePulseNetRF(ws, raw, freqHz, preset, snrDB)
	m.runBackbone(ws)

	nFine := len(m.Labels)
	runLinear(ws.neck[:], ws.pooled[:], ws.qBuf[:], &m.Layers[11])
	runLinear(ws.fineLog[:nFine], ws.neck[:], ws.qBuf[:], &m.Layers[12])
	runLinear(ws.crsLog[:], ws.neck[:], ws.qBuf[:], &m.Layers[13])
	runLinear(ws.embRaw[:], ws.neck[:], ws.qBuf[:], &m.Layers[14])

	fineIdx, fineProb := softmaxTemperature(ws.fineProb[:nFine], ws.fineLog[:nFine], nFine, m.TempFine)
	crsIdx, crsProb := softmaxTemperature(ws.crsProb[:], ws.crsLog[:], mlpOutputDim, m.TempCoarse)

	return PulseNetResult{
		FineClassID:        uint16(fineIdx),
		ProtocolLabel:      m.Labels[fineIdx],
		ProtocolConfidence: fineProb,
		CoarseClass:        SignalClass(crsIdx),
		CoarseConfidence:   crsProb,
		CoarseProbs:        ws.crsProb,
		Embedding:          l2Normalize32(ws.embRaw[:]),
		ModelVersion:       m.Version,
	}
}

// DenoiseNeuralInto runs the lazy 1D-UNet asymmetric AGC denoiser into dst with 0 allocs
// when cap(dst) >= len(raw), preserving strict +/- sign alternation.
func (m *CZMLModel) DenoiseNeuralInto(ws *PulseNetWorkspace, dst, raw []int32, freqHz uint32, preset string, snrDB float64) ([]int32, int) {
	if len(raw) == 0 || (m.Flags&czmlFlagHasDenoiser) == 0 || len(m.Layers) < pulseNetFullLayerCount {
		return dst[:0], 0
	}
	tensorizePulseNetRF(ws, raw, freqHz, preset, snrDB)
	m.runBackbone(ws)
	m.runDenoiserDecoder(ws)
	return reconstructDenoisedPulses(dst, raw, ws.denOut[:])
}

func (m *CZMLModel) runBackbone(ws *PulseNetWorkspace) {
	runLayer1D(ws.skip128[:], ws.input[:], ws.qBuf[:], PulseNetSeqLen, &m.Layers[0])
	runLayer1D(ws.bufA[:], ws.skip128[:], ws.qBuf[:], 128, &m.Layers[1])
	runLayer1D(ws.bufB[:], ws.bufA[:], ws.qBuf[:], 128, &m.Layers[2])
	runLayer1D(ws.bufA[:], ws.bufB[:], ws.qBuf[:], 128, &m.Layers[3])
	runLayer1D(ws.skip64[:], ws.bufA[:], ws.qBuf[:], 64, &m.Layers[4])
	runLayer1D(ws.bufA[:], ws.skip64[:], ws.qBuf[:], 64, &m.Layers[5])
	runLayer1D(ws.bufB[:], ws.bufA[:], ws.qBuf[:], 64, &m.Layers[6])
	addResidualReLU6(ws.bufB[:], ws.skip64[:], 64*64)
	copy(ws.skip64[:], ws.bufB[:64*64])

	runLayer1D(ws.bufA[:], ws.skip64[:], ws.qBuf[:], 64, &m.Layers[7])
	runLayer1D(ws.res96[:], ws.bufA[:], ws.qBuf[:], 32, &m.Layers[8])
	runLayer1D(ws.bufA[:], ws.res96[:], ws.qBuf[:], 32, &m.Layers[9])
	runLayer1D(ws.bufB[:], ws.bufA[:], ws.qBuf[:], 32, &m.Layers[10])
	addResidualReLU6(ws.bufB[:], ws.res96[:], 96*32)

	globalAvgMaxPool(ws.pooled[:192], ws.bufB[:96*32], 96, 32)
	copy(ws.pooled[192:], ws.meta[:])
}

func (m *CZMLModel) runDenoiserDecoder(ws *PulseNetWorkspace) {
	upsample2xConcat(ws.bufA[:], ws.skip64[:], 64, 64, ws.skip128[:], 32)
	runLayer1D(ws.bufB[:], ws.bufA[:], ws.qBuf[:], 128, &m.Layers[15])
	runLayer1D(ws.bufA[:], ws.bufB[:], ws.qBuf[:], 128, &m.Layers[16])
	upsample2xConcat(ws.bufB[:pulseNetDenoiseBufElems], ws.bufA[:], 32, 128, ws.input[:], PulseNetInChannels)
	runLayer1D(ws.bufA[:], ws.bufB[:pulseNetDenoiseBufElems], ws.qBuf[:], PulseNetSeqLen, &m.Layers[17])
	runLayer1D(ws.denOut[:], ws.bufA[:], ws.qBuf[:], PulseNetSeqLen, &m.Layers[18])
}

func tensorizePulseNetRF(ws *PulseNetWorkspace, raw []int32, freqHz uint32, preset string, snrDB float64) {
	clear(ws.input[:])
	var hiSum, totSum float64
	glitches := 0
	t := 0
	var prevCh1 float32

	for _, p := range raw {
		mag := absInt64(p)
		totSum += float64(mag)
		if p > 0 {
			hiSum += float64(mag)
		}
		if mag < int64(DefaultMinGlitchUs) {
			glitches++
		}
		if p == 0 {
			continue
		}
		if t < PulseNetSeqLen {
			clamped := min(mag, int64(pulseNetDenoiseMaxUs))
			c1 := float32(math.Log1p(float64(clamped)) / pulseNetLogMaxUs)
			if p > 0 {
				ws.input[t] = 1.0
			} else {
				ws.input[t] = -1.0
			}
			ws.input[PulseNetSeqLen+t] = c1
			if t > 0 {
				ws.input[2*PulseNetSeqLen+t] = clampSignedUnit((c1 - prevCh1) * 3.0)
			}
			prevCh1 = c1
			t++
		}
	}

	duty := 0.0
	if totSum > 0 {
		duty = hiSum / totSum
	}
	glitchRate := 1.0
	if len(raw) > 0 {
		glitchRate = float64(glitches) / float64(len(raw))
	}
	snr := 0.0
	if !math.IsNaN(snrDB) && !math.IsInf(snrDB, 0) {
		snr = snrDB
	}
	isFSK := float32(0)
	if isFSKPreset(preset) {
		isFSK = 1.0
	}

	ws.meta[0] = float32(normalizeBand(freqHz))
	ws.meta[1] = isFSK
	ws.meta[2] = clamp32(snr / 50.0)
	ws.meta[3] = clamp32(math.Log1p(float64(len(raw))) / 7.0)
	ws.meta[4] = clamp32(duty)
	ws.meta[5] = clamp32(glitchRate)
}

func reconstructDenoisedPulses(dst, raw []int32, denOut []float32) ([]int32, int) {
	out := dst[:0]
	glitches := 0
	t := 0
	for _, p := range raw {
		if p == 0 {
			glitches++
			continue
		}
		mag := absInt64(p)
		isGlitch := mag < int64(pulseNetDenoiseMinUs)
		adjMag := mag
		if t < PulseNetSeqLen {
			if denOut[PulseNetSeqLen+t] > 0.0 {
				isGlitch = true
			} else {
				dLog := math.Tanh(float64(denOut[t])) * 1.5
				scaled := math.Expm1(math.Log1p(float64(min(mag, int64(pulseNetDenoiseMaxUs)))) + dLog)
				adjMag = max(int64(pulseNetDenoiseMinUs), min(int64(pulseNetDenoiseMaxUs), int64(math.Round(scaled))))
			}
			t++
		}
		if isGlitch {
			glitches++
			if len(out) > 0 {
				out[len(out)-1] = addSameSign(out[len(out)-1], mag)
			}
			continue
		}
		signed := int32(adjMag)
		if p < 0 {
			signed = -signed
		}
		if len(out) > 0 && sameSign(out[len(out)-1], signed) {
			out[len(out)-1] = addSameSign(out[len(out)-1], adjMag)
			continue
		}
		out = append(out, signed)
	}
	return out, glitches
}

func isFSKPreset(preset string) bool {
	if len(preset) >= 2 && (preset[0]|0x20) == 'f' && (preset[1]|0x20) == 'm' {
		return true
	}
	for i := 0; i+3 <= len(preset); i++ {
		if (preset[i]|0x20) == 'f' && (preset[i+1]|0x20) == 's' && (preset[i+2]|0x20) == 'k' {
			return true
		}
	}
	return false
}

func clampSignedUnit(v float32) float32 {
	if v < -1 {
		return -1
	}
	if v > 1 {
		return 1
	}
	return v
}
