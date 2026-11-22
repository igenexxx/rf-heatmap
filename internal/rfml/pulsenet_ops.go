package rfml

import "math"

// quantizeDynamicInt8 quantizes src[0:n] into qDst[0:n] using symmetric per-tensor
// INT8 scaling matching PyTorch/NumPy QAT bit-for-bit. Zero allocations.
func quantizeDynamicInt8(src []float32, qDst []int8, n int) float32 {
	var maxAbs float32
	for i := range n {
		v := src[i]
		if v < 0 {
			v = -v
		}
		if v > maxAbs {
			maxAbs = v
		}
	}
	if maxAbs < 1e-8 {
		clear(qDst[:n])
		return 0
	}
	scale := maxAbs / 127.0
	for i := range n {
		r := math.Round(float64(src[i] / scale))
		if r > 127 {
			r = 127
		} else if r < -127 {
			r = -127
		}
		qDst[i] = int8(r)
	}
	return scale
}

func applyActivation(v float32, act uint8) float32 {
	switch act {
	case actReLU6:
		if v <= 0 {
			return 0
		}
		if v >= 6 {
			return 6
		}
		return v
	case actTanh:
		return float32(math.Tanh(float64(v)))
	default:
		return v
	}
}

// runLayer1D executes Conv1D, DepthwiseConv1D, or PointwiseConv1D with 0 allocations.
func runLayer1D(dst, src []float32, qBuf []int8, inLen int, l *czmlLayer) int {
	inC := int(l.InChannels)
	outC := int(l.OutChannels)
	sx := quantizeDynamicInt8(src, qBuf, inC*inLen)
	switch l.OpType {
	case opPointwiseConv1D:
		runPointwise1D(dst, qBuf, sx, inC, outC, inLen, l)
		return inLen
	case opDepthwiseConv1D:
		return runDepthwise1D(dst, qBuf, sx, outC, inLen, l)
	default:
		return runConv1D(dst, qBuf, sx, inC, outC, inLen, l)
	}
}

func runPointwise1D(dst []float32, xq []int8, sx float32, inC, outC, seqLen int, l *czmlLayer) {
	weights := l.Weights
	var acc [PulseNetSeqLen]int32
	for o := range outC {
		wRow := weights[o*inC : (o+1)*inC]
		scale := l.WeightScales[o] * sx
		bias := l.Biases[o]
		outRow := dst[o*seqLen : (o+1)*seqLen]
		clear(acc[:seqLen])
		for c := range inC {
			w := int32(wRow[c])
			if w == 0 {
				continue
			}
			inRow := xq[c*seqLen : (c+1)*seqLen]
			for t, xv := range inRow {
				acc[t] += w * int32(xv)
			}
		}
		for t := range seqLen {
			outRow[t] = applyActivation(float32(float32(acc[t])*scale)+bias, l.Activation)
		}
	}
}

func runDepthwise1D(dst []float32, xq []int8, sx float32, channels, inLen int, l *czmlLayer) int {
	k := int(l.KernelSize)
	stride := int(l.Stride)
	dil := int(l.Dilation)
	pad := (k / 2) * dil
	outLen := inLen / stride
	weights := l.Weights

	for c := range channels {
		wRow := weights[c*k : (c+1)*k]
		inRow := xq[c*inLen : (c+1)*inLen]
		outRow := dst[c*outLen : (c+1)*outLen]
		scale := l.WeightScales[c] * sx
		bias := l.Biases[c]
		for to := range outLen {
			base := to*stride - pad
			var acc int32
			for ki := range k {
				ti := base + ki*dil
				if ti >= 0 && ti < inLen {
					acc += int32(wRow[ki]) * int32(inRow[ti])
				}
			}
			outRow[to] = applyActivation(float32(float32(acc)*scale)+bias, l.Activation)
		}
	}
	return outLen
}

func runConv1D(dst []float32, xq []int8, sx float32, inC, outC, inLen int, l *czmlLayer) int {
	k := int(l.KernelSize)
	stride := int(l.Stride)
	dil := int(l.Dilation)
	pad := (k / 2) * dil
	outLen := inLen / stride
	weights := l.Weights
	wPerOut := inC * k
	var acc [PulseNetSeqLen]int32

	for o := range outC {
		wBase := o * wPerOut
		outRow := dst[o*outLen : (o+1)*outLen]
		scale := l.WeightScales[o] * sx
		bias := l.Biases[o]
		clear(acc[:outLen])
		for c := range inC {
			inRow := xq[c*inLen : (c+1)*inLen]
			wc := weights[wBase+c*k : wBase+(c+1)*k]
			for ki := range k {
				w := int32(wc[ki])
				if w == 0 {
					continue
				}
				off := ki*dil - pad
				for to := range outLen {
					if ti := to*stride + off; ti >= 0 && ti < inLen {
						acc[to] += w * int32(inRow[ti])
					}
				}
			}
		}
		for to := range outLen {
			outRow[to] = applyActivation(float32(float32(acc[to])*scale)+bias, l.Activation)
		}
	}
	return outLen
}

// runLinear executes a quantized INT8 matrix-vector multiply with 0 allocations.
func runLinear(dst, src []float32, qBuf []int8, l *czmlLayer) {
	inF := int(l.InChannels)
	outF := int(l.OutChannels)
	sx := quantizeDynamicInt8(src, qBuf, inF)
	weights := l.Weights
	q := qBuf[:inF]

	for o := range outF {
		wRow := weights[o*inF : (o+1)*inF]
		var acc int32
		for i, w := range wRow {
			acc += int32(w) * int32(q[i])
		}
		dst[o] = applyActivation(float32(float32(acc)*(l.WeightScales[o]*sx))+l.Biases[o], l.Activation)
	}
}

func addResidualReLU6(dst, res []float32, n int) {
	for i := range n {
		v := dst[i] + res[i]
		if v < 0 {
			v = 0
		} else if v > 6 {
			v = 6
		}
		dst[i] = v
	}
}

func globalAvgMaxPool(dst, src []float32, channels, seqLen int) {
	invLen := float32(1.0 / float64(seqLen))
	for c := range channels {
		row := src[c*seqLen : (c+1)*seqLen]
		sum := row[0]
		mx := row[0]
		for t := 1; t < seqLen; t++ {
			v := row[t]
			sum += v
			if v > mx {
				mx = v
			}
		}
		dst[c] = sum * invLen
		dst[channels+c] = mx
	}
}

func upsample2xConcat(dst, low []float32, lowC, lowLen int, skip []float32, skipC int) {
	outLen := lowLen * 2
	for c := range lowC {
		inRow := low[c*lowLen : (c+1)*lowLen]
		outRow := dst[c*outLen : (c+1)*outLen]
		for t := range lowLen {
			v := inRow[t]
			outRow[t*2] = v
			outRow[t*2+1] = v
		}
	}
	copy(dst[lowC*outLen:(lowC+skipC)*outLen], skip[:skipC*outLen])
}

func softmaxTemperature(dst, logits []float32, n int, temp float32) (int, float32) {
	t := max(float64(temp), 1e-4)
	maxL := float64(logits[0]) / t
	for i := 1; i < n; i++ {
		v := float64(logits[i]) / t
		if v > maxL {
			maxL = v
		}
	}
	var sum float64
	for i := range n {
		e := math.Exp(float64(logits[i])/t - maxL)
		dst[i] = float32(e)
		sum += e
	}
	inv := float32(1.0 / sum)
	bestIdx := 0
	var bestProb float32
	for i := range n {
		dst[i] *= inv
		if dst[i] > bestProb {
			bestProb = dst[i]
			bestIdx = i
		}
	}
	return bestIdx, bestProb
}

func l2Normalize32(src []float32) [PulseNetEmbedDim]float32 {
	var out [PulseNetEmbedDim]float32
	var sumSq float64
	for i := range PulseNetEmbedDim {
		v := float64(src[i])
		sumSq += v * v
	}
	if sumSq <= 1e-12 {
		return out
	}
	inv := float32(1.0 / math.Sqrt(sumSq))
	for i := range PulseNetEmbedDim {
		out[i] = src[i] * inv
	}
	return out
}
