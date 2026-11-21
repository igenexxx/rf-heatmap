package sensors

import (
	"encoding/binary"
	"errors"
	"fmt"
	"math/bits"
	"strings"
)

const (
	// POCSAGSyncWord is the standard 32-bit POCSAG batch synchronization codeword.
	POCSAGSyncWord uint32 = 0x7CD215D8
	// POCSAGIdleWord is the standard 32-bit POCSAG idle codeword.
	POCSAGIdleWord uint32 = 0x7A89C197

	pocsagBCHPoly      uint32 = 0x769 // x^10 + x^9 + x^8 + x^6 + x^5 + x^3 + 1
	pocsagBatchWords          = 16    // 8 frames * 2 codewords
	pocsagMaxWords            = 4096
)

var pocsagBCDTable = [16]byte{
	'0', '1', '2', '3', '4', '5', '6', '7',
	'8', '9', '*', 'U', ' ', '-', ')', '(',
}

// POCSAGMessage is a decoded POCSAG pager message from a 512/1200/2400 bps batch.
type POCSAGMessage struct {
	RIC       uint32 `json:"ric"`
	Function  uint8  `json:"function"`
	BaudRate  int    `json:"baud_rate"`
	Encoding  string `json:"encoding"`
	Text      string `json:"text"`
	Numeric   string `json:"numeric"`
	BCHErrors int    `json:"bch_errors"`
	Valid     bool   `json:"valid"`
}

func pocsagBCHSyndrome(word uint32) uint32 {
	reg := word >> 1 // 31-bit BCH word (bits 30..0)
	for i := 30; i >= 10; i-- {
		if (reg>>uint(i))&1 != 0 {
			reg ^= pocsagBCHPoly << uint(i-10)
		}
	}
	return reg & 0x3FF
}

// EncodePOCSAGWord encodes a 21-bit payload (bits 20..0) into a 32-bit BCH(31,21)+parity codeword.
func EncodePOCSAGWord(data21 uint32) uint32 {
	base := (data21 & 0x1FFFFF) << 11
	rem := pocsagBCHSyndrome(base)
	word := base | (rem << 1)
	if bits.OnesCount32(word)%2 != 0 {
		word |= 1
	}
	return word
}

// VerifyPOCSAGWord checks BCH(31,21) + even parity and corrects up to 1 single-bit error.
func VerifyPOCSAGWord(word uint32) (corrected uint32, errBits int, ok bool) {
	syn := pocsagBCHSyndrome(word)
	parityBad := bits.OnesCount32(word)%2 != 0
	if syn == 0 && !parityBad {
		return word, 0, true
	}
	if syn == 0 && parityBad {
		return word ^ 1, 1, true
	}
	if !parityBad {
		return 0, 2, false // even number of bit errors (>=2) with non-zero syndrome
	}
	for bitPos := uint(1); bitPos < 32; bitPos++ {
		cand := word ^ (1 << bitPos)
		if pocsagBCHSyndrome(cand) == 0 && bits.OnesCount32(cand)%2 == 0 {
			return cand, 1, true
		}
	}
	return 0, 2, false
}

// ValidatePOCSAGBaud checks that baudRate is one of 512, 1200, or 2400 bps (0 defaults to 1200).
func ValidatePOCSAGBaud(baudRate int) (int, error) {
	if baudRate == 0 {
		return 1200, nil
	}
	switch baudRate {
	case 512, 1200, 2400:
		return baudRate, nil
	default:
		return 0, fmt.Errorf("pocsag: unsupported baud rate %d (use 512, 1200, or 2400)", baudRate)
	}
}

// DecodePOCSAGBytes parses big-endian 32-bit POCSAG codewords from raw bytes.
func DecodePOCSAGBytes(data []byte, baudRate int) ([]POCSAGMessage, error) {
	if len(data) < 8 {
		return nil, errors.New("pocsag: input too short (need at least 8 bytes)")
	}
	n := min(len(data)/4, pocsagMaxWords)
	words := make([]uint32, n)
	for i := range n {
		words[i] = binary.BigEndian.Uint32(data[i*4 : i*4+4])
	}
	return DecodePOCSAGWords(words, baudRate)
}

// DecodePOCSAGWords locates POCSAG sync words and decodes all address/message batches.
func DecodePOCSAGWords(words []uint32, baudRate int) ([]POCSAGMessage, error) {
	baud, err := ValidatePOCSAGBaud(baudRate)
	if err != nil {
		return nil, err
	}
	if len(words) < 2 {
		return nil, errors.New("pocsag: need at least 2 codewords")
	}
	if len(words) > pocsagMaxWords {
		words = words[:pocsagMaxWords]
	}

	var msgs []POCSAGMessage
	var cur *pocsagAccumulator
	foundSync := false
	wordInBatch := -1

	flush := func() {
		if cur != nil {
			msgs = append(msgs, cur.finish(baud))
			cur = nil
		}
	}

	for _, raw := range words {
		if raw == POCSAGSyncWord {
			foundSync = true
			wordInBatch = 0
			continue
		}
		if !foundSync {
			continue
		}
		if wordInBatch >= pocsagBatchWords {
			wordInBatch = -1
			continue
		}
		frameIdx := uint32(wordInBatch / 2)
		wordInBatch++
		if raw == POCSAGIdleWord {
			flush()
			continue
		}
		cw, errs, ok := VerifyPOCSAGWord(raw)
		if !ok {
			flush()
			continue
		}
		if cw&0x80000000 == 0 {
			flush()
			addr18 := (cw >> 13) & 0x3FFFF
			fn := uint8((cw >> 11) & 0x03)
			cur = &pocsagAccumulator{
				ric:      (addr18 << 3) | frameIdx,
				function: fn,
				bchErrs:  errs,
			}
			continue
		}
		if cur != nil {
			cur.bchErrs += errs
			cur.appendData20((cw >> 11) & 0xFFFFF)
		}
	}
	flush()
	if !foundSync {
		return nil, errors.New("pocsag: sync word 0x7CD215D8 not found")
	}
	return msgs, nil
}

type pocsagAccumulator struct {
	ric      uint32
	function uint8
	bchErrs  int
	bits     []uint8
}

func (a *pocsagAccumulator) appendData20(v uint32) {
	for i := 19; i >= 0; i-- {
		a.bits = append(a.bits, uint8((v>>uint(i))&1))
	}
}

func (a *pocsagAccumulator) finish(baud int) POCSAGMessage {
	alpha := decodePOCSAGAlpha(a.bits)
	numeric := decodePOCSAGNumeric(a.bits)
	enc := "alpha"
	text := alpha
	if a.function == 0 && len(numeric) > 0 {
		enc = "numeric"
		text = numeric
	}
	return POCSAGMessage{
		RIC:       a.ric,
		Function:  a.function,
		BaudRate:  baud,
		Encoding:  enc,
		Text:      text,
		Numeric:   numeric,
		BCHErrors: a.bchErrs,
		Valid:     true,
	}
}

func decodePOCSAGAlpha(bitStream []uint8) string {
	var b strings.Builder
	for i := 0; i+7 <= len(bitStream); i += 7 {
		var ch byte
		for j := range 7 {
			ch |= bitStream[i+j] << uint(j)
		}
		if ch == 0x00 || ch == 0x04 {
			break
		}
		if ch >= 0x20 && ch <= 0x7E {
			b.WriteByte(ch)
		}
	}
	return strings.TrimRight(b.String(), " ")
}

func decodePOCSAGNumeric(bitStream []uint8) string {
	var b strings.Builder
	for i := 0; i+4 <= len(bitStream); i += 4 {
		var nib uint8
		for j := range 4 {
			nib |= bitStream[i+j] << uint(j)
		}
		b.WriteByte(pocsagBCDTable[nib&0x0F])
	}
	return strings.TrimRight(b.String(), " ")
}

// SimPOCSAGFrame builds a valid BCH-encoded POCSAG batch for simulator and unit testing.
func SimPOCSAGFrame(baudRate int) []byte {
	_ = baudRate
	const ric uint32 = 1234560 // frame 0 (1234560 & 7 == 0)
	const fn uint32 = 3        // alphanumeric function
	msg := "ALERT: PUMP 3 OK"

	words := make([]uint32, 0, 1+pocsagBatchWords)
	words = append(words, POCSAGSyncWord)
	addrPayload := ((ric >> 3) << 2) | (fn & 0x03)
	words = append(words, EncodePOCSAGWord(addrPayload))

	var bitStream []uint8
	for i := range len(msg) {
		ch := msg[i] & 0x7F
		for b := range 7 {
			bitStream = append(bitStream, (ch>>uint(b))&1)
		}
	}
	for len(bitStream)%20 != 0 {
		bitStream = append(bitStream, 0)
	}
	for i := 0; i < len(bitStream); i += 20 {
		var chunk uint32
		for j := range 20 {
			chunk = (chunk << 1) | uint32(bitStream[i+j])
		}
		words = append(words, EncodePOCSAGWord(0x100000|chunk))
	}
	for len(words) < 1+pocsagBatchWords {
		words = append(words, POCSAGIdleWord)
	}
	out := make([]byte, len(words)*4)
	for i, w := range words {
		binary.BigEndian.PutUint32(out[i*4:i*4+4], w)
	}
	return out
}
