package subfile

import (
	"errors"
	"math"
	"testing"
)

func buildRatioPulses(val uint64, bits int, te, mult int32, syncMult int32) []int32 {
	out := []int32{-te * syncMult}
	for b := bits - 1; b >= 0; b-- {
		if (val>>uint(b))&1 == 1 {
			out = append(out, te*mult, -te)
		} else {
			out = append(out, te, -te*mult)
		}
	}
	return append(out, te, -te*syncMult)
}

func buildHoltekHT12EPulses(val uint64, te int32) []int32 {
	out := []int32{-te * 36, te}
	for b := 11; b >= 0; b-- {
		if (val>>uint(b))&1 == 1 {
			out = append(out, -te, te*2)
		} else {
			out = append(out, -te*2, te)
		}
	}
	return append(out, -te*36)
}

func TestExtendedOOKDecoders(t *testing.T) {
	// PT2262 valid tri-state: 00='0', 01='F', 11='1' -> 0x05F301 = 00 00 01 01 11 11 00 11 00 00 00 01
	ptVal := uint64(0x05F301)
	ptPulses := buildRatioPulses(ptVal, 24, 350, 3, 31)
	if k, ok := DecodePT2262(ptPulses); !ok || k.Protocol != "PT2262" || k.TriState != "00FF1101000F" {
		t.Fatalf("DecodePT2262 got (%+v, %v)", k, ok)
	}
	if k, ok := DecodeEV1527(ptPulses); !ok || k.Protocol != "EV1527" || k.AddressHex != "05F30" || k.Button != 0x1 {
		t.Fatalf("DecodeEV1527 got (%+v, %v)", k, ok)
	}
	// Invalid trit (10) must disable TriState and fail DecodePT2262.
	badTriPulses := buildRatioPulses(0xA55A01, 24, 350, 3, 31)
	if _, ok := DecodePT2262(badTriPulses); ok {
		t.Fatal("expected DecodePT2262 to reject frame containing invalid trit 10")
	}

	// Holtek HT6P20B 28-bit (anti-code 0101 = 0x5).
	ht28 := buildRatioPulses((0x123456<<6)|(0x2<<4)|0x5, 28, 450, 2, 23)
	if k, ok := DecodeOOK(ht28); !ok || k.Protocol != "Holtek" || k.Bits != 28 {
		t.Fatalf("Holtek 28-bit got (%+v, %v)", k, ok)
	}
	// Bad anti-code (0x9 != 0x5) must NOT decode as Holtek.
	ht28Bad := buildRatioPulses((0x123456<<6)|(0x2<<4)|0x9, 28, 450, 2, 23)
	if k, ok := DecodeOOK(ht28Bad); ok && k.Protocol == "Holtek" {
		t.Fatalf("expected bad Holtek anti-code to reject, got %+v", k)
	}

	// Holtek HT12E 12-bit inverted PWM.
	ht12 := buildHoltekHT12EPulses(0xA53, 400)
	if k, ok := DecodeOOK(ht12); !ok || k.Protocol != "Holtek" || k.Bits != 12 {
		t.Fatalf("Holtek 12-bit got (%+v, %v)", k, ok)
	}

	// Nice FLO 12-bit & 24-bit (TE=700 us, 1:2/2:1 ratio).
	nice12 := buildRatioPulses(0xABC, 12, 700, 2, 36)
	if k, ok := DecodeOOK(nice12); !ok || k.Protocol != "Nice FLO" || k.Bits != 12 || k.TE < 650 || k.TE > 750 {
		t.Fatalf("Nice FLO 12-bit got (%+v, %v)", k, ok)
	}
	nice24 := buildRatioPulses(0x123456, 24, 700, 2, 36)
	if k, ok := DecodeOOK(nice24); !ok || k.Protocol != "Nice FLO" || k.Bits != 24 {
		t.Fatalf("Nice FLO 24-bit got (%+v, %v)", k, ok)
	}
}

func TestKeeLoqAndSomfyDecoders(t *testing.T) {
	// Valid KeeLoq 66-bit frame.
	kl := EncodeKeeLoqPulses(0xDEADBEEF, 0x0ABC123, 0x4, 400)
	got, ok := DecodeOOK(kl)
	if !ok || got.Protocol != "KeeLoq" || got.Bits != 66 || !got.IsRolling {
		t.Fatalf("KeeLoq decode failed: (%+v, %v)", got, ok)
	}
	if got.EncryptedHex != "DEADBEEF" || got.SerialHex != "0ABC123" || got.Button != 0x4 {
		t.Fatalf("KeeLoq fields mismatch: %+v", got)
	}

	// Truncated preamble (<10 pulses) must reject.
	if _, ok := DecodeOOK(kl[18:]); ok {
		t.Fatal("expected truncated KeeLoq preamble to reject")
	}
	// Truncated payload (65 bits) must reject.
	if _, ok := DecodeOOK(kl[:len(kl)-4]); ok {
		t.Fatal("expected 65-bit truncated KeeLoq payload to reject")
	}

	// Valid Somfy Telis 56-bit Manchester RTS frame.
	somfy := EncodeSomfyTelisPulses(0xA1, 0x2, 0x0142, 0x123456)
	sg, ok := DecodeOOK(somfy)
	if !ok || sg.Protocol != "Somfy Telis" || sg.Bits != 56 || !sg.IsRolling {
		t.Fatalf("Somfy decode failed: (%+v, %v)", sg, ok)
	}
	if sg.Counter != 0x0142 || sg.SerialHex != "123456" || sg.Button != 0x2 {
		t.Fatalf("Somfy fields mismatch: %+v", sg)
	}

	// Corrupted Somfy frame (flip a pulse duration) must fail checksum.
	badSomfy := append([]int32(nil), somfy...)
	badSomfy[10] = -badSomfy[10]
	if k, ok := DecodeOOK(badSomfy); ok && k.Protocol == "Somfy Telis" {
		t.Fatalf("expected corrupted Somfy frame to fail checksum, got %+v", k)
	}
}

func TestRollingCodeDetector(t *testing.T) {
	// Single-frame KeeLoq.
	kl := EncodeKeeLoqPulses(0x11223344, 0x0765432, 0x1, 400)
	rep := AnalyzeRollingCode(433_920_000, [][]int32{kl})
	if !rep.IsRolling || rep.Kind != RollingKeeLoq || rep.SerialHex != "0765432" || rep.ReplayWarning == "" {
		t.Fatalf("KeeLoq report unexpected: %+v", rep)
	}

	// Single-frame Somfy.
	somfy := EncodeSomfyTelisPulses(0xA0, 0x4, 0x0010, 0xABCDEF)
	srep := AnalyzeRollingCode(433_420_000, [][]int32{somfy})
	if !srep.IsRolling || srep.Kind != RollingSomfy || srep.Counter != 0x0010 {
		t.Fatalf("Somfy report unexpected: %+v", srep)
	}

	// Generic heuristic: 48-bit frames sharing 24-bit static serial + 24-bit changing hop.
	f1 := buildRatioPulses((uint64(0x123456)<<24)|0x00AAAA, 48, 350, 3, 30)
	f2 := buildRatioPulses((uint64(0x123456)<<24)|0xFF5555, 48, 350, 3, 30)
	grep := AnalyzeRollingCode(433_920_000, [][]int32{f1, f2})
	if !grep.IsRolling || grep.Kind != RollingGeneric || grep.Confidence < 0.80 || len(grep.HammingDeltas) == 0 {
		t.Fatalf("generic rolling heuristic failed: %+v", grep)
	}

	// Fixed-code identical captures MUST NOT trigger rolling code.
	fixed := buildRatioPulses(0xA55A01, 24, 350, 3, 31)
	frep := AnalyzeRollingCode(433_920_000, [][]int32{fixed, fixed})
	if frep.IsRolling || frep.Kind != RollingNone {
		t.Fatalf("identical fixed captures flagged as rolling: %+v", frep)
	}

	// Frequency mismatch (>250 kHz) resets detector.
	det := NewRollingDetector()
	det.Observe(433_920_000, f1)
	mrep := det.Observe(868_350_000, f2)
	if mrep.IsRolling {
		t.Fatalf("frequency mismatch should reset detector, got %+v", mrep)
	}
}

func TestTrimAndRepeatRaw(t *testing.T) {
	// Leading silence + EMI glitch + clean burst + trailing silence.
	noisy := []int32{-45000, 35, -30000, 350, -1050, 1050, -350, 350, -1050, -50000, 20}
	b := SuggestTrimBounds(noisy)
	if b.Start != 3 || b.End != 9 {
		t.Fatalf("SuggestTrimBounds = %+v, want [3..9)", b)
	}

	// Start on negative pulse, repeat 3x -> must enforce strict +high/-low alternation, no zeros.
	res, err := TrimAndRepeatRaw([]int32{-500, 0, 350, 100, -1050, 1050}, 0, 6, 3, 10000)
	if err != nil {
		t.Fatalf("TrimAndRepeatRaw failed: %v", err)
	}
	if res.Repeats != 3 || len(res.Pulses) == 0 || res.Pulses[0] <= 0 {
		t.Fatalf("unexpected TrimResult: %+v", res)
	}
	assertSignAlternation(t, res.Pulses)

	// Error cases.
	if _, err := TrimRawPulses(noisy, 5, 5, 1, 10000); !errors.Is(err, ErrInvalidTrimRange) {
		t.Fatalf("want ErrInvalidTrimRange, got %v", err)
	}
	if _, err := TrimAndRepeatRaw(noisy, 0, 4, 0, 10000); !errors.Is(err, ErrInvalidRepeats) {
		t.Fatalf("want ErrInvalidRepeats for 0, got %v", err)
	}
	if _, err := TrimAndRepeatRaw(noisy, 0, 4, 11, 10000); !errors.Is(err, ErrInvalidRepeats) {
		t.Fatalf("want ErrInvalidRepeats for 11, got %v", err)
	}
	big := make([]int32, 10000)
	for i := range big {
		if i%2 == 0 {
			big[i] = 350
		} else {
			big[i] = -350
		}
	}
	if _, err := TrimAndRepeatRaw(big, 0, len(big), 10, 10000); !errors.Is(err, ErrRawTooLarge) {
		t.Fatalf("want ErrRawTooLarge, got %v", err)
	}
}

func assertSignAlternation(t *testing.T, pulses []int32) {
	t.Helper()
	for i, p := range pulses {
		if p == 0 {
			t.Fatalf("pulse[%d] == 0", i)
		}
		if i > 0 && ((pulses[i-1] > 0 && p > 0) || (pulses[i-1] < 0 && p < 0)) {
			t.Fatalf("sign alternation broken at %d: %d followed by %d", i, pulses[i-1], p)
		}
	}
}

func FuzzDecodeOOK(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6, 7, 8})
	f.Fuzz(func(t *testing.T, data []byte) {
		pulses := make([]int32, len(data))
		for i, b := range data {
			v := int32(b) * 40
			if i%2 == 1 {
				v = -v
			}
			pulses[i] = v
		}
		_, _ = DecodeOOK(pulses)
	})
}

func FuzzAnalyzeRollingCode(f *testing.F) {
	f.Add([]byte{10, 20, 30, 40}, []byte{10, 25, 35, 45})
	f.Fuzz(func(t *testing.T, a, b []byte) {
		toPulses := func(bs []byte) []int32 {
			out := make([]int32, len(bs))
			for i, v := range bs {
				p := int32(v) * 30
				if i%2 == 1 {
					p = -p
				}
				out[i] = p
			}
			return out
		}
		_ = AnalyzeRollingCode(433_920_000, [][]int32{toPulses(a), toPulses(b)})
	})
}

func FuzzTrimAndRepeatRaw(f *testing.F) {
	f.Add([]byte{1, 2, 3, 4, 5, 6}, 2)
	f.Fuzz(func(t *testing.T, data []byte, repeats int) {
		raw := make([]int32, len(data))
		for i, b := range data {
			switch b % 4 {
			case 0:
				raw[i] = int32(b) * 50
			case 1:
				raw[i] = -int32(b) * 50
			case 2:
				raw[i] = 0
			default:
				raw[i] = math.MinInt32
			}
		}
		res, err := TrimAndRepeatRaw(raw, 0, len(raw), repeats, 10000)
		if err == nil {
			assertSignAlternation(t, res.Pulses)
		}
	})
}
