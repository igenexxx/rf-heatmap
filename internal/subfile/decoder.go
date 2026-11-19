package subfile

import (
	"fmt"
	"math"
)

// DecodedKey represents a decoded fixed-code OOK remote frame.
type DecodedKey struct {
	Protocol string
	Bits     int
	KeyHex   string
	TE       int
}

// DecodeOOK attempts to decode common OOK PWM/Manchester remote protocols from signed µs pulses.
// Positive values = high (carrier on), negative values = low (carrier off).
func DecodeOOK(pulses []int32) (DecodedKey, bool) {
	if len(pulses) < 20 {
		return DecodedKey{}, false
	}
	// Try Princeton PT2262 (24-bit, 1:3 / 3:1 pulse ratio, long sync gap ~30*TE).
	if k, ok := decodePrinceton(pulses); ok {
		return k, true
	}
	// Try CAME 12-bit / 24-bit (inverted PWM: short high=0, long high=1, sync gap ~36*TE).
	if k, ok := decodeCAME(pulses); ok {
		return k, true
	}
	// Try Linear 10-bit (1:3 / 3:1).
	if k, ok := decodeLinear(pulses); ok {
		return k, true
	}
	return DecodedKey{}, false
}

func decodePrinceton(pulses []int32) (DecodedKey, bool) {
	// Look for a long negative sync gap (> 4000 µs) followed by 24 high/low pairs.
	for i := 0; i+49 < len(pulses); i++ {
		if pulses[i] >= -4000 {
			continue
		}
		// Need 24 pairs starting at i+1 (each pair: +high, -low).
		if pulses[i+1] <= 0 {
			continue
		}
		var bits uint64
		var teSum int64
		valid := true
		for b := 0; b < 24; b++ {
			hi := pulses[i+1+2*b]
			lo := -pulses[i+1+2*b+1]
			if hi <= 80 || lo <= 80 {
				valid = false
				break
			}
			ratio := float64(hi) / float64(lo)
			if ratio >= 2.0 && ratio <= 4.5 {
				// 3:1 -> 1
				bits = (bits << 1) | 1
				teSum += int64(lo)
			} else if ratio <= 0.5 && ratio >= 0.2 {
				// 1:3 -> 0
				bits <<= 1
				teSum += int64(hi)
			} else {
				valid = false
				break
			}
		}
		if valid {
			te := int(math.Round(float64(teSum) / 24.0))
			return DecodedKey{
				Protocol: "Princeton",
				Bits:     24,
				KeyHex:   formatKeyHex(bits, 8),
				TE:       te,
			}, true
		}
	}
	return DecodedKey{}, false
}

func decodeCAME(pulses []int32) (DecodedKey, bool) {
	for _, bitCount := range []int{24, 12} {
		need := bitCount * 2
		for i := 0; i+need < len(pulses); i++ {
			if pulses[i] >= -5000 {
				continue
			}
			if pulses[i+1] <= 0 {
				continue
			}
			// First bit in CAME is a start/preamble or data; estimate TE from shortest pulse.
			minP := int32(100000)
			for k := 0; k < need; k++ {
				v := pulses[i+1+k]
				if v < 0 {
					v = -v
				}
				if v < minP {
					minP = v
				}
			}
			if minP < 150 || minP > 600 {
				continue
			}
			var bits uint64
			valid := true
			for b := 0; b < bitCount; b++ {
				lo := -pulses[i+1+2*b]
				hi := pulses[i+2+2*b]
				// Support either (+hi, -lo) or (-lo, +hi) depending on sync alignment.
				if hi <= 0 || lo <= 0 {
					hi = pulses[i+1+2*b]
					lo = -pulses[i+2+2*b]
				}
				if hi <= 100 || lo <= 100 {
					valid = false
					break
				}
				if float64(hi) > float64(lo)*1.6 {
					bits = (bits << 1) | 1
				} else if float64(lo) > float64(hi)*1.6 {
					bits <<= 1
				} else {
					valid = false
					break
				}
			}
			if valid {
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

func decodeLinear(pulses []int32) (DecodedKey, bool) {
	for i := 0; i+21 < len(pulses); i++ {
		if pulses[i] >= -8000 {
			continue
		}
		if pulses[i+1] <= 0 {
			continue
		}
		var bits uint64
		var teSum int64
		valid := true
		for b := 0; b < 10; b++ {
			hi := pulses[i+1+2*b]
			lo := -pulses[i+2+2*b]
			if hi <= 150 || lo <= 150 {
				valid = false
				break
			}
			ratio := float64(hi) / float64(lo)
			if ratio >= 2.0 && ratio <= 4.5 {
				bits = (bits << 1) | 1
				teSum += int64(lo)
			} else if ratio <= 0.5 && ratio >= 0.2 {
				bits <<= 1
				teSum += int64(hi)
			} else {
				valid = false
				break
			}
		}
		if valid {
			return DecodedKey{
				Protocol: "Linear",
				Bits:     10,
				KeyHex:   formatKeyHex(bits, 8),
				TE:       int(math.Round(float64(teSum) / 10.0)),
			}, true
		}
	}
	return DecodedKey{}, false
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
