package sensors

import "testing"

func TestTPMSDecoders(t *testing.T) {
	// Schrader 433.92 MHz normal + flat tire + negative temp + low battery.
	tests := []struct {
		name      string
		pulses    []int32
		freqHz    uint32
		wantProto string
		wantID    string
		wantKPa   float64
		wantTempC float64
		wantBatOK bool
	}{
		{
			name:      "Schrader 433 normal",
			pulses:    EncodeSchraderTPMS(0x3A4F12, 235.0, 26.0, 0x00),
			freqHz:    433_920_000,
			wantProto: "Schrader",
			wantID:    "3A4F12",
			wantKPa:   235.0,
			wantTempC: 26.0,
			wantBatOK: true,
		},
		{
			name:      "Schrader flat tire negative temp low bat",
			pulses:    EncodeSchraderTPMS(0x0000FF, 0.0, -20.0, 0x01),
			freqHz:    433_920_000,
			wantProto: "Schrader",
			wantID:    "0000FF",
			wantKPa:   0.0,
			wantTempC: -20.0,
			wantBatOK: false,
		},
		{
			name:      "Toyota 315 high pressure",
			pulses:    EncodeToyotaTPMS(0x8821C409, 450.0, 32.0, 0x00),
			freqHz:    315_000_000,
			wantProto: "Toyota",
			wantID:    "8821C409",
			wantKPa:   450.0,
			wantTempC: 32.0,
			wantBatOK: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			r, ok := Decode(tc.pulses, tc.freqHz, "GFSK99")
			if !ok {
				t.Fatalf("expected decode to succeed")
			}
			if r.Kind != KindTPMS || r.Protocol != tc.wantProto || r.SensorID != tc.wantID ||
				r.PressureKPa != tc.wantKPa || r.TempC != tc.wantTempC || r.BatteryOK != tc.wantBatOK {
				t.Fatalf("got %+v", r)
			}
		})
	}

	// Corrupt 1 pulse in Schrader -> CRC-8 MUST reject.
	bad := EncodeSchraderTPMS(0x3A4F12, 235.0, 26.0, 0x00)
	bad[len(bad)/2] = -bad[len(bad)/2]
	if r, ok := Decode(bad, 433_920_000, "GFSK99"); ok {
		t.Fatalf("expected corrupted TPMS frame to reject, got %+v", r)
	}
}

func TestWeatherDecoders(t *testing.T) {
	// Oregon Scientific negative temp, 0% and 99% RH.
	ore := EncodeOregonWeather(0x1D20, 1, 0x7A, -15.5, 99, false)
	r, ok := Decode(ore, 433_920_000, "AM650")
	if !ok || r.Kind != KindWeather || r.Protocol != "Oregon" || r.TempC != -15.5 || r.HumidityPct != 99 || r.BatteryOK {
		t.Fatalf("Oregon decode got (%+v, %v)", r, ok)
	}

	// LaCrosse TX141.
	lac := EncodeLaCrosseWeather(0x5C, 2, 18.6, 62, true)
	r2, ok := Decode(lac, 433_920_000, "AM650")
	if !ok || r2.Protocol != "LaCrosse" || r2.Channel != 2 || r2.TempC != 18.6 || r2.HumidityPct != 62 || !r2.BatteryOK {
		t.Fatalf("LaCrosse decode got (%+v, %v)", r2, ok)
	}

	// Acurite negative temp.
	acu := EncodeAcuriteWeather(0x123, 3, -4.2, true)
	r3, ok := Decode(acu, 433_920_000, "AM650")
	if !ok || r3.Protocol != "Acurite" || r3.Channel != 3 || r3.TempC != -4.2 || !r3.BatteryOK {
		t.Fatalf("Acurite decode got (%+v, %v)", r3, ok)
	}

	// Invalid BCD digit in Oregon payload MUST reject.
	var badBCD [8]byte
	badBCD[2] = 0x10
	badBCD[4] = 0xAF // invalid BCD
	var sum uint8
	for i := range 7 {
		sum += (badBCD[i] >> 4) + (badBCD[i] & 0x0F)
	}
	badBCD[7] = sum
	if _, ok := parseOregonPayload(badBCD, 433_920_000); ok {
		t.Fatal("expected invalid BCD digit to reject")
	}

	// Unit conversions.
	if got := KPaToPSI(234.0); got < 33.8 || got > 34.1 {
		t.Fatalf("KPaToPSI(234) = %.1f", got)
	}
	if got := CelsiusToFahrenheit(20.0); got != 68.0 {
		t.Fatalf("CelsiusToFahrenheit(20) = %.1f", got)
	}
}

func TestSimFramesRoundTrip(t *testing.T) {
	for i := range uint64(16) {
		pulses, freqHz, preset := SimFrame(i)
		r, ok := Decode(pulses, freqHz, preset)
		if !ok {
			t.Fatalf("SimFrame(%d) failed to decode", i)
		}
		if r.SensorID == "" || r.Protocol == "" {
			t.Fatalf("SimFrame(%d) produced incomplete reading: %+v", i, r)
		}
	}
	// Empty / too large pulses reject cleanly.
	if _, ok := Decode(nil, 433_920_000, "AM650"); ok {
		t.Fatal("expected nil pulses to reject")
	}
	if _, ok := Decode(make([]int32, maxSensorPulses+1), 433_920_000, "AM650"); ok {
		t.Fatal("expected oversized pulses to reject")
	}
}

func FuzzDecodeSensor(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Fuzz(func(t *testing.T, data []byte) {
		pulses := make([]int32, len(data))
		for i, b := range data {
			p := int32(b) * 10
			if i%2 == 1 {
				p = -p
			}
			pulses[i] = p
		}
		_, _ = Decode(pulses, 433_920_000, "GFSK99")
	})
}
