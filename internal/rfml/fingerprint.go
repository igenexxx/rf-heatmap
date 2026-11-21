package rfml

import (
	"cmp"
	"math"
	"slices"
	"strings"
)

const (
	// EmbeddingDim is the dimensionality of an RF transmitter fingerprint.
	EmbeddingDim = 8

	// MaxFreqDeltaHz is the carrier-frequency gate (±2.5 MHz) preventing cross-band false matches.
	MaxFreqDeltaHz int64 = 2_500_000
)

// Embedding is an L2-normalized 8D RF transmitter fingerprint:
//
//	0: BandNorm      (315/433/868/915)
//	1: ModType       (0.25=OOK/AM, 0.85=2-FSK/FM)
//	2: ShortUsNorm   (TE/1500)
//	3: LongUsNorm    (LongUs/3000)
//	4: RatioNorm     (LongShortRatio/5)
//	5: LogSyncUs     (log10(SyncUs+1)/5)
//	6: FrameLenNorm  (pulsesPerFrame/180)
//	7: DutyCycle     (0..1)
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

// ComputeEmbeddingFromRaw builds an L2-normalized 8D RF fingerprint from signed µs RAW_Data.
func ComputeEmbeddingFromRaw(raw []int32, freqHz uint32, preset string) Embedding {
	feat, _ := ExtractFeatures(raw, freqHz, 25)
	var v Embedding
	v[0] = feat[0]
	v[1] = presetModType(preset)
	v[2] = feat[2]
	v[3] = feat[3]
	v[4] = feat[4]
	v[5] = feat[5]
	v[6] = feat[8]
	v[7] = feat[7]
	return normalizeEmbedding(v)
}

// ComputeEmbeddingFromKey synthesizes an 8D RF fingerprint from a Flipper Key .sub file
// so live RAW captures can match saved decoded Key files without RAW_Data.
func ComputeEmbeddingFromKey(freqHz uint32, preset, protocol string, bits, te int) Embedding {
	if te <= 0 {
		te = 350
	}
	if bits <= 0 {
		bits = 24
	}
	ratio := 3.0
	syncFactor := 30.0
	if strings.EqualFold(protocol, "CAME") {
		ratio = 2.0
		syncFactor = 36.0
	} else if strings.EqualFold(protocol, "KeeLoq") {
		ratio = 2.0
		syncFactor = 40.0
	}
	shortUs := float64(te)
	longUs := shortUs * ratio
	syncUs := shortUs * syncFactor
	frameLen := float64(bits * 2)

	var v Embedding
	v[0] = float32(normalizeBand(freqHz))
	v[1] = presetModType(preset)
	v[2] = clamp32(shortUs / 1500.0)
	v[3] = clamp32(longUs / 3000.0)
	v[4] = clamp32(ratio / 5.0)
	v[5] = clamp32(math.Log10(syncUs+1.0) / 5.0)
	v[6] = clamp32(frameLen / 180.0)
	v[7] = 0.45 // Typical OOK PWM duty cycle
	return normalizeEmbedding(v)
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
