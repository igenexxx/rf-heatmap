package subfile

import (
	"fmt"
	"math"
)

const (
	keeLoqMinPreamblePulses = 10
	keeLoqTotalBits         = 66
	keeLoqMinTE             = 250
	keeLoqMaxTE             = 500

	somfyTotalBits    = 56
	somfyHalfTE       = 640
	somfyHwSyncUs     = 2560
	somfySwSyncHighUs = 4550
)

type keeLoqDecoder struct{}

func (keeLoqDecoder) Name() string { return "KeeLoq" }

func (keeLoqDecoder) Decode(pulses []int32) (DecodedKey, bool) {
	const minNeeded = keeLoqMinPreamblePulses + 1 + keeLoqTotalBits*2
	if len(pulses) < minNeeded {
		return DecodedKey{}, false
	}
	for i := 0; i+minNeeded <= len(pulses); i++ {
		hdrIdx, te, ok := matchKeeLoqPreamble(pulses, i)
		if !ok || hdrIdx+1+keeLoqTotalBits*2 > len(pulses) {
			continue
		}
		if k, ok := decodeKeeLoqPayload(pulses[hdrIdx+1:], te); ok {
			return k, true
		}
	}
	return DecodedKey{}, false
}

func matchKeeLoqPreamble(pulses []int32, start int) (int, int32, bool) {
	first := absInt32(pulses[start])
	if first < keeLoqMinTE || first > keeLoqMaxTE {
		return 0, 0, false
	}
	var sum int64
	count := 0
	idx := start
	for idx < len(pulses) && count < 32 {
		v := absInt32(pulses[idx])
		if v < keeLoqMinTE || v > keeLoqMaxTE {
			break
		}
		if count > 0 {
			avg := float64(sum) / float64(count)
			if math.Abs(float64(v)-avg) > avg*0.35 {
				break
			}
		}
		sum += int64(v)
		count++
		idx++
	}
	if count < keeLoqMinPreamblePulses || idx >= len(pulses) {
		return 0, 0, false
	}
	te := int32(math.Round(float64(sum) / float64(count)))
	gap := -pulses[idx]
	if gap < te*7 || gap > te*14 {
		return 0, 0, false
	}
	return idx, te, true
}

func decodeKeeLoqPayload(frame []int32, te int32) (DecodedKey, bool) {
	var enc uint32
	var serial uint32
	var btn uint8
	for b := range keeLoqTotalBits {
		hi := frame[2*b]
		lo := -frame[2*b+1]
		if hi <= 0 || lo <= 0 {
			return DecodedKey{}, false
		}
		bit, ok := classifyKeeLoqBit(hi, lo, te)
		if !ok {
			return DecodedKey{}, false
		}
		switch {
		case b < 32:
			enc |= uint32(bit) << uint(b)
		case b < 60:
			serial |= uint32(bit) << uint(b-32)
		case b < 64:
			btn |= uint8(bit) << uint(b-60)
		}
	}
	packed := (uint64(enc) << 32) | (uint64(serial) << 4) | uint64(btn)
	return DecodedKey{
		Protocol:     "KeeLoq",
		Bits:         keeLoqTotalBits,
		KeyHex:       formatKeyHex(packed, 8),
		TE:           int(te),
		IsRolling:    true,
		SerialHex:    fmt.Sprintf("%07X", serial),
		EncryptedHex: fmt.Sprintf("%08X", enc),
		Button:       btn,
	}, true
}

func classifyKeeLoqBit(hi, lo, te int32) (uint8, bool) {
	shortP := min(hi, lo)
	if float64(shortP) < float64(te)*0.55 || float64(shortP) > float64(te)*1.45 {
		return 0, false
	}
	ratio := float64(hi) / float64(lo)
	if ratio >= 1.45 && ratio <= 2.85 {
		return 0, true // 2*TE high, 1*TE low
	}
	if ratio <= 0.69 && ratio >= 0.35 {
		return 1, true // 1*TE high, 2*TE low
	}
	return 0, false
}

// EncodeKeeLoqPulses generates a valid 66-bit KeeLoq/HCS301 pulse train for tests and SIM.
func EncodeKeeLoqPulses(enc, serial uint32, btn uint8, te int32) []int32 {
	if te < keeLoqMinTE || te > keeLoqMaxTE {
		te = 400
	}
	out := make([]int32, 0, 24+1+keeLoqTotalBits*2+2)
	for i := range 23 {
		if i%2 == 0 {
			out = append(out, te)
		} else {
			out = append(out, -te)
		}
	}
	out = append(out, -te*10) // header gap
	for b := range keeLoqTotalBits {
		var bit uint8
		switch {
		case b < 32:
			bit = uint8((enc >> uint(b)) & 1)
		case b < 60:
			bit = uint8((serial >> uint(b-32)) & 1)
		case b < 64:
			bit = (btn >> uint(b-60)) & 1
		}
		if bit == 1 {
			out = append(out, te, -2*te)
		} else {
			out = append(out, 2*te, -te)
		}
	}
	out = append(out, -te*20)
	return out
}

type somfyTelisDecoder struct{}

func (somfyTelisDecoder) Name() string { return "Somfy Telis" }

func (somfyTelisDecoder) Decode(pulses []int32) (DecodedKey, bool) {
	for i := 0; i+6 < len(pulses); i++ {
		swIdx, ok := matchSomfySync(pulses, i)
		if !ok {
			continue
		}
		raw, ok := decodeSomfyManchester(pulses[swIdx+1:])
		if !ok {
			continue
		}
		if k, ok := parseSomfyFrame(raw); ok {
			return k, true
		}
	}
	return DecodedKey{}, false
}

func matchSomfySync(pulses []int32, start int) (int, bool) {
	hwPairs := 0
	idx := start
	for idx+1 < len(pulses) && hwPairs < 8 {
		if !withinTol(pulses[idx], somfyHwSyncUs, 0.30) || !withinTol(-pulses[idx+1], somfyHwSyncUs, 0.30) {
			break
		}
		hwPairs++
		idx += 2
	}
	if hwPairs < 2 || idx+1 >= len(pulses) {
		return 0, false
	}
	if !withinTol(pulses[idx], somfySwSyncHighUs, 0.30) {
		return 0, false
	}
	return idx, true
}

func decodeSomfyManchester(tail []int32) ([7]byte, bool) {
	var frame [7]byte
	if len(tail) < 20 || tail[0] >= 0 {
		return frame, false
	}
	halfs := make([]int8, 0, somfyTotalBits*2+2)
	// Software sync ends with -1*TE; if merged with bit 0's leading -1*TE, tail[0] is -2*TE.
	firstLow := -tail[0]
	if withinTol(firstLow, somfyHalfTE*2, 0.30) {
		halfs = append(halfs, -1)
	} else if !withinTol(firstLow, somfyHalfTE, 0.35) {
		return frame, false
	}
	for _, p := range tail[1:] {
		if len(halfs) >= somfyTotalBits*2 {
			break
		}
		n, ok := somfyHalfCount(absInt32(p))
		if !ok {
			break
		}
		sign := int8(1)
		if p < 0 {
			sign = -1
		}
		for range n {
			halfs = append(halfs, sign)
		}
	}
	if len(halfs) < somfyTotalBits*2 {
		return frame, false
	}
	for b := range somfyTotalBits {
		h0, h1 := halfs[2*b], halfs[2*b+1]
		if h0 == h1 {
			return frame, false
		}
		if h0 < 0 && h1 > 0 {
			frame[b/8] |= 1 << uint(7-(b%8))
		}
	}
	return frame, true
}

func somfyHalfCount(d int32) (int, bool) {
	if withinTol(d, somfyHalfTE, 0.35) {
		return 1, true
	}
	if withinTol(d, somfyHalfTE*2, 0.30) {
		return 2, true
	}
	return 0, false
}

func parseSomfyFrame(obf [7]byte) (DecodedKey, bool) {
	frame := obf
	for i := 6; i >= 1; i-- {
		frame[i] ^= frame[i-1]
	}
	var cks uint8
	for _, b := range frame {
		cks ^= (b >> 4) ^ (b & 0x0F)
	}
	if cks != 0 {
		return DecodedKey{}, false
	}
	btn := frame[1] >> 4
	cnt := uint16(frame[2])<<8 | uint16(frame[3])
	serial := fmt.Sprintf("%02X%02X%02X", frame[4], frame[5], frame[6])
	var packed uint64
	for _, b := range frame {
		packed = (packed << 8) | uint64(b)
	}
	return DecodedKey{
		Protocol:  "Somfy Telis",
		Bits:      somfyTotalBits,
		KeyHex:    formatKeyHex(packed, 7),
		TE:        somfyHalfTE,
		IsRolling: true,
		SerialHex: serial,
		Counter:   cnt,
		Button:    btn,
	}, true
}

// EncodeSomfyTelisPulses generates a valid 56-bit Manchester Somfy Telis RTS pulse train.
func EncodeSomfyTelisPulses(key uint8, btn uint8, counter uint16, serial uint32) []int32 {
	var frame [7]byte
	frame[0] = key
	frame[1] = (btn & 0x0F) << 4
	frame[2] = uint8(counter >> 8)
	frame[3] = uint8(counter)
	frame[4] = uint8((serial >> 16) & 0xFF)
	frame[5] = uint8((serial >> 8) & 0xFF)
	frame[6] = uint8(serial & 0xFF)
	var cks uint8
	for _, b := range frame {
		cks ^= (b >> 4) ^ (b & 0x0F)
	}
	frame[1] |= cks & 0x0F
	for i := 1; i < 7; i++ {
		frame[i] ^= frame[i-1]
	}
	out := []int32{somfyHwSyncUs, -somfyHwSyncUs, somfyHwSyncUs, -somfyHwSyncUs, somfySwSyncHighUs}
	halfs := make([]int8, 0, somfyTotalBits*2+1)
	halfs = append(halfs, -1) // software sync low half
	for b := range somfyTotalBits {
		bit := (frame[b/8] >> uint(7-(b%8))) & 1
		if bit == 1 {
			halfs = append(halfs, -1, 1)
		} else {
			halfs = append(halfs, 1, -1)
		}
	}
	for _, h := range halfs {
		dur := int32(h) * somfyHalfTE
		if len(out) > 0 && ((out[len(out)-1] > 0 && dur > 0) || (out[len(out)-1] < 0 && dur < 0)) {
			out[len(out)-1] += dur
		} else {
			out = append(out, dur)
		}
	}
	out = append(out, -30000)
	return out
}

func absInt32(v int32) int32 {
	if v < 0 {
		if v == math.MinInt32 {
			return math.MaxInt32
		}
		return -v
	}
	return v
}

func withinTol(val, target int32, tol float64) bool {
	if val <= 0 {
		return false
	}
	lo := float64(target) * (1.0 - tol)
	hi := float64(target) * (1.0 + tol)
	return float64(val) >= lo && float64(val) <= hi
}
