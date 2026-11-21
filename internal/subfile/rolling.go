package subfile

import (
	"fmt"
	"math"
)

const (
	rollingRingCap        = 6
	rollingMaxFreqDeltaHz = 250_000
	rollingMinBits        = 24
	rollingMinHamming     = 3
	rollingMinStaticRatio = 0.25
	rollingTETolerance    = 0.15
	rollingWarningText    = "Rolling code detected: replaying may fail or desynchronize the receiver"
)

// RollingKind classifies how a rolling-code transmitter was identified.
type RollingKind string

const (
	RollingNone    RollingKind = "none"
	RollingKeeLoq  RollingKind = "keeloq"
	RollingSomfy   RollingKind = "somfy_telis"
	RollingGeneric RollingKind = "generic_heuristic"
)

// RollingReport summarizes rolling-code analysis across 1..N consecutive captures.
type RollingReport struct {
	IsRolling     bool        `json:"is_rolling"`
	Kind          RollingKind `json:"kind"`
	Protocol      string      `json:"protocol,omitempty"`
	SerialHex     string      `json:"serial_hex,omitempty"`
	Button        uint8       `json:"button,omitempty"`
	EncryptedHex  string      `json:"encrypted_hex,omitempty"`
	Counter       uint16      `json:"counter,omitempty"`
	FramesSeen    int         `json:"frames_seen"`
	HammingDeltas []int       `json:"hamming_deltas,omitempty"`
	StaticBits    int         `json:"static_bits,omitempty"`
	ChangingBits  int         `json:"changing_bits,omitempty"`
	Confidence    float64     `json:"confidence"`
	ReplayWarning string      `json:"replay_warning,omitempty"`
}

type rollingSample struct {
	freqHz uint32
	te     int
	bits   []uint8
}

// RollingDetector tracks recent captures to detect consecutive hopping payloads.
type RollingDetector struct {
	ring   [rollingRingCap]rollingSample
	count  int
	lastHz uint32
}

// NewRollingDetector creates an empty rolling-code detector with a 6-frame ring buffer.
func NewRollingDetector() *RollingDetector {
	return &RollingDetector{}
}

// Reset clears all observed frames.
func (d *RollingDetector) Reset() {
	d.count = 0
	d.lastHz = 0
}

// Observe ingests one capture on freqHz and returns the updated rolling-code classification.
func (d *RollingDetector) Observe(freqHz uint32, pulses []int32) RollingReport {
	if d.lastHz != 0 && freqDeltaHz(d.lastHz, freqHz) > rollingMaxFreqDeltaHz {
		d.Reset()
	}
	d.lastHz = freqHz

	if k, ok := DecodeOOK(pulses); ok && k.IsRolling {
		return reportFromDecodedRolling(k, d.count+1)
	}
	bits, te, ok := extractGenericBitstream(pulses)
	if !ok {
		return RollingReport{Kind: RollingNone, FramesSeen: d.count}
	}
	d.pushSample(rollingSample{freqHz: freqHz, te: te, bits: bits})
	return d.evaluateRing()
}

// AnalyzeRollingCode analyzes a batch of 1..N captured frames on freqHz for rolling-code behavior.
func AnalyzeRollingCode(freqHz uint32, frames [][]int32) RollingReport {
	if len(frames) == 0 {
		return RollingReport{Kind: RollingNone}
	}
	det := NewRollingDetector()
	var best RollingReport
	for _, f := range frames {
		rep := det.Observe(freqHz, f)
		if rep.IsRolling && rep.Confidence >= best.Confidence {
			best = rep
		} else if !best.IsRolling {
			best = rep
		}
	}
	best.FramesSeen = len(frames)
	return best
}

func (d *RollingDetector) pushSample(s rollingSample) {
	if d.count < rollingRingCap {
		d.ring[d.count] = s
		d.count++
		return
	}
	copy(d.ring[:], d.ring[1:])
	d.ring[rollingRingCap-1] = s
}

func (d *RollingDetector) evaluateRing() RollingReport {
	if d.count < 2 {
		return RollingReport{Kind: RollingNone, FramesSeen: d.count}
	}
	cur := d.ring[d.count-1]
	var matched []rollingSample
	for i := range d.count {
		s := d.ring[i]
		if len(s.bits) == len(cur.bits) && teMatches(s.te, cur.te) {
			matched = append(matched, s)
		}
	}
	if len(matched) < 2 {
		return RollingReport{Kind: RollingNone, FramesSeen: d.count}
	}
	return compareMatchedSamples(matched, d.count)
}

func compareMatchedSamples(matched []rollingSample, totalSeen int) RollingReport {
	bitLen := len(matched[0].bits)
	deltas := make([]int, 0, len(matched)-1)
	maxDelta := 0
	for i := 1; i < len(matched); i++ {
		hd := hammingDistance(matched[i-1].bits, matched[i].bits)
		deltas = append(deltas, hd)
		maxDelta = max(maxDelta, hd)
	}
	staticBits := countStaticPositions(matched, bitLen)
	changingBits := bitLen - staticBits
	staticRatio := float64(staticBits) / float64(bitLen)

	if maxDelta < rollingMinHamming || staticRatio < rollingMinStaticRatio {
		return RollingReport{
			Kind:          RollingNone,
			FramesSeen:    totalSeen,
			HammingDeltas: deltas,
			StaticBits:    staticBits,
			ChangingBits:  changingBits,
		}
	}
	conf := min(0.98, 0.80+0.02*float64(min(maxDelta, 9)))
	return RollingReport{
		IsRolling:     true,
		Kind:          RollingGeneric,
		Protocol:      fmt.Sprintf("Rolling-%db", bitLen),
		FramesSeen:    totalSeen,
		HammingDeltas: deltas,
		StaticBits:    staticBits,
		ChangingBits:  changingBits,
		Confidence:    math.Round(conf*100) / 100,
		ReplayWarning: rollingWarningText,
	}
}

func reportFromDecodedRolling(k DecodedKey, seen int) RollingReport {
	kind := RollingKeeLoq
	if k.Protocol == "Somfy Telis" {
		kind = RollingSomfy
	}
	return RollingReport{
		IsRolling:     true,
		Kind:          kind,
		Protocol:      k.Protocol,
		SerialHex:     k.SerialHex,
		Button:        k.Button,
		EncryptedHex:  k.EncryptedHex,
		Counter:       k.Counter,
		FramesSeen:    seen,
		Confidence:    0.99,
		ReplayWarning: rollingWarningText,
	}
}

func extractGenericBitstream(pulses []int32) ([]uint8, int, bool) {
	if len(pulses) < rollingMinBits*2 {
		return nil, 0, false
	}
	var bestBits []uint8
	bestTE := 0
	for i := 0; i+rollingMinBits*2 <= len(pulses); i++ {
		if pulses[i] >= -2000 || pulses[i+1] <= 0 {
			continue
		}
		bits, te := readPWMBurst(pulses[i+1:])
		if len(bits) >= rollingMinBits && len(bits) > len(bestBits) {
			bestBits = bits
			bestTE = te
		}
	}
	if len(bestBits) < rollingMinBits {
		return nil, 0, false
	}
	return bestBits, bestTE, true
}

func readPWMBurst(frame []int32) ([]uint8, int) {
	var bits []uint8
	var teSum int64
	for j := 0; j+1 < len(frame) && len(bits) < 128; j += 2 {
		hi, lo := frame[j], -frame[j+1]
		if hi < 80 || lo < 80 || hi > 3000 || lo > 3000 {
			break
		}
		ratio := float64(hi) / float64(lo)
		if ratio >= 1.45 && ratio <= 4.5 {
			bits = append(bits, 1)
			teSum += int64(lo)
		} else if ratio <= 0.69 && ratio >= 0.2 {
			bits = append(bits, 0)
			teSum += int64(hi)
		} else {
			break
		}
	}
	if len(bits) == 0 {
		return nil, 0
	}
	return bits, int(math.Round(float64(teSum) / float64(len(bits))))
}

func hammingDistance(a, b []uint8) int {
	n := min(len(a), len(b))
	d := 0
	for i := range n {
		if a[i] != b[i] {
			d++
		}
	}
	return d
}

func countStaticPositions(samples []rollingSample, bitLen int) int {
	static := 0
	for b := range bitLen {
		v := samples[0].bits[b]
		same := true
		for i := 1; i < len(samples); i++ {
			if samples[i].bits[b] != v {
				same = false
				break
			}
		}
		if same {
			static++
		}
	}
	return static
}

func teMatches(a, b int) bool {
	if a <= 0 || b <= 0 {
		return false
	}
	diff := math.Abs(float64(a - b))
	return diff <= float64(max(a, b))*rollingTETolerance
}

func freqDeltaHz(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}
