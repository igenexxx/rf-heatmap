package sensors

import "fmt"

const (
	tpmsHalfTE      int32 = 52 // ~9.6 kbps Manchester half-bit (~52 us)
	schraderSync    uint8 = 0xF5
	schraderCRC8          = 0x07
	schraderBytes         = 8
	toyotaSync      uint8 = 0x55
	toyotaCRC8            = 0x13
	toyotaBytes           = 9
	tpmsPressureMul       = 2.5
)

type schraderTPMSDecoder struct{}

func (schraderTPMSDecoder) Name() string { return "Schrader" }

func (schraderTPMSDecoder) Decode(pulses []int32, freqHz uint32) (Reading, bool) {
	frame, ok := extractManchesterFrame(pulses, tpmsHalfTE, schraderSync, schraderBytes)
	if !ok {
		return Reading{}, false
	}
	if crc8(frame[:7], schraderCRC8, 0x00) != frame[7] {
		return Reading{}, false
	}
	id := uint32(frame[1])<<16 | uint32(frame[2])<<8 | uint32(frame[3])
	flags := frame[4]
	kpa := round1(float64(frame[5]) * tpmsPressureMul)
	tempC := round1(float64(frame[6]) - 50.0)
	return Reading{
		Kind:        KindTPMS,
		Protocol:    "Schrader",
		SensorID:    fmt.Sprintf("%06X", id),
		FreqHz:      freqHz,
		PressureKPa: kpa,
		PressurePSI: KPaToPSI(kpa),
		TempC:       tempC,
		BatteryOK:   (flags & 0x01) == 0,
		ReadingMeta: ReadingMeta{Flags: flags},
	}, true
}

type toyotaTPMSDecoder struct{}

func (toyotaTPMSDecoder) Name() string { return "Toyota" }

func (toyotaTPMSDecoder) Decode(pulses []int32, freqHz uint32) (Reading, bool) {
	frame, ok := extractManchesterFrame(pulses, tpmsHalfTE, toyotaSync, toyotaBytes)
	if !ok {
		return Reading{}, false
	}
	if crc8(frame[:8], toyotaCRC8, 0x80) != frame[8] {
		return Reading{}, false
	}
	id := uint32(frame[1])<<24 | uint32(frame[2])<<16 | uint32(frame[3])<<8 | uint32(frame[4])
	flags := frame[5]
	kpa := round1(float64(frame[6]) * tpmsPressureMul)
	tempC := round1(float64(frame[7]) - 40.0)
	return Reading{
		Kind:        KindTPMS,
		Protocol:    "Toyota",
		SensorID:    fmt.Sprintf("%08X", id),
		FreqHz:      freqHz,
		PressureKPa: kpa,
		PressurePSI: KPaToPSI(kpa),
		TempC:       tempC,
		BatteryOK:   (flags & 0x80) == 0,
		ReadingMeta: ReadingMeta{Flags: flags},
	}, true
}

func extractManchesterFrame(pulses []int32, halfTE int32, syncByte uint8, wantBytes int) ([]byte, bool) {
	halfs := pulsesToHalfBits(pulses, halfTE)
	needHalfs := wantBytes * 16
	if len(halfs) < needHalfs {
		return nil, false
	}
	for off := 0; off+needHalfs <= len(halfs); off++ {
		firstByte, ok := decodeManchesterByte(halfs[off : off+16])
		if !ok || firstByte != syncByte {
			continue
		}
		out := make([]byte, wantBytes)
		out[0] = firstByte
		valid := true
		for i := 1; i < wantBytes; i++ {
			b, ok := decodeManchesterByte(halfs[off+i*16 : off+(i+1)*16])
			if !ok {
				valid = false
				break
			}
			out[i] = b
		}
		if valid {
			return out, true
		}
	}
	return nil, false
}

func pulsesToHalfBits(pulses []int32, halfTE int32) []int8 {
	halfs := make([]int8, 0, len(pulses)*2)
	for _, p := range pulses {
		d := absInt32(p)
		sign := int8(1)
		if p < 0 {
			sign = -1
		}
		switch {
		case withinTol(d, halfTE, 0.35):
			halfs = append(halfs, sign)
		case withinTol(d, halfTE*2, 0.30):
			halfs = append(halfs, sign, sign)
		default:
			if len(halfs) >= 64 {
				// Trailing idle gap supplies the final half-bit of the last symbol.
				return append(halfs, sign)
			}
			// Leading idle gap supplies the initial half-bit before the first edge.
			halfs = append(halfs[:0], sign)
		}
	}
	return halfs
}

func decodeManchesterByte(halfs []int8) (byte, bool) {
	var b byte
	for i := range 8 {
		h0, h1 := halfs[2*i], halfs[2*i+1]
		if h0 == h1 {
			return 0, false
		}
		b <<= 1
		if h0 < 0 && h1 > 0 {
			b |= 1 // rising edge = 1
		}
	}
	return b, true
}

func encodeManchesterPulses(data []byte, halfTE int32) []int32 {
	out := []int32{-halfTE * 20}
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			var h0, h1 int32
			if (b>>uint(i))&1 == 1 {
				h0, h1 = -halfTE, halfTE
			} else {
				h0, h1 = halfTE, -halfTE
			}
			out = appendMergedPulse(out, h0)
			out = appendMergedPulse(out, h1)
		}
	}
	return appendMergedPulse(out, -halfTE*20)
}

func appendMergedPulse(out []int32, p int32) []int32 {
	if len(out) > 0 && ((out[len(out)-1] > 0 && p > 0) || (out[len(out)-1] < 0 && p < 0)) {
		out[len(out)-1] += p
		return out
	}
	return append(out, p)
}

// EncodeSchraderTPMS builds a valid Schrader Manchester TPMS pulse train.
func EncodeSchraderTPMS(id uint32, kpa, tempC float64, flags uint8) []int32 {
	pRaw := uint8(min(255, max(0, int(kpa/tpmsPressureMul+0.5))))
	tRaw := uint8(min(255, max(0, int(tempC+50.5))))
	frame := []byte{
		schraderSync,
		uint8((id >> 16) & 0xFF),
		uint8((id >> 8) & 0xFF),
		uint8(id & 0xFF),
		flags,
		pRaw,
		tRaw,
		0,
	}
	frame[7] = crc8(frame[:7], schraderCRC8, 0x00)
	return encodeManchesterPulses(frame, tpmsHalfTE)
}

// EncodeToyotaTPMS builds a valid Toyota Manchester TPMS pulse train.
func EncodeToyotaTPMS(id uint32, kpa, tempC float64, flags uint8) []int32 {
	pRaw := uint8(min(255, max(0, int(kpa/tpmsPressureMul+0.5))))
	tRaw := uint8(min(255, max(0, int(tempC+40.5))))
	frame := []byte{
		toyotaSync,
		uint8((id >> 24) & 0xFF),
		uint8((id >> 16) & 0xFF),
		uint8((id >> 8) & 0xFF),
		uint8(id & 0xFF),
		flags,
		pRaw,
		tRaw,
		0,
	}
	frame[8] = crc8(frame[:8], toyotaCRC8, 0x80)
	return encodeManchesterPulses(frame, tpmsHalfTE)
}
