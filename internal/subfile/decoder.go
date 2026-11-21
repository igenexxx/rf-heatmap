package subfile

import (
	"fmt"

	"rf-heatmap/internal/rfml"
)

const minOOKPulses = 20

// DecodedKey represents a decoded OOK remote frame (fixed or rolling).
type DecodedKey struct {
	Protocol     string `json:"protocol"`
	Bits         int    `json:"bits"`
	KeyHex       string `json:"key_hex"`
	TE           int    `json:"te_us"`
	Denoised     bool   `json:"denoised,omitempty"`
	IsRolling    bool   `json:"is_rolling,omitempty"`
	SerialHex    string `json:"serial_hex,omitempty"`
	EncryptedHex string `json:"encrypted_hex,omitempty"`
	Counter      uint16 `json:"counter,omitempty"`
	Button       uint8  `json:"button,omitempty"`
	TriState     string `json:"tristate,omitempty"`
	AddressHex   string `json:"address_hex,omitempty"`
}

// OOKDecoder is a Strategy interface for single-frame OOK protocol decoders.
type OOKDecoder interface {
	Name() string
	Decode(pulses []int32) (DecodedKey, bool)
}

var defaultOOKDecoders = []OOKDecoder{
	somfyTelisDecoder{},
	keeLoqDecoder{},
	holtekDecoder{},
	niceFLODecoder{},
	princetonDecoder{},
	cameDecoder{},
	linearDecoder{},
}

// DecodeOOK attempts to decode common OOK PWM/Manchester remote protocols from signed us pulses.
// Positive values = high (carrier on), negative values = low (carrier off).
// If strict decoding fails due to jitter or EMI glitches, it falls back to 1D K-Means quantization.
func DecodeOOK(pulses []int32) (DecodedKey, bool) {
	if len(pulses) < minOOKPulses {
		return DecodedKey{}, false
	}
	if k, ok := decodeOOKStrict(pulses); ok {
		return k, true
	}
	return decodeOOKWithDenoise(pulses)
}

func decodeOOKStrict(pulses []int32) (DecodedKey, bool) {
	for _, dec := range defaultOOKDecoders {
		if k, ok := dec.Decode(pulses); ok {
			return k, true
		}
	}
	return DecodedKey{}, false
}

func decodeOOKWithDenoise(pulses []int32) (DecodedKey, bool) {
	cl := rfml.ClusterTimings(pulses, rfml.DefaultMinGlitchUs)
	if !cl.Valid || cl.LongShortRatio < 1.5 || cl.LongShortRatio > 5.0 {
		return DecodedKey{}, false
	}
	q := rfml.QuantizePulses(pulses, cl, rfml.DefaultMinGlitchUs)
	if len(q) < minOOKPulses {
		return DecodedKey{}, false
	}
	if k, ok := decodeOOKStrict(q); ok {
		k.Denoised = true
		return k, true
	}
	return DecodedKey{}, false
}

func formatKeyHex(v uint64, bytes int) string {
	out := make([]byte, 0, bytes*3)
	for i := bytes - 1; i >= 0; i-- {
		if len(out) > 0 {
			out = append(out, ' ')
		}
		b := uint8((v >> (uint(i) * 8)) & 0xFF)
		out = append(out, fmt.Sprintf("%02X", b)...)
	}
	return string(out)
}
