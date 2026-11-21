package sensors

import "fmt"

const (
	oregonHalfTE   int32 = 488
	acuriteShortTE int32 = 200
	laCrosseSync   int32 = 830
	laCrosseShort  int32 = 208
	laCrosseCRC8         = 0x31
)

type oregonDecoder struct{}

func (oregonDecoder) Name() string { return "Oregon" }

func (oregonDecoder) Decode(pulses []int32, freqHz uint32) (Reading, bool) {
	halfs := pulsesToHalfBits(pulses, oregonHalfTE)
	const needBytes = 10 // 2 preamble bytes (0xFF, 0xFA) + 8 payload bytes
	if len(halfs) < needBytes*16 {
		return Reading{}, false
	}
	for off := 0; off+needBytes*16 <= len(halfs); off++ {
		b0, ok0 := decodeManchesterByte(halfs[off : off+16])
		b1, ok1 := decodeManchesterByte(halfs[off+16 : off+32])
		if !ok0 || !ok1 || b0 != 0xFF || b1 != 0xFA {
			continue
		}
		var payload [8]byte
		valid := true
		for i := range 8 {
			b, ok := decodeManchesterByte(halfs[off+(i+2)*16 : off+(i+3)*16])
			if !ok {
				valid = false
				break
			}
			payload[i] = b
		}
		if valid {
			if r, ok := parseOregonPayload(payload, freqHz); ok {
				return r, true
			}
		}
	}
	return Reading{}, false
}

func parseOregonPayload(p [8]byte, freqHz uint32) (Reading, bool) {
	var sum uint8
	for i := range 7 {
		sum += (p[i] >> 4) + (p[i] & 0x0F)
	}
	if sum != p[7] {
		return Reading{}, false
	}
	tTens, ok1 := bcdDigit(p[4] >> 4)
	tOnes, ok2 := bcdDigit(p[4] & 0x0F)
	tTenths, ok3 := bcdDigit(p[5] >> 4)
	hTens, ok4 := bcdDigit(p[6] >> 4)
	hOnes, ok5 := bcdDigit(p[6] & 0x0F)
	if !ok1 || !ok2 || !ok3 || !ok4 || !ok5 {
		return Reading{}, false
	}
	tempC := float64(tTens*10+tOnes) + float64(tTenths)/10.0
	if p[5]&0x0F != 0 {
		tempC = -tempC
	}
	ch := int(p[2] >> 4)
	if ch < 1 || ch > 3 {
		return Reading{}, false
	}
	id := fmt.Sprintf("%02X%02X-%02X", p[0], p[1], p[3])
	return Reading{
		Kind:        KindWeather,
		Protocol:    "Oregon",
		SensorID:    id,
		FreqHz:      freqHz,
		Channel:     ch,
		TempC:       round1(tempC),
		HasHumidity: true,
		HumidityPct: hTens*10 + hOnes,
		BatteryOK:   (p[2] & 0x04) == 0,
	}, true
}

func bcdDigit(n uint8) (int, bool) {
	if n > 9 {
		return 0, false
	}
	return int(n), true
}

type laCrosseDecoder struct{}

func (laCrosseDecoder) Name() string { return "LaCrosse" }

func (laCrosseDecoder) Decode(pulses []int32, freqHz uint32) (Reading, bool) {
	const need = 8 + 40*2
	if len(pulses) < need {
		return Reading{}, false
	}
	for i := 0; i+need <= len(pulses); i++ {
		if !matchLaCrosseSync(pulses[i : i+8]) {
			continue
		}
		raw, ok := decodePWMBytes(pulses[i+8:], 5, laCrosseShort)
		if !ok || crc8(raw[:4], laCrosseCRC8, 0x00) != raw[4] {
			continue
		}
		ch := int((raw[1]>>4)&0x03) + 1
		batOK := (raw[1] & 0x80) == 0
		rawTemp := int(raw[1]&0x0F)<<8 | int(raw[2])
		tempC := round1(float64(rawTemp-500) / 10.0)
		hum := int(raw[3])
		if hum > 100 {
			continue
		}
		return Reading{
			Kind:        KindWeather,
			Protocol:    "LaCrosse",
			SensorID:    fmt.Sprintf("%02X", raw[0]),
			FreqHz:      freqHz,
			Channel:     ch,
			TempC:       tempC,
			HasHumidity: hum > 0,
			HumidityPct: hum,
			BatteryOK:   batOK,
		}, true
	}
	return Reading{}, false
}

func matchLaCrosseSync(win []int32) bool {
	for j := range 4 {
		if !withinTol(win[2*j], laCrosseSync, 0.25) || !withinTol(-win[2*j+1], laCrosseSync, 0.25) {
			return false
		}
	}
	return true
}

type acuriteDecoder struct{}

func (acuriteDecoder) Name() string { return "Acurite" }

func (acuriteDecoder) Decode(pulses []int32, freqHz uint32) (Reading, bool) {
	const need = 1 + 40*2
	if len(pulses) < need {
		return Reading{}, false
	}
	for i := 0; i+need <= len(pulses); i++ {
		if pulses[i] >= -2000 || pulses[i+1] <= 0 {
			continue
		}
		raw, ok := decodePWMBytes(pulses[i+1:], 5, acuriteShortTE)
		if !ok {
			continue
		}
		if (raw[0]>>4) != 0xA || uint8(raw[0]+raw[1]+raw[2]+raw[3]) != raw[4] {
			continue
		}
		ch := int((raw[2]>>6)&0x03) + 1
		batOK := (raw[2] & 0x20) == 0
		tRaw := int(raw[2]&0x0F)<<8 | int(raw[3])
		if raw[2]&0x10 != 0 {
			tRaw = -tRaw
		}
		return Reading{
			Kind:      KindWeather,
			Protocol:  "Acurite",
			SensorID:  fmt.Sprintf("%02X%02X", raw[0]&0x0F, raw[1]),
			FreqHz:    freqHz,
			Channel:   ch,
			TempC:     round1(float64(tRaw) / 10.0),
			BatteryOK: batOK,
		}, true
	}
	return Reading{}, false
}

func decodePWMBytes(frame []int32, nBytes int, shortTE int32) ([]byte, bool) {
	if len(frame) < nBytes*16 {
		return nil, false
	}
	out := make([]byte, nBytes)
	for b := range nBytes * 8 {
		hi, lo := frame[2*b], -frame[2*b+1]
		if hi <= 0 || lo <= 0 {
			return nil, false
		}
		var bit byte
		if withinTol(hi, shortTE*2, 0.30) && withinTol(lo, shortTE, 0.35) {
			bit = 1
		} else if withinTol(hi, shortTE, 0.35) && withinTol(lo, shortTE*2, 0.30) {
			bit = 0
		} else {
			return nil, false
		}
		out[b/8] = (out[b/8] << 1) | bit
	}
	return out, true
}

func encodePWMBytes(data []byte, shortTE int32) []int32 {
	out := make([]int32, 0, len(data)*16)
	for _, b := range data {
		for i := 7; i >= 0; i-- {
			if (b>>uint(i))&1 == 1 {
				out = append(out, shortTE*2, -shortTE)
			} else {
				out = append(out, shortTE, -shortTE*2)
			}
		}
	}
	return out
}

// EncodeOregonWeather builds a valid Oregon Scientific Manchester pulse train.
func EncodeOregonWeather(sensorType uint16, ch int, rollingID uint8, tempC float64, hum int, batOK bool) []int32 {
	var p [8]byte
	p[0] = uint8(sensorType >> 8)
	p[1] = uint8(sensorType & 0xFF)
	p[2] = uint8(max(1, min(3, ch)) << 4)
	if !batOK {
		p[2] |= 0x04
	}
	p[3] = rollingID
	neg := tempC < 0
	t10 := int(round1(absFloat(tempC)) * 10)
	p[4] = uint8(((t10/100)%10)<<4 | ((t10 / 10) % 10))
	p[5] = uint8((t10 % 10) << 4)
	if neg {
		p[5] |= 0x08
	}
	hum = max(0, min(99, hum))
	p[6] = uint8((hum/10)<<4 | (hum % 10))
	var sum uint8
	for i := range 7 {
		sum += (p[i] >> 4) + (p[i] & 0x0F)
	}
	p[7] = sum
	full := append([]byte{0xFF, 0xFA}, p[:]...)
	return encodeManchesterPulses(full, oregonHalfTE)
}

// EncodeLaCrosseWeather builds a valid LaCrosse TX141 OOK PWM pulse train.
func EncodeLaCrosseWeather(id uint8, ch int, tempC float64, hum int, batOK bool) []int32 {
	rawTemp := max(0, min(4095, int(round1(tempC)*10)+500))
	flags := uint8(((max(1, min(4, ch)) - 1) & 0x03) << 4)
	if !batOK {
		flags |= 0x80
	}
	raw := []byte{id, flags | uint8((rawTemp>>8)&0x0F), uint8(rawTemp & 0xFF), uint8(max(0, min(100, hum))), 0}
	raw[4] = crc8(raw[:4], laCrosseCRC8, 0x00)
	out := []int32{-10000, laCrosseSync, -laCrosseSync, laCrosseSync, -laCrosseSync, laCrosseSync, -laCrosseSync, laCrosseSync, -laCrosseSync}
	out = append(out, encodePWMBytes(raw, laCrosseShort)...)
	return append(out, -10000)
}

// EncodeAcuriteWeather builds a valid Acurite OOK PWM pulse train.
func EncodeAcuriteWeather(id uint16, ch int, tempC float64, batOK bool) []int32 {
	neg := tempC < 0
	t10 := min(4095, int(round1(absFloat(tempC))*10))
	b2 := uint8(((max(1, min(4, ch)) - 1) & 0x03) << 6)
	if !batOK {
		b2 |= 0x20
	}
	if neg {
		b2 |= 0x10
	}
	b2 |= uint8((t10 >> 8) & 0x0F)
	raw := []byte{0xA0 | uint8((id>>8)&0x0F), uint8(id & 0xFF), b2, uint8(t10 & 0xFF), 0}
	raw[4] = uint8(raw[0] + raw[1] + raw[2] + raw[3])
	out := append([]int32{-4000}, encodePWMBytes(raw, acuriteShortTE)...)
	return append(out, -4000)
}

func absFloat(v float64) float64 {
	if v < 0 {
		return -v
	}
	return v
}
