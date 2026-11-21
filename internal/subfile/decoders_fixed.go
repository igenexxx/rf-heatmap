package subfile

import (
	"fmt"
	"math"
)

type princetonDecoder struct{}

func (princetonDecoder) Name() string { return "Princeton" }

func (princetonDecoder) Decode(pulses []int32) (DecodedKey, bool) {
	k, bits, ok := decodeRatioOOK(pulses, "Princeton", 24, -4000, 80)
	if !ok {
		return DecodedKey{}, false
	}
	k.AddressHex = fmt.Sprintf("%05X", (bits>>4)&0xFFFFF)
	k.Button = uint8(bits & 0x0F)
	if tri, valid := formatTriState(bits, 12); valid {
		k.TriState = tri
	}
	return k, true
}

// DecodeEV1527 decodes a 24-bit EV1527 (20-bit address + 4-bit button) frame explicitly.
func DecodeEV1527(pulses []int32) (DecodedKey, bool) {
	k, ok := princetonDecoder{}.Decode(pulses)
	if !ok {
		return DecodedKey{}, false
	}
	k.Protocol = "EV1527"
	return k, true
}

// DecodePT2262 decodes a 24-bit PT2262 tri-state frame (requiring all 12 valid trits).
func DecodePT2262(pulses []int32) (DecodedKey, bool) {
	k, ok := princetonDecoder{}.Decode(pulses)
	if !ok || k.TriState == "" {
		return DecodedKey{}, false
	}
	k.Protocol = "PT2262"
	return k, true
}

func formatTriState(bits uint64, trits int) (string, bool) {
	buf := make([]byte, trits)
	for i := range trits {
		shift := uint((trits - 1 - i) * 2)
		pair := (bits >> shift) & 0x3
		switch pair {
		case 0b00:
			buf[i] = '0'
		case 0b11:
			buf[i] = '1'
		case 0b01:
			buf[i] = 'F'
		default:
			return "", false
		}
	}
	return string(buf), true
}

type linearDecoder struct{}

func (linearDecoder) Name() string { return "Linear" }

func (linearDecoder) Decode(pulses []int32) (DecodedKey, bool) {
	k, _, ok := decodeRatioOOK(pulses, "Linear", 10, -8000, 150)
	return k, ok
}

func decodeRatioOOK(pulses []int32, proto string, bitCount int, maxSyncNeg, minPulseUs int32) (DecodedKey, uint64, bool) {
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
			}, bits, true
		}
	}
	return DecodedKey{}, 0, false
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

type cameDecoder struct{}

func (cameDecoder) Name() string { return "CAME" }

func (cameDecoder) Decode(pulses []int32) (DecodedKey, bool) {
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
		if hi <= 100 || lo <= 100 || hi > 2500 || lo > 2500 {
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

type holtekDecoder struct{}

func (holtekDecoder) Name() string { return "Holtek" }

func (holtekDecoder) Decode(pulses []int32) (DecodedKey, bool) {
	if k, ok := decodeHoltekHT6P20B(pulses); ok {
		return k, true
	}
	return decodeHoltekHT12E(pulses)
}

func decodeHoltekHT6P20B(pulses []int32) (DecodedKey, bool) {
	const bitsCount = 28
	need := bitsCount * 2
	for i := 0; i+need < len(pulses); i++ {
		if pulses[i] >= -6000 || pulses[i+1] <= 0 {
			continue
		}
		bits, te, ok := decodeTwoToOneFrame(pulses[i+1:], bitsCount, 350, 580)
		if !ok || (bits&0x0F) != 0x05 {
			continue
		}
		return DecodedKey{
			Protocol:   "Holtek",
			Bits:       bitsCount,
			KeyHex:     formatKeyHex(bits, 8),
			TE:         te,
			AddressHex: fmt.Sprintf("%06X", (bits>>6)&0x3FFFFF),
			Button:     uint8((bits >> 4) & 0x03),
		}, true
	}
	return DecodedKey{}, false
}

func decodeHoltekHT12E(pulses []int32) (DecodedKey, bool) {
	const bitsCount = 12
	need := bitsCount*2 + 2
	for i := 0; i+need <= len(pulses); i++ {
		if pulses[i] >= -10000 || pulses[i+1] < 250 || pulses[i+1] > 600 {
			continue
		}
		bits, te, ok := decodeInvertedTwoToOne(pulses[i+2:], bitsCount, 280, 520)
		if !ok {
			continue
		}
		return DecodedKey{
			Protocol: "Holtek",
			Bits:     bitsCount,
			KeyHex:   formatKeyHex(bits, 8),
			TE:       te,
		}, true
	}
	return DecodedKey{}, false
}

type niceFLODecoder struct{}

func (niceFLODecoder) Name() string { return "Nice FLO" }

func (niceFLODecoder) Decode(pulses []int32) (DecodedKey, bool) {
	for _, bitCount := range []int{24, 12} {
		need := bitCount * 2
		for i := 0; i+need < len(pulses); i++ {
			if pulses[i] >= -18000 || pulses[i+1] <= 0 {
				continue
			}
			bits, te, ok := decodeTwoToOneFrame(pulses[i+1:], bitCount, 600, 800)
			if !ok {
				continue
			}
			return DecodedKey{
				Protocol: "Nice FLO",
				Bits:     bitCount,
				KeyHex:   formatKeyHex(bits, 8),
				TE:       te,
			}, true
		}
	}
	return DecodedKey{}, false
}

func decodeTwoToOneFrame(frame []int32, bitCount int, minTE, maxTE int32) (uint64, int, bool) {
	var bits uint64
	var teSum int64
	for b := range bitCount {
		hi := frame[2*b]
		lo := -frame[2*b+1]
		if hi <= 0 || lo <= 0 {
			return 0, 0, false
		}
		shortP := min(hi, lo)
		if shortP < minTE || shortP > maxTE {
			return 0, 0, false
		}
		ratio := float64(hi) / float64(lo)
		if ratio >= 1.5 && ratio <= 2.7 {
			bits = (bits << 1) | 1
		} else if ratio <= 0.67 && ratio >= 0.37 {
			bits <<= 1
		} else {
			return 0, 0, false
		}
		teSum += int64(shortP)
	}
	return bits, int(math.Round(float64(teSum) / float64(bitCount))), true
}

func decodeInvertedTwoToOne(frame []int32, bitCount int, minTE, maxTE int32) (uint64, int, bool) {
	var bits uint64
	var teSum int64
	for b := range bitCount {
		lo := -frame[2*b]
		hi := frame[2*b+1]
		if hi <= 0 || lo <= 0 {
			return 0, 0, false
		}
		shortP := min(hi, lo)
		if shortP < minTE || shortP > maxTE {
			return 0, 0, false
		}
		ratio := float64(hi) / float64(lo)
		if ratio >= 1.5 && ratio <= 2.7 {
			bits = (bits << 1) | 1
		} else if ratio <= 0.67 && ratio >= 0.37 {
			bits <<= 1
		} else {
			return 0, 0, false
		}
		teSum += int64(shortP)
	}
	return bits, int(math.Round(float64(teSum) / float64(bitCount))), true
}
