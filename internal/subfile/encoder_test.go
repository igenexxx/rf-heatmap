package subfile

import (
	"errors"
	"testing"
)

func TestEncodePulsesRoundTrip(t *testing.T) {
	tests := []struct {
		name     string
		sf       *SubFile
		wantProt string
		wantBits int
		wantKey  string
	}{
		{
			name: "princeton_24bit",
			sf: &SubFile{
				Protocol: "Princeton",
				Bit:      24,
				Key:      "00 00 00 00 00 A5 5A 01",
				TE:       350,
			},
			wantProt: "Princeton",
			wantBits: 24,
			wantKey:  "00 00 00 00 00 A5 5A 01",
		},
		{
			name: "came_12bit",
			sf: &SubFile{
				Protocol: "CAME",
				Bit:      12,
				Key:      "00 00 00 00 00 00 0A 5F",
				TE:       320,
			},
			wantProt: "CAME",
			wantBits: 12,
			wantKey:  "00 00 00 00 00 00 0A 5F",
		},
		{
			name: "came_24bit",
			sf: &SubFile{
				Protocol: "CAME",
				Bit:      24,
				Key:      "00 00 00 00 00 12 34 AB",
				TE:       300,
			},
			wantProt: "CAME",
			wantBits: 24,
			wantKey:  "00 00 00 00 00 12 34 AB",
		},
		{
			name: "linear_10bit_default_te",
			sf: &SubFile{
				Protocol: "Linear",
				Bit:      10,
				Key:      "00 00 00 00 00 00 02 A5",
			},
			wantProt: "Linear",
			wantBits: 10,
			wantKey:  "00 00 00 00 00 00 02 A5",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			pulses, err := EncodePulses(tc.sf)
			if err != nil {
				t.Fatalf("EncodePulses failed: %v", err)
			}
			// Verify sign-alternation invariant across every adjacent pair.
			for i := 1; i < len(pulses); i++ {
				if (pulses[i] > 0) == (pulses[i-1] > 0) {
					t.Fatalf("sign alternation broken at index %d: %d followed by %d", i, pulses[i-1], pulses[i])
				}
			}
			got, ok := DecodeOOK(pulses)
			if !ok {
				t.Fatalf("DecodeOOK failed to decode encoded %s pulses", tc.wantProt)
			}
			if got.Protocol != tc.wantProt || got.Bits != tc.wantBits || got.KeyHex != tc.wantKey {
				t.Fatalf("round-trip got (%s, %d, %q), want (%s, %d, %q)",
					got.Protocol, got.Bits, got.KeyHex, tc.wantProt, tc.wantBits, tc.wantKey)
			}
		})
	}
}

func TestEncodePulsesRawAndErrors(t *testing.T) {
	t.Run("raw_passthrough_defensive_copy", func(t *testing.T) {
		orig := []int32{-1000, 350, 0, -350}
		got, err := EncodePulses(&SubFile{RawData: orig})
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if len(got) != 3 || got[0] != -1000 || got[1] != 350 || got[2] != -350 {
			t.Fatalf("unexpected raw pulses: %v", got)
		}
		got[0] = -9999
		if orig[0] != -1000 {
			t.Fatal("EncodePulses must return a defensive copy of RawData")
		}
	})

	errCases := []struct {
		name    string
		sf      *SubFile
		wantErr error
	}{
		{name: "nil_subfile", sf: nil, wantErr: ErrNilSubFile},
		{name: "empty_subfile", sf: &SubFile{Protocol: "Princeton"}, wantErr: ErrEmptySignal},
		{name: "only_zero_raw", sf: &SubFile{RawData: []int32{0, 0}}, wantErr: ErrEmptySignal},
		{name: "unsupported_protocol", sf: &SubFile{Protocol: "KeeLoq", Key: "01 02"}, wantErr: ErrUnsupportedProtocol},
		{name: "invalid_key_hex", sf: &SubFile{Protocol: "Princeton", Key: "ZZ GG"}},
		{name: "key_too_long", sf: &SubFile{Protocol: "Princeton", Key: "00 11 22 33 44 55 66 77 88"}},
		{name: "invalid_came_bits", sf: &SubFile{Protocol: "CAME", Bit: 16, Key: "00 01", TE: 320}},
		{name: "invalid_came_te", sf: &SubFile{Protocol: "CAME", Bit: 12, Key: "00 01", TE: 900}},
		{name: "invalid_princeton_bits", sf: &SubFile{Protocol: "Princeton", Bit: 80, Key: "00 01", TE: 350}},
		{name: "invalid_princeton_te", sf: &SubFile{Protocol: "Princeton", Bit: 24, Key: "00 01", TE: 10}},
	}
	for _, tc := range errCases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := EncodePulses(tc.sf)
			if err == nil {
				t.Fatalf("expected error for %s, got nil", tc.name)
			}
			if tc.wantErr != nil && !errors.Is(err, tc.wantErr) {
				t.Fatalf("err = %v, want errors.Is(_, %v)", err, tc.wantErr)
			}
		})
	}
}

func FuzzEncodePulses(f *testing.F) {
	f.Add("Princeton", "00 00 00 00 00 A5 5A 01", 24, 350)
	f.Add("CAME", "00 00 00 00 00 00 0A 5F", 12, 320)
	f.Add("Linear", "00 00 00 00 00 00 02 A5", 10, 500)
	f.Add("Unknown", "ZZ", -1, -10)
	f.Fuzz(func(t *testing.T, proto, key string, bits, te int) {
		_, _ = EncodePulses(&SubFile{
			Protocol: proto,
			Key:      key,
			Bit:      bits,
			TE:       te,
		})
	})
}
