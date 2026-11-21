package subfile

import (
	"errors"
	"math"
)

const (
	minTrimRepeats       = 1
	maxTrimRepeats       = 10
	defaultInterFrameGap = 10000
	trimSilenceGapUs     = 25000
	trimGlitchUs         = 60
)

var (
	// ErrInvalidTrimRange is returned when start/end trim indices are out of bounds.
	ErrInvalidTrimRange = errors.New("subfile: invalid trim range")
	// ErrInvalidRepeats is returned when repeat count is outside [1..10].
	ErrInvalidRepeats = errors.New("subfile: repeats must be between 1 and 10")
	// ErrRawTooLarge is returned when trimmed+repeated RAW_Data exceeds maxRawPulsesToEncode.
	ErrRawTooLarge = errors.New("subfile: trimmed RAW_Data exceeds max pulses")
)

// TrimBounds identifies the half-open [Start, End) pulse slice of an isolated burst.
type TrimBounds struct {
	Start int `json:"start_idx"`
	End   int `json:"end_idx"`
}

// TrimResult summarizes a trimmed and optionally repeated RAW_Data capture.
type TrimResult struct {
	OriginalCount int        `json:"original_count"`
	TrimmedCount  int        `json:"trimmed_count"`
	Bounds        TrimBounds `json:"bounds"`
	Repeats       int        `json:"repeats"`
	DurationMs    float64    `json:"duration_ms"`
	Pulses        []int32    `json:"pulses"`
}

// SuggestTrimBounds identifies the first and last pulse indices of the strongest clean burst,
// skipping leading/trailing silence gaps (>25000 us) and isolated <60 us EMI spikes.
func SuggestTrimBounds(raw []int32) TrimBounds {
	if len(raw) == 0 {
		return TrimBounds{}
	}
	best := TrimBounds{Start: 0, End: len(raw)}
	bestScore := -1

	i := 0
	for i < len(raw) {
		if isSilenceOrGlitch(raw[i]) {
			i++
			continue
		}
		segStart := i
		validCount := 0
		for i < len(raw) && absInt32(raw[i]) <= trimSilenceGapUs {
			if absInt32(raw[i]) >= trimGlitchUs {
				validCount++
			}
			i++
		}
		segEnd := trimSegmentEdges(raw, segStart, i)
		if segEnd > segStart && validCount > bestScore {
			bestScore = validCount
			best = TrimBounds{Start: segStart, End: segEnd}
		}
	}
	return best
}

func isSilenceOrGlitch(p int32) bool {
	d := absInt32(p)
	return d == 0 || d < trimGlitchUs || d > trimSilenceGapUs
}

func trimSegmentEdges(raw []int32, start, end int) int {
	for end > start && isSilenceOrGlitch(raw[end-1]) {
		end--
	}
	return end
}

// TrimAndRepeatRaw isolates raw[start:end], normalizes sign alternation (+high/-low, no zeros),
// repeats it 1..10 times separated by -interFrameGapUs, and enforces maxRawPulsesToEncode.
func TrimAndRepeatRaw(raw []int32, start, end, repeats int, interFrameGapUs int32) (TrimResult, error) {
	if len(raw) == 0 || start < 0 || end > len(raw) || start >= end {
		return TrimResult{}, ErrInvalidTrimRange
	}
	if repeats < minTrimRepeats || repeats > maxTrimRepeats {
		return TrimResult{}, ErrInvalidRepeats
	}
	if interFrameGapUs <= 0 {
		interFrameGapUs = defaultInterFrameGap
	}
	base := NormalizeSignAlternation(raw[start:end])
	if len(base) == 0 {
		return TrimResult{}, ErrInvalidTrimRange
	}
	repeated, err := buildRepeatedPulses(base, repeats, interFrameGapUs)
	if err != nil {
		return TrimResult{}, err
	}
	return TrimResult{
		OriginalCount: len(raw),
		TrimmedCount:  len(repeated),
		Bounds:        TrimBounds{Start: start, End: end},
		Repeats:       repeats,
		DurationMs:    calcDurationMs(repeated),
		Pulses:        repeated,
	}, nil
}

// TrimRawPulses is an alias for TrimAndRepeatRaw.
func TrimRawPulses(raw []int32, start, end, repeats int, interFrameGapUs int32) (TrimResult, error) {
	return TrimAndRepeatRaw(raw, start, end, repeats, interFrameGapUs)
}

// NormalizeSignAlternation strips 0 us pulses, merges adjacent same-sign pulses, and ensures
// the output starts on a positive (+high) pulse when one is present.
func NormalizeSignAlternation(pulses []int32) []int32 {
	out := make([]int32, 0, len(pulses))
	for _, p := range pulses {
		if p == 0 {
			continue
		}
		if len(out) == 0 {
			out = append(out, p)
			continue
		}
		prev := out[len(out)-1]
		if (prev > 0 && p > 0) || (prev < 0 && p < 0) {
			out[len(out)-1] = satAddInt32(prev, p)
		} else {
			out = append(out, p)
		}
	}
	if len(out) > 1 && out[0] < 0 {
		out = out[1:]
	}
	return out
}

func buildRepeatedPulses(base []int32, repeats int, gapUs int32) ([]int32, error) {
	est := len(base)*repeats + repeats
	if est > maxRawPulsesToEncode {
		return nil, ErrRawTooLarge
	}
	out := make([]int32, 0, est)
	for r := range repeats {
		if r > 0 {
			out = appendSignSafe(out, -gapUs)
		}
		for _, p := range base {
			out = appendSignSafe(out, p)
			if len(out) > maxRawPulsesToEncode {
				return nil, ErrRawTooLarge
			}
		}
	}
	return out, nil
}

func appendSignSafe(out []int32, p int32) []int32 {
	if p == 0 {
		return out
	}
	if len(out) > 0 && ((out[len(out)-1] > 0 && p > 0) || (out[len(out)-1] < 0 && p < 0)) {
		out[len(out)-1] = satAddInt32(out[len(out)-1], p)
		return out
	}
	return append(out, p)
}

func satAddInt32(a, b int32) int32 {
	sum := int64(a) + int64(b)
	if sum > math.MaxInt32 {
		return math.MaxInt32
	}
	if sum < math.MinInt32+1 {
		return math.MinInt32 + 1
	}
	return int32(sum)
}

func calcDurationMs(pulses []int32) float64 {
	var totalUs int64
	for _, p := range pulses {
		totalUs += int64(absInt32(p))
	}
	return math.Round(float64(totalUs)/100.0) / 10.0
}
