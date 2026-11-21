package subfile

import (
	"fmt"
	"math"

	"rf-heatmap/internal/rfml"
)

// DecodedKey represents a decoded fixed-code OOK remote frame.
type DecodedKey struct {
	Protocol string
	Bits     int
	KeyHex   string
	TE       int
	Denoised bool // True when recovered via 1D K-Means glitch-denoising & quantization
}

// DecodeOOK attempts to decode common OOK PWM/Manchester remote protocols from signed µs pulses.
// Positive values = high (carrier on), negative values = low (carrier off).
// If strict decoding fails due to jitter or EMI glitches, it falls back to 1D K-Means quantization.
func DecodeOOK(pulses []int32) (DecodedKey, bool) {
	if len(pulses) < 20 {
		return DecodedKey{}, false
	}
	if k, ok := decodeOOKStrict(pulses); ok {
		return k, true
	}

	// Second-pass ML fallback: merge EMI glitches and quantize jittery pulses to 1D K-Means centroids.
	cl := rfml.ClusterTimings(pulses, rfml.DefaultMinGlitchUs)
	if cl.Valid && cl.LongShortRatio >= 1.5 && cl.LongShortRatio <= 5.0 {
		if q := rfml.QuantizePulses(pulses, cl, rfml.DefaultMinGlitchUs); len(q) >= 20 {
			if k, ok := decodeOOKStrict(q); ok {
				k.Denoised = true
				return k, true
			}
		}
	}
	return DecodedKey{}, false
}

func decodeOOKStrict(pulses []int32) (DecodedKey, bool) {
	// Princeton PT2262: 24-bit, 1:3 / 3:1 pulse ratio, sync gap > 4000 µs.
	if k, ok := decodeRatioOOK(pulses, "Princeton", 24, -4000, 80); ok {
		return k, true
	}
	// CAME 12-bit / 24-bit: inverted PWM, sync gap > 5000 µs.
	if k, ok := decodeCAME(pulses); ok {
		return k, true
	}
	// Linear 10-bit: 1:3 / 3:1 pulse ratio, sync gap > 8000 µs.
	if k, ok := decodeRatioOOK(pulses, "Linear", 10, -8000, 150); ok {
		return k, true
	}
	return DecodedKey{}, false
}

func decodeRatioOOK(pulses []int32, proto string, bitCount int, maxSyncNeg, minPulseUs int32) (DecodedKey, bool) {
	need := bitCount*2 + 1
	for i := 0; i+need < len(pulses); i++ {
		if pulses[i] >= maxSyncNeg || pulses[i+1] <= 0 {
			continue
		}
		if bits, te, ok := decodeRatioFrame(pulses[i+1:], bitCount, minPulseUs); ok {
			return DecodedKey{
				Protocol: proto,
				Bits:     bitCount,
				KeyHex:   formatKeyHex(bits, 8),
				TE:       te,
			}, true
		}
	}
	return DecodedKey{}, false
}

func decodeRatioFrame(frame []int32, bitCount int, minPulseUs int32) (uint64, int, bool) {
	var bits uint64
	var teSum int64
	for b := range bitCount {
		hi := frame[2*b]
		lo := -frame[2*b+1]
		if hi <= minPulseUs || lo <= minPulseUs {
			return 0, 0, false
		}
		ratio := float64(hi) / float64(lo)
		if ratio >= 2.0 && ratio <= 4.5 {
			bits = (bits << 1) | 1
			teSum += int64(lo)
		} else if ratio <= 0.5 && ratio >= 0.2 {
			bits <<= 1
			teSum += int64(hi)
		} else {
			return 0, 0, false
		}
	}
	return bits, int(math.Round(float64(teSum) / float64(bitCount))), true
}

func decodeCAME(pulses []int32) (DecodedKey, bool) {
	for _, bitCount := range []int{24, 12} {
		need := bitCount * 2
		for i := 0; i+need < len(pulses); i++ {
			if pulses[i] >= -5000 || pulses[i+1] <= 0 {
				continue
			}
			minP := estimateMinPulse(pulses[i+1 : i+1+need])
			if minP < 150 || minP > 600 {
				continue
			}
			if bits, ok := decodeCAMEFrame(pulses[i+1:], bitCount); ok {
				return DecodedKey{
					Protocol: "CAME",
					Bits:     bitCount,
					KeyHex:   formatKeyHex(bits, 8),
					TE:       int(minP),
				}, true
			}
		}
	}
	return DecodedKey{}, false
}

func estimateMinPulse(window []int32) int32 {
	minP := int32(100000)
	for _, v := range window {
		if v < 0 {
			v = -v
		}
		minP = min(minP, v)
	}
	return minP
}

func decodeCAMEFrame(frame []int32, bitCount int) (uint64, bool) {
	var bits uint64
	for b := range bitCount {
		lo := -frame[2*b]
		hi := frame[2*b+1]
		if hi <= 0 || lo <= 0 {
			hi = frame[2*b]
			lo = -frame[2*b+1]
		}
		if hi <= 100 || lo <= 100 {
			return 0, false
		}
		if float64(hi) > float64(lo)*1.6 {
			bits = (bits << 1) | 1
		} else if float64(lo) > float64(hi)*1.6 {
			bits <<= 1
		} else {
			return 0, false
		}
	}
	return bits, true
}

func formatKeyHex(v uint64, bytes int) string {
	out := make([]byte, 0, bytes*3)
	for i := bytes - 1; i >= 0; i-- {
		if len(out) > 0 {
			out = append(out, ' ')
		}
		b := uint8((v >> (uint(i) * 8)) & 0xFF)
		out = append(out, fmt.Sprintf("%02X", b)...)
	}
	return string(out)
}
