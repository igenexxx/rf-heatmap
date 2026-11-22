package rfml

import "fmt"

const (
	// SpectrumRows is the temporal depth of the 2D waterfall input to SpectrumNet-2D.
	SpectrumRows = 32
	// SpectrumCols is the frequency bin width of the 2D waterfall input to SpectrumNet-2D.
	SpectrumCols = 36
	// SpectrumClasses is the number of RF spectrum scene classes.
	SpectrumClasses = 8

	spectrumLayerCount = 9
	spectrumMaxBuf     = 16 * 16 * 18 // 4608 float32
)

// SpectrumClass identifies the high-level 2D waterfall RF scene predicted by SpectrumNet-2D.
type SpectrumClass int

const (
	SpectrumQuiet SpectrumClass = iota
	SpectrumNarrowbandOOK
	SpectrumPeriodicBeacon
	SpectrumWidebandEMI
	SpectrumFHSSHopper
	SpectrumLoRaChirp
	SpectrumCWJammer
	SpectrumRollJamSuspect
)

// SpectrumPrediction holds one zero-allocation SpectrumNet-2D classification result.
type SpectrumPrediction struct {
	Class      SpectrumClass            `json:"class_id"`
	Label      string                   `json:"label"`
	Confidence float32                  `json:"confidence"`
	IsAlert    bool                     `json:"is_alert"`
	Probs      [SpectrumClasses]float32 `json:"probs"`
}

// SpectrumWorkspace holds reusable scratch buffers for 0-alloc SpectrumNet-2D inference.
type SpectrumWorkspace struct {
	input  [2 * SpectrumRows * SpectrumCols]float32
	bufA   [spectrumMaxBuf]float32
	bufB   [spectrumMaxBuf]float32
	qBuf   [spectrumMaxBuf]int8
	pooled [128]float32
	fc1    [64]float32
	logits [SpectrumClasses]float32
	probs  [SpectrumClasses]float32
}

// ValidateSpectrumModel checks that m conforms to the canonical SpectrumNet-2D topology.
func ValidateSpectrumModel(m *CZMLModel) error {
	if m == nil || m.Domain != DomainRFSpectrum {
		return fmt.Errorf("%w: expected RF SpectrumNet domain", ErrCZMLDomainMismatch)
	}
	if len(m.Layers) != spectrumLayerCount {
		return fmt.Errorf("%w: expected %d layers, got %d", ErrInvalidCZML, spectrumLayerCount, len(m.Layers))
	}
	if int(m.Layers[8].OutChannels) != SpectrumClasses || len(m.Labels) != SpectrumClasses {
		return fmt.Errorf("%w: expected %d spectrum classes", ErrInvalidCZML, SpectrumClasses)
	}
	return nil
}

// InferSpectrum classifies a [32][36] waterfall matrix (oldest->newest or chronological)
// against per-bin baselineFloor dBm with 0 heap allocations.
func (m *CZMLModel) InferSpectrum(ws *SpectrumWorkspace, waterfall *[SpectrumRows][SpectrumCols]int32, baselineFloor *[SpectrumCols]float32) SpectrumPrediction {
	tensorizeWaterfall(ws, waterfall, baselineFloor)

	h, w := runLayer2D(ws.bufA[:], ws.input[:], ws.qBuf[:], SpectrumRows, SpectrumCols, &m.Layers[0])
	h, w = runLayer2D(ws.bufB[:], ws.bufA[:], ws.qBuf[:], h, w, &m.Layers[1])
	h, w = runLayer2D(ws.bufA[:], ws.bufB[:], ws.qBuf[:], h, w, &m.Layers[2])
	h, w = runLayer2D(ws.bufB[:], ws.bufA[:], ws.qBuf[:], h, w, &m.Layers[3])
	h, w = runLayer2D(ws.bufA[:], ws.bufB[:], ws.qBuf[:], h, w, &m.Layers[4])
	h, w = runLayer2D(ws.bufB[:], ws.bufA[:], ws.qBuf[:], h, w, &m.Layers[5])
	h, w = runLayer2D(ws.bufA[:], ws.bufB[:], ws.qBuf[:], h, w, &m.Layers[6])

	globalAvgMaxPool(ws.pooled[:], ws.bufA[:64*h*w], 64, h*w)
	runLinear(ws.fc1[:], ws.pooled[:], ws.qBuf[:], &m.Layers[7])
	runLinear(ws.logits[:], ws.fc1[:], ws.qBuf[:], &m.Layers[8])

	bestIdx, bestProb := softmaxTemperature(ws.probs[:], ws.logits[:], SpectrumClasses, m.TempFine)
	cls := SpectrumClass(bestIdx)
	isAlert := (cls == SpectrumRollJamSuspect || cls == SpectrumCWJammer) && bestProb >= 0.65
	return SpectrumPrediction{
		Class:      cls,
		Label:      m.Labels[bestIdx],
		Confidence: bestProb,
		IsAlert:    isAlert,
		Probs:      ws.probs,
	}
}

func tensorizeWaterfall(ws *SpectrumWorkspace, wf *[SpectrumRows][SpectrumCols]int32, floor *[SpectrumCols]float32) {
	const plane = SpectrumRows * SpectrumCols
	for r := range SpectrumRows {
		rowOff := r * SpectrumCols
		for c := range SpectrumCols {
			v := float32(wf[r][c])
			if v < -115 {
				v = -115
			} else if v > -30 {
				v = -30
			}
			base := float32(-102)
			if floor != nil && floor[c] != 0 {
				base = floor[c]
			}
			ws.input[rowOff+c] = clampUnit32((v + 115.0) / 85.0)
			ws.input[plane+rowOff+c] = clampSignedUnit((v - base) / 40.0)
		}
	}
}

func runLayer2D(dst, src []float32, qBuf []int8, inH, inW int, l *czmlLayer) (int, int) {
	inC := int(l.InChannels)
	outC := int(l.OutChannels)
	sx := quantizeDynamicInt8(src, qBuf, inC*inH*inW)
	k := int(l.KernelSize)
	stride := int(l.Stride)
	pad := k / 2
	outH := (inH+2*pad-k)/stride + 1
	outW := (inW+2*pad-k)/stride + 1

	if l.OpType == opDepthwiseConv2D {
		runDepthwise2D(dst, qBuf, sx, outC, inH, inW, outH, outW, k, stride, pad, l)
		return outH, outW
	}
	runConv2D(dst, qBuf, sx, inC, outC, inH, inW, outH, outW, k, stride, pad, l)
	return outH, outW
}

func runDepthwise2D(dst []float32, xq []int8, sx float32, ch, inH, inW, outH, outW, k, stride, pad int, l *czmlLayer) {
	inPlane := inH * inW
	outPlane := outH * outW
	kk := k * k
	for c := range ch {
		wBase := c * kk
		inOff := c * inPlane
		outOff := c * outPlane
		scale := l.WeightScales[c] * sx
		bias := l.Biases[c]
		for oh := range outH {
			ihBase := oh*stride - pad
			for ow := range outW {
				iwBase := ow*stride - pad
				var acc int32
				for kh := range k {
					ih := ihBase + kh
					if ih < 0 || ih >= inH {
						continue
					}
					for kw := range k {
						iw := iwBase + kw
						if iw >= 0 && iw < inW {
							acc += int32(l.Weights[wBase+kh*k+kw]) * int32(xq[inOff+ih*inW+iw])
						}
					}
				}
				dst[outOff+oh*outW+ow] = applyActivation(float32(acc)*scale+bias, l.Activation)
			}
		}
	}
}

func runConv2D(dst []float32, xq []int8, sx float32, inC, outC, inH, inW, outH, outW, k, stride, pad int, l *czmlLayer) {
	inPlane := inH * inW
	outPlane := outH * outW
	if k == 1 && stride == 1 {
		runPointwise2D(dst, xq, sx, inC, outC, outPlane, l)
		return
	}
	kk := k * k
	wPerOut := inC * kk
	for o := range outC {
		wBase := o * wPerOut
		outOff := o * outPlane
		scale := l.WeightScales[o] * sx
		bias := l.Biases[o]
		for oh := range outH {
			ihBase := oh*stride - pad
			for ow := range outW {
				iwBase := ow*stride - pad
				var acc int32
				for c := range inC {
					inOff := c * inPlane
					wc := wBase + c*kk
					for kh := range k {
						ih := ihBase + kh
						if ih < 0 || ih >= inH {
							continue
						}
						for kw := range k {
							iw := iwBase + kw
							if iw >= 0 && iw < inW {
								acc += int32(l.Weights[wc+kh*k+kw]) * int32(xq[inOff+ih*inW+iw])
							}
						}
					}
				}
				dst[outOff+oh*outW+ow] = applyActivation(float32(float32(acc)*scale)+bias, l.Activation)
			}
		}
	}
}

func runPointwise2D(dst []float32, xq []int8, sx float32, inC, outC, plane int, l *czmlLayer) {
	var acc [16 * 18]int32
	for o := range outC {
		wRow := l.Weights[o*inC : (o+1)*inC]
		scale := l.WeightScales[o] * sx
		bias := l.Biases[o]
		outRow := dst[o*plane : (o+1)*plane]
		clear(acc[:plane])
		for c := range inC {
			w := int32(wRow[c])
			if w == 0 {
				continue
			}
			inRow := xq[c*plane : (c+1)*plane]
			for p, xv := range inRow {
				acc[p] += w * int32(xv)
			}
		}
		for p := range plane {
			outRow[p] = applyActivation(float32(float32(acc[p])*scale)+bias, l.Activation)
		}
	}
}

func clampUnit32(v float32) float32 {
	if v < 0 {
		return 0
	}
	if v > 1 {
		return 1
	}
	return v
}
