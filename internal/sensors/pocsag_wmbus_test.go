package sensors

import (
	"encoding/binary"
	"testing"
)

func TestPOCSAGBCHSingleBitCorrection(t *testing.T) {
	// Standard sync and idle words must pass BCH(31,21)+even parity with 0 errors.
	for _, w := range []uint32{POCSAGSyncWord, POCSAGIdleWord, EncodePOCSAGWord(0x155AA)} {
		got, errs, ok := VerifyPOCSAGWord(w)
		if !ok || errs != 0 || got != w {
			t.Fatalf("VerifyPOCSAGWord(0x%08X) = (0x%08X, %d, %v), want exact pass", w, got, errs, ok)
		}
		// Every single-bit flip (0..31) must be corrected cleanly.
		for bit := uint(0); bit < 32; bit++ {
			corrupted := w ^ (1 << bit)
			fixed, e, ok := VerifyPOCSAGWord(corrupted)
			if !ok || e != 1 || fixed != w {
				t.Fatalf("bit %d flip not corrected: got (0x%08X, %d, %v), want 0x%08X", bit, fixed, e, ok, w)
			}
		}
		// 2-bit flip must be rejected (even parity preserved, non-zero syndrome).
		if _, _, ok := VerifyPOCSAGWord(w ^ 0x00000018); ok {
			t.Fatalf("expected 2-bit corrupted word 0x%08X to fail", w^0x18)
		}
	}
}

func TestPOCSAGDecodeAlphaNumericAndErrors(t *testing.T) {
	for _, baud := range []int{512, 1200, 2400} {
		raw := SimPOCSAGFrame(baud)
		msgs, err := DecodePOCSAGBytes(raw, baud)
		if err != nil {
			t.Fatalf("DecodePOCSAGBytes(baud=%d) failed: %v", baud, err)
		}
		if len(msgs) != 1 {
			t.Fatalf("got %d messages, want 1", len(msgs))
		}
		m := msgs[0]
		if !m.Valid || m.RIC != 1234560 || m.Function != 3 || m.Encoding != "alpha" || m.Text != "ALERT: PUMP 3 OK" {
			t.Fatalf("unexpected POCSAG message: %+v", m)
		}
	}

	// Single-bit error in a message codeword inside a batch should still decode with BCHErrors >= 1.
	raw := SimPOCSAGFrame(1200)
	w := binary.BigEndian.Uint32(raw[8:12]) ^ (1 << 15)
	binary.BigEndian.PutUint32(raw[8:12], w)
	msgs, err := DecodePOCSAGBytes(raw, 1200)
	if err != nil || len(msgs) != 1 || msgs[0].BCHErrors != 1 || msgs[0].Text != "ALERT: PUMP 3 OK" {
		t.Fatalf("1-bit error recovery failed: msgs=%+v err=%v", msgs, err)
	}

	// Numeric pager (function 0): "12345" -> nibbles 1,2,3,4,5 (LSB-first per nibble).
	var numBits []uint8
	for _, nib := range []uint8{1, 2, 3, 4, 5} {
		for b := range 4 {
			numBits = append(numBits, (nib>>uint(b))&1)
		}
	}
	var chunk uint32
	for _, bit := range numBits {
		chunk = (chunk << 1) | uint32(bit)
	}
	numWords := []uint32{
		POCSAGSyncWord,
		EncodePOCSAGWord((100 << 2) | 0), // frame 0, addr18=100 -> RIC=800, fn=0 (numeric)
		EncodePOCSAGWord(0x100000 | chunk),
		POCSAGIdleWord,
	}
	numMsgs, err := DecodePOCSAGWords(numWords, 512)
	if err != nil || len(numMsgs) != 1 || numMsgs[0].Encoding != "numeric" || numMsgs[0].Text != "12345" || numMsgs[0].RIC != 800 {
		t.Fatalf("numeric POCSAG decode failed: msgs=%+v err=%v", numMsgs, err)
	}

	// Adversarial error cases.
	if _, err := DecodePOCSAGBytes([]byte{1, 2, 3}, 1200); err == nil {
		t.Fatal("expected error for short input")
	}
	if _, err := DecodePOCSAGBytes(make([]byte, 32), 1200); err == nil {
		t.Fatal("expected error when sync word missing")
	}
	if _, err := DecodePOCSAGBytes(SimPOCSAGFrame(1200), 9600); err == nil {
		t.Fatal("expected error for unsupported baud rate")
	}
}

func FuzzDecodePOCSAG(f *testing.F) {
	f.Add(SimPOCSAGFrame(1200), 1200)
	f.Add([]byte{}, 512)
	f.Add([]byte{0x7C, 0xD2, 0x15, 0xD8, 0xFF, 0xFF, 0xFF, 0xFF}, 2400)
	f.Fuzz(func(t *testing.T, data []byte, baud int) {
		_, _ = DecodePOCSAGBytes(data, baud)
	})
}

func TestWMBusDecodeAndAdversarialCases(t *testing.T) {
	for _, mode := range []string{"T1", "C1", "S1", ""} {
		raw := SimWMBusFrame(mode)
		fr, err := DecodeWMBus(raw, mode)
		if err != nil {
			t.Fatalf("DecodeWMBus(%q) failed: %v", mode, err)
		}
		if !fr.CRCOK || fr.ManufacturerCode != "KAM" || fr.Serial != "12345678" || fr.DeviceTypeName != "Water" || fr.CIField != 0x7A {
			t.Fatalf("unexpected wM-Bus frame: %+v", fr)
		}
	}

	// 1-bit corruption in header must flip CRCOK to false.
	corrupt := SimWMBusFrame("T1")
	corrupt[8] ^= 0x01 // flip version bit
	fr, err := DecodeWMBus(corrupt, "T1")
	if err != nil {
		t.Fatalf("corrupted CRC frame returned unexpected error: %v", err)
	}
	if fr.CRCOK {
		t.Fatal("expected CRCOK=false on corrupted header")
	}

	// Manufacturer round-trip & invalid inputs.
	for _, code := range []string{"KAM", "TCH", "LUG", "SEN"} {
		id, err := EncodeManufacturer(code)
		if err != nil {
			t.Fatalf("EncodeManufacturer(%q) failed: %v", code, err)
		}
		got, err := DecodeManufacturer(id)
		if err != nil || got != code {
			t.Fatalf("DecodeManufacturer(0x%04X) = (%q, %v), want %q", id, got, err, code)
		}
	}
	if _, err := EncodeManufacturer("AB"); err == nil {
		t.Fatal("expected error for 2-letter manufacturer")
	}
	if _, err := DecodeManufacturer(0x0000); err == nil {
		t.Fatal("expected error for 0x0000 manufacturer ID")
	}

	// Adversarial malformed frames.
	if _, err := DecodeWMBus([]byte{0x05, 0x44}, "T1"); err == nil {
		t.Fatal("expected error for short frame")
	}
	if _, err := DecodeWMBus(SimWMBusFrame("T1"), "X9"); err == nil {
		t.Fatal("expected error for invalid mode")
	}
	badBCD := SimWMBusFrame("T1")
	badBCD[4] = 0xFA
	if _, err := DecodeWMBus(badBCD, "T1"); err == nil {
		t.Fatal("expected error for non-BCD serial nibble")
	}
}

func FuzzDecodeWMBus(f *testing.F) {
	f.Add(SimWMBusFrame("T1"), "T1")
	f.Add([]byte{}, "C1")
	f.Add([]byte{0x11, 0x44, 0x2D, 0x2C, 0x78, 0x56, 0x34, 0x12, 0x02, 0x07, 0x00, 0x00}, "S1")
	f.Fuzz(func(t *testing.T, data []byte, mode string) {
		_, _ = DecodeWMBus(data, mode)
	})
}
