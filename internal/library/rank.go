package library

import (
	"cmp"
	"slices"

	"rf-heatmap/internal/rfml"
)

const freqGateHz uint32 = 2_500_000

// RankedEntry pairs a catalog EntryRef with its 8D RF fingerprint cosine similarity.
type RankedEntry struct {
	Entry      EntryRef
	Similarity float32
}

// RankEntriesByEmbedding ranks disk-backed .sub entries descending by frequency-gated
// 8D RF cosine similarity against (query, queryFreqHz). Unmatched/out-of-band entries
// remain at the tail in their original relative order.
func RankEntriesByEmbedding(entries []EntryRef, query rfml.Embedding, queryFreqHz uint32) ([]EntryRef, float32) {
	if len(entries) <= 1 {
		return entries, 0
	}
	scored := make([]RankedEntry, len(entries))
	var bestSim float32
	for i, ref := range entries {
		sim := scoreEntry(ref, query, queryFreqHz)
		if sim > bestSim {
			bestSim = sim
		}
		scored[i] = RankedEntry{Entry: ref, Similarity: sim}
	}
	slices.SortStableFunc(scored, func(a, b RankedEntry) int {
		return cmp.Compare(b.Similarity, a.Similarity)
	})
	out := make([]EntryRef, len(scored))
	for i, s := range scored {
		out[i] = s.Entry
	}
	return out, bestSim
}

func scoreEntry(ref EntryRef, query rfml.Embedding, queryFreqHz uint32) float32 {
	sf, err := LoadSubFile(ref)
	if err != nil || sf == nil {
		return -1
	}
	if queryFreqHz > 0 && sf.FreqHz > 0 && freqDiffHz(queryFreqHz, sf.FreqHz) > freqGateHz {
		return -1
	}
	var emb rfml.Embedding
	if len(sf.RawData) > 0 {
		emb = rfml.ComputeEmbeddingFromRaw(sf.RawData, sf.FreqHz, sf.Preset)
	} else {
		emb = rfml.ComputeEmbeddingFromKey(sf.FreqHz, sf.Preset, sf.Protocol, sf.Bit, sf.TE)
	}
	return dotEmbedding(query, emb)
}

func freqDiffHz(a, b uint32) uint32 {
	if a > b {
		return a - b
	}
	return b - a
}

func dotEmbedding(a, b rfml.Embedding) float32 {
	var s float32
	for i := range a {
		s += a[i] * b[i]
	}
	return s
}
