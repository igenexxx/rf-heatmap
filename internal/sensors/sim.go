package sensors

// SimFrame generates realistic encoded Sub-GHz TPMS and Weather pulse trains for SIM mode.
// Every generated frame passes through the real sensors.Decode() pipeline end-to-end.
func SimFrame(tick uint64) (pulses []int32, freqHz uint32, preset string) {
	drift := float64(tick%7) * 0.2
	switch tick % 4 {
	case 0:
		// 433.92 MHz EU Schrader TPMS front-left tire (~234 kPa / 33.9 PSI, 26.5 C).
		return EncodeSchraderTPMS(0x3A4F12, 232.5+drift*2.5, 26.0+drift, 0x00), 433_920_000, "GFSK99"
	case 1:
		// 433.92 MHz Oregon Scientific THGN132N CH1 (~22.4 C, 48% RH).
		return EncodeOregonWeather(0x1D20, 1, 0x7A, 22.0+drift, 48+int(tick%3), true), 433_920_000, "AM650"
	case 2:
		// 433.92 MHz LaCrosse TX141 CH2 (~18.6 C, 62% RH).
		return EncodeLaCrosseWeather(0x5C, 2, 18.4+drift, 62-int(tick%4), true), 433_920_000, "AM650"
	default:
		// 315.00 MHz US Toyota TPMS rear-right tire (~220 kPa, low-battery flag).
		return EncodeToyotaTPMS(0x8821C409, 220.0+drift*2.5, 24.0+drift, 0x80), 315_000_000, "GFSK99"
	}
}
