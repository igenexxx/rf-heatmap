package subfile

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

const (
	defaultPrincetonTE   = 350
	defaultCAMETE        = 320
	defaultLinearTE      = 500
	defaultFrameRepeats  = 4
	maxRawPulsesToEncode = 65536
)

var (
	// ErrNilSubFile is returned when encoding a nil SubFile.
	ErrNilSubFile = errors.New("subfile: nil subfile")
	// ErrEmptySignal is returned when a SubFile has neither RAW_Data nor an encodable Key.
	ErrEmptySignal = errors.New("subfile: empty signal")
	// ErrUnsupportedProtocol is returned when a Key SubFile uses an unsupported OOK protocol.
	ErrUnsupportedProtocol = errors.New("subfile: unsupported protocol")
)

// ParseKeyHex parses a Flipper Zero space-separated or compact hex key string into a uint64.
func ParseKeyHex(key string) (uint64, error) {
	cleaned := strings.ReplaceAll(strings.TrimSpace(key), " ", "")
	if cleaned == "" {
		return 0, fmt.Errorf("subfile: empty key hex")
	}
	if len(cleaned) > 16 {
		return 0, fmt.Errorf("subfile: key hex %q exceeds 64 bits", key)
	}
	val, err := strconv.ParseUint(cleaned, 16, 64)
	if err != nil {
		return 0, fmt.Errorf("subfile: invalid key hex %q: %w", key, err)
	}
	return val, nil
}

// EncodePulses converts a SubFile (RAW or supported OOK Key) into signed microsecond
// pulse durations (+high carrier on, -low carrier off) suitable for CC1101 GDO0 TX.
func EncodePulses(sf *SubFile) ([]int32, error) {
	if sf == nil {
		return nil, ErrNilSubFile
	}
	if len(sf.RawData) > 0 {
		return copyValidatedRaw(sf.RawData)
	}
	if strings.TrimSpace(sf.Key) == "" {
		return nil, ErrEmptySignal
	}
	keyVal, err := ParseKeyHex(sf.Key)
	if err != nil {
		return nil, err
	}
	return encodeKeyProtocol(sf.Protocol, keyVal, sf.Bit, sf.TE)
}

func copyValidatedRaw(raw []int32) ([]int32, error) {
	if len(raw) > maxRawPulsesToEncode {
		return nil, fmt.Errorf("subfile: RAW_Data exceeds max %d pulses", maxRawPulsesToEncode)
	}
	out := make([]int32, 0, len(raw))
	for _, p := range raw {
		if p == 0 {
			continue
		}
		out = append(out, p)
	}
	if len(out) == 0 {
		return nil, ErrEmptySignal
	}
	return out, nil
}

func encodeKeyProtocol(protocol string, key uint64, bits, te int) ([]int32, error) {
	switch strings.ToLower(strings.TrimSpace(protocol)) {
	case "princeton":
		return encodeRatioProtocol(key, defaultIfZero(bits, 24), defaultIfZero(te, defaultPrincetonTE), 31)
	case "linear":
		return encodeRatioProtocol(key, defaultIfZero(bits, 10), defaultIfZero(te, defaultLinearTE), 30)
	case "came":
		return encodeCAMEProtocol(key, defaultIfZero(bits, 12), defaultIfZero(te, defaultCAMETE))
	default:
		return nil, fmt.Errorf("%w: %q", ErrUnsupportedProtocol, protocol)
	}
}

func defaultIfZero(v, def int) int {
	if v <= 0 {
		return def
	}
	return v
}

func encodeRatioProtocol(key uint64, bits, te, syncMult int) ([]int32, error) {
	if bits < 1 || bits > 64 {
		return nil, fmt.Errorf("subfile: invalid bit count %d", bits)
	}
	if te < 50 || te > 5000 {
		return nil, fmt.Errorf("subfile: invalid TE %d", te)
	}
	shortP := int32(te)
	longP := int32(te * 3)
	syncGap := -int32(te * syncMult)

	out := make([]int32, 0, defaultFrameRepeats*(bits*2+2))
	for range defaultFrameRepeats {
		out = append(out, syncGap)
		for i := bits - 1; i >= 0; i-- {
			if (key>>uint(i))&1 == 1 {
				out = append(out, longP, -shortP)
			} else {
				out = append(out, shortP, -longP)
			}
		}
		// Trailing short high pulse before next sync gap preserves sign alternation.
		out = append(out, shortP)
	}
	out = append(out, syncGap)
	return out, nil
}

func encodeCAMEProtocol(key uint64, bits, te int) ([]int32, error) {
	if bits != 12 && bits != 24 {
		return nil, fmt.Errorf("subfile: CAME requires 12 or 24 bits, got %d", bits)
	}
	if te < 150 || te > 600 {
		return nil, fmt.Errorf("subfile: CAME TE %d out of range [150..600]", te)
	}
	shortP := int32(te)
	// CAME uses ~1.85:1 pulse ratio (distinct from Princeton's 3:1 ratio).
	longP := int32((te * 185) / 100)
	syncGap := -int32(te * 36)

	out := make([]int32, 0, defaultFrameRepeats*(bits*2+2))
	for range defaultFrameRepeats {
		out = append(out, syncGap)
		for i := bits - 1; i >= 0; i-- {
			if (key>>uint(i))&1 == 1 {
				out = append(out, longP, -shortP)
			} else {
				out = append(out, shortP, -longP)
			}
		}
		out = append(out, shortP)
	}
	out = append(out, syncGap)
	return out, nil
}
