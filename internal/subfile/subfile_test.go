package subfile

import (
	"strings"
	"testing"
)

func TestParseAndFormat(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
		wantHz  uint32
		wantRaw int
	}{
		{
			name: "valid RAW file",
			input: `Filetype: Flipper SubGhz RAW File
Version: 1
Frequency: 433920000
Preset: FuriHalSubGhzPresetOok650Async
Protocol: RAW
RAW_Data: 350 -1050 1050 -350 -10000
`,
			wantHz:  433_920_000,
			wantRaw: 5,
		},
		{
			name: "valid Key file",
			input: `Filetype: Flipper SubGhz Key File
Version: 1
Frequency: 868350000
Preset: FuriHalSubGhzPresetOok270Async
Protocol: Princeton
Bit: 24
Key: 00 00 00 00 00 55 AA 01
TE: 350
`,
			wantHz: 868_350_000,
		},
		{
			name:    "missing filetype",
			input:   "Frequency: 433920000\n",
			wantErr: true,
		},
		{
			name:    "bad frequency",
			input:   "Filetype: Flipper SubGhz RAW File\nFrequency: notanumber\n",
			wantErr: true,
		},
		{
			name:    "bad raw data",
			input:   "Filetype: Flipper SubGhz RAW File\nFrequency: 433920000\nRAW_Data: 100 abc\n",
			wantErr: true,
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			sf, err := Parse(tc.input)
			if (err != nil) != tc.wantErr {
				t.Fatalf("Parse err=%v wantErr=%v", err, tc.wantErr)
			}
			if tc.wantErr {
				return
			}
			if sf.FreqHz != tc.wantHz {
				t.Fatalf("FreqHz=%d want %d", sf.FreqHz, tc.wantHz)
			}
			if len(sf.RawData) != tc.wantRaw {
				t.Fatalf("len(RawData)=%d want %d", len(sf.RawData), tc.wantRaw)
			}
			formatted := sf.Format()
			if !strings.Contains(formatted, "Frequency:") {
				t.Fatalf("Format missing Frequency header: %s", formatted)
			}
			sf2, err := Parse(formatted)
			if err != nil || sf2.FreqHz != sf.FreqHz {
				t.Fatalf("round-trip failed: %v", err)
			}
		})
	}
}

func TestDecodeOOK(t *testing.T) {
	// Build a 24-bit Princeton frame for 0xA55A01 (TE=350 µs).
	const val = uint64(0xA55A01)
	pulses := []int32{-10500}
	for b := 23; b >= 0; b-- {
		if (val>>uint(b))&1 == 1 {
			pulses = append(pulses, 1050, -350)
		} else {
			pulses = append(pulses, 350, -1050)
		}
	}
	pulses = append(pulses, 350, -10500)

	got, ok := DecodeOOK(pulses)
	if !ok {
		t.Fatal("expected Princeton decode to succeed")
	}
	if got.Protocol != "Princeton" || got.Bits != 24 || !strings.HasSuffix(got.KeyHex, "A5 5A 01") {
		t.Fatalf("unexpected decoded key: %+v", got)
	}

	// Noise / short input should not decode.
	if _, ok := DecodeOOK([]int32{100, -100, 200}); ok {
		t.Fatal("expected short noise to fail decoding")
	}
}

func TestDecodeOOKNoisyRecovery(t *testing.T) {
	// Build a 24-bit Princeton frame (0xA55A01, TE=350us) corrupted with severe jitter
	// (breaking the strict 2.0..4.5 ratio) and 25us EMI dropout glitches.
	const val = uint64(0xA55A01)
	noisy := []int32{-10500}
	for b := 23; b >= 0; b-- {
		bit := (val >> uint(b)) & 1
		if b == 12 {
			// Inject a 25us dropout glitch splitting a pulse + skewed ratio that fails strict pass.
			if bit == 1 {
				noisy = append(noisy, 500, -25, 525, -380)
			} else {
				noisy = append(noisy, 460, -200, 25, -620)
			}
			continue
		}
		if bit == 1 {
			noisy = append(noisy, 920, -470) // ratio 1.95 < 2.0 (fails strict!)
		} else {
			noisy = append(noisy, 440, -860) // ratio 0.51 > 0.50 (fails strict!)
		}
	}
	noisy = append(noisy, 350, -10500)

	if _, ok := decodeOOKStrict(noisy); ok {
		t.Fatal("expected strict decoder to fail on jittery/glitched frame")
	}
	got, ok := DecodeOOK(noisy)
	if !ok {
		t.Fatal("expected K-Means denoised fallback to recover Princeton frame")
	}
	if !got.Denoised {
		t.Fatal("expected Denoised=true for recovered frame")
	}
	if got.Protocol != "Princeton" || got.Bits != 24 || !strings.HasSuffix(got.KeyHex, "A5 5A 01") {
		t.Fatalf("unexpected recovered key: %+v", got)
	}
}

func FuzzParseSubFile(f *testing.F) {
	f.Add("Filetype: Flipper SubGhz RAW File\nVersion: 1\nFrequency: 433920000\nRAW_Data: 300 -900\n")
	f.Add("Filetype: x\nFrequency: 0\n")
	f.Fuzz(func(t *testing.T, data string) {
		sf, err := Parse(data)
		if err == nil {
			_ = sf.Format()
		}
	})
}
