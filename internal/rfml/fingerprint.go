package rfml

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

const (
	// EmbeddingDim is the dimensionality of a learned PulseNet RF transmitter fingerprint.
	EmbeddingDim = PulseNetEmbedDim

	// MaxFreqDeltaHz is the carrier-frequency gate (±2.5 MHz) preventing cross-band false matches.
	MaxFreqDeltaHz int64 = 2_500_000
)

// Embedding is an L2-normalized 32D learned RF transmitter fingerprint produced by PulseNet.
type Embedding [EmbeddingDim]float32

// FingerprintEntry represents one indexed known transmitter from a saved .sub file.
type FingerprintEntry struct {
	Name      string    `json:"name"`
	Path      string    `json:"path,omitempty"`
	FreqHz    uint32    `json:"freq_hz"`
	Protocol  string    `json:"protocol"`
	Embedding Embedding `json:"embedding"`
}

// MatchResult describes a matched known transmitter and its cosine similarity score.
type MatchResult struct {
	Name        string  `json:"name"`
	Path        string  `json:"path,omitempty"`
	Protocol    string  `json:"protocol"`
	Similarity  float32 `json:"similarity"`
	FreqDeltaHz int64   `json:"freq_delta_hz"`
}

// FingerprintIndex holds precomputed RF embeddings for fast on-device k-NN matching.
type FingerprintIndex struct {
	entries []FingerprintEntry
}

// NewFingerprintIndex creates an index from a slice of FingerprintEntry records.
func NewFingerprintIndex(entries []FingerprintEntry) *FingerprintIndex {
	return &FingerprintIndex{entries: slices.Clone(entries)}
}

// Len returns the number of indexed transmitters.
func (idx *FingerprintIndex) Len() int {
	if idx == nil {
		return 0
	}
	return len(idx.entries)
}

// ComputeEmbeddingFromRaw builds an L2-normalized 32D RF fingerprint from signed µs RAW_Data
// using PulseNet Head B (falling back to handcrafted normalization only if no model is loaded).
func ComputeEmbeddingFromRaw(raw []int32, freqHz uint32, preset string) Embedding {
	if len(raw) == 0 {
		return Embedding{}
	}
	if m := defaultPulseNet(); m != nil {
		ws := pulseWS.Get().(*PulseNetWorkspace)
		res := m.Infer(ws, raw, freqHz, preset, 25)
		pulseWS.Put(ws)
		return res.Embedding
	}
	feat, _ := ExtractFeatures(raw, freqHz, 25)
	var v Embedding
	v[0], v[1], v[2], v[3] = feat[0], presetModType(preset), feat[2], feat[3]
	v[4], v[5], v[6], v[7] = feat[4], feat[5], feat[8], feat[7]
	return normalizeEmbedding(v)
}

// ComputeEmbeddingFromKey synthesizes canonical pulses from a Flipper Key .sub file and runs
// the exact same PulseNet 32D encoder so live RAW captures match Key files in one shared manifold.
func ComputeEmbeddingFromKey(freqHz uint32, preset, protocol string, bits, te int) Embedding {
	synth := synthesizeCanonicalKeyPulses(protocol, bits, te)
	return ComputeEmbeddingFromRaw(synth, freqHz, preset)
}

func synthesizeCanonicalKeyPulses(protocol string, bits, te int) []int32 {
	if te <= 0 {
		te = 350
	}
	if bits <= 0 {
		bits = 24
	}
	t := int32(te)
	n := min(bits, 66)
	p := strings.ToLower(protocol)
	out := make([]int32, 0, min(PulseNetSeqLen, n*4+40))

	switch {
	case strings.Contains(p, "came") || strings.Contains(p, "nice"):
		for range 2 {
			out = append(out, -t*36, t)
			for i := range n {
				if (0xAAAAAA>>uint(i%24))&1 == 1 {
					out = append(out, -2*t, t)
				} else {
					out = append(out, -t, 2*t)
				}
			}
		}
	case strings.Contains(p, "keeloq"):
		for range 11 {
			out = append(out, t, -t)
		}
		out = append(out, t, -10*t)
		for i := range n {
			if (i*7+3)%2 == 0 {
				out = append(out, t, -2*t)
			} else {
				out = append(out, 2*t, -t)
			}
		}
		out = append(out, t, -15*t)
	default: // Princeton / EV1527 / Linear 1:3 PWM
		for range 2 {
			out = append(out, -t*30)
			for i := n - 1; i >= 0; i-- {
				if (0xAAAAAA>>uint(i%24))&1 == 1 {
					out = append(out, 3*t, -t)
				} else {
					out = append(out, t, -3*t)
				}
			}
			out = append(out, t)
		}
	}
	return out
}

// CosineSimilarity returns the cosine similarity in [0, 1] between two L2-normalized embeddings.
func CosineSimilarity(a, b Embedding) float32 {
	var dot, normA, normB float64
	for i := range EmbeddingDim {
		va := float64(a[i])
		vb := float64(b[i])
		if math.IsNaN(va) || math.IsInf(va, 0) || math.IsNaN(vb) || math.IsInf(vb, 0) {
			return 0
		}
		dot += va * vb
		normA += va * va
		normB += vb * vb
	}
	if normA <= 1e-12 || normB <= 1e-12 {
		return 0
	}
	sim := dot / (math.Sqrt(normA) * math.Sqrt(normB))
	if sim < 0 {
		return 0
	}
	if sim > 1 {
		return 1
	}
	return float32(sim)
}

// Match returns the single closest indexed transmitter passing the frequency gate and minSim.
// Zero heap allocations on the hot path.
func (idx *FingerprintIndex) Match(query Embedding, freqHz uint32, minSim float32) (MatchResult, bool) {
	if idx == nil || len(idx.entries) == 0 {
		return MatchResult{}, false
	}
	var best MatchResult
	found := false
	for i := range idx.entries {
		e := &idx.entries[i]
		delta := freqDelta(freqHz, e.FreqHz)
		if freqHz > 0 && e.FreqHz > 0 && delta > MaxFreqDeltaHz {
			continue
		}
		sim := CosineSimilarity(query, e.Embedding)
		if sim >= minSim && (!found || sim > best.Similarity) {
			best = MatchResult{
				Name:        e.Name,
				Path:        e.Path,
				Protocol:    e.Protocol,
				Similarity:  sim,
				FreqDeltaHz: delta,
			}
			found = true
		}
	}
	return best, found
}

// TopK returns up to k closest transmitters sorted by descending cosine similarity.
func (idx *FingerprintIndex) TopK(query Embedding, freqHz uint32, k int, minSim float32) []MatchResult {
	if idx == nil || len(idx.entries) == 0 || k <= 0 {
		return nil
	}
	matches := make([]MatchResult, 0, min(k, len(idx.entries)))
	for i := range idx.entries {
		e := &idx.entries[i]
		delta := freqDelta(freqHz, e.FreqHz)
		if freqHz > 0 && e.FreqHz > 0 && delta > MaxFreqDeltaHz {
			continue
		}
		sim := CosineSimilarity(query, e.Embedding)
		if sim >= minSim {
			matches = append(matches, MatchResult{
				Name:        e.Name,
				Path:        e.Path,
				Protocol:    e.Protocol,
				Similarity:  sim,
				FreqDeltaHz: delta,
			})
		}
	}
	slices.SortFunc(matches, func(a, b MatchResult) int {
		if c := cmp.Compare(b.Similarity, a.Similarity); c != 0 {
			return c
		}
		return cmp.Compare(a.Name, b.Name)
	})
	if len(matches) > k {
		matches = matches[:k]
	}
	return matches
}

func presetModType(preset string) float32 {
	p := strings.ToUpper(preset)
	if strings.Contains(p, "FSK") || strings.HasPrefix(p, "FM") {
		return 0.85
	}
	return 0.25
}

func normalizeEmbedding(v Embedding) Embedding {
	var sumSq float64
	for i := range EmbeddingDim {
		sumSq += float64(v[i]) * float64(v[i])
	}
	if sumSq <= 1e-12 {
		return Embedding{}
	}
	inv := float32(1.0 / math.Sqrt(sumSq))
	for i := range EmbeddingDim {
		v[i] *= inv
	}
	return v
}

func freqDelta(a, b uint32) int64 {
	d := int64(a) - int64(b)
	if d < 0 {
		return -d
	}
	return d
}
