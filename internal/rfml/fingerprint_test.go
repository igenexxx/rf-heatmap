package rfml

import (
	"math"
	"testing"
)

func buildPrincetonRaw(te int32, jitter int32) []int32 {
	pulses := make([]int32, 0, 120)
	for range 2 {
		pulses = append(pulses, -te*30)
		for b := 23; b >= 0; b-- {
			j := jitter
			if b%2 == 0 {
				j = -jitter
			}
			if (0xA55A01>>uint(b))&1 == 1 {
				pulses = append(pulses, te*3+j, -(te + j/2))
			} else {
				pulses = append(pulses, te+j/2, -(te*3 + j))
			}
		}
		pulses = append(pulses, te)
	}
	return pulses
}

func TestFingerprintMatchAndCrossFormat(t *testing.T) {
	gateRaw := buildPrincetonRaw(350, 0)
	entries := []FingerprintEntry{
		{
			Name:      "home_gate_raw.sub",
			FreqHz:    433_920_000,
			Protocol:  "RAW",
			Embedding: ComputeEmbeddingFromRaw(gateRaw, 433_920_000, "AM650"),
		},
		{
			Name:      "home_gate_key.sub",
			FreqHz:    433_920_000,
			Protocol:  "Princeton",
			Embedding: ComputeEmbeddingFromKey(433_920_000, "AM650", "Princeton", 24, 350),
		},
		{
			Name:      "weather_868.sub",
			FreqHz:    868_350_000,
			Protocol:  "RAW",
			Embedding: ComputeEmbeddingFromRaw(gateRaw, 868_350_000, "FM238"),
		},
	}

	idx := NewFingerprintIndex(entries)
	if idx.Len() != 3 {
		t.Fatalf("Len()=%d want 3", idx.Len())
	}

	// Jittered live capture on 433.92 MHz must match home_gate_raw.sub cleanly.
	liveJittered := buildPrincetonRaw(355, 25)
	query := ComputeEmbeddingFromRaw(liveJittered, 433_920_000, "AM650")

	best, ok := idx.Match(query, 433_920_000, 0.90)
	if !ok {
		t.Fatal("expected fingerprint match for jittered 433 MHz capture")
	}
	if best.Name != "home_gate_raw.sub" || best.Similarity < 0.95 {
		t.Fatalf("unexpected best match: %+v", best)
	}

	// Cross-format: live RAW capture must also match saved Key .sub file in TopK.
	top := idx.TopK(query, 433_920_000, 3, 0.90)
	if len(top) != 2 {
		t.Fatalf("expected 2 matches on 433 MHz (868 MHz gated out), got %d: %+v", len(top), top)
	}
	if top[1].Name != "home_gate_key.sub" {
		t.Fatalf("expected second match to be home_gate_key.sub, got %+v", top[1])
	}

	// Frequency gate: querying at 868.35 MHz must NOT match 433 MHz entries.
	q868 := ComputeEmbeddingFromRaw(gateRaw, 868_350_000, "FM238")
	best868, ok := idx.Match(q868, 868_350_000, 0.90)
	if !ok || best868.Name != "weather_868.sub" {
		t.Fatalf("expected weather_868.sub, got ok=%v best=%+v", ok, best868)
	}
}

func TestFingerprintEdgeCases(t *testing.T) {
	var nilIdx *FingerprintIndex
	if nilIdx.Len() != 0 {
		t.Fatal("expected Len=0 on nil index")
	}
	if _, ok := nilIdx.Match(Embedding{}, 433_920_000, 0.5); ok {
		t.Fatal("expected no match on nil index")
	}
	if top := nilIdx.TopK(Embedding{}, 433_920_000, 0, 0.5); top != nil {
		t.Fatalf("expected nil TopK, got %v", top)
	}
	if sim := CosineSimilarity(Embedding{}, Embedding{1}); sim != 0 {
		t.Fatalf("expected 0 similarity for zero vector, got %v", sim)
	}
	nanEmb := Embedding{float32(math.NaN())}
	if sim := CosineSimilarity(nanEmb, Embedding{1}); sim != 0 {
		t.Fatalf("expected 0 similarity for NaN vector, got %v", sim)
	}
	// CAME and KeeLoq key synthesis branches.
	_ = ComputeEmbeddingFromKey(433_920_000, "FM476", "CAME", 12, 320)
	_ = ComputeEmbeddingFromKey(433_920_000, "AM650", "KeeLoq", 66, 400)
	_ = ComputeEmbeddingFromKey(0, "", "RAW", 0, 0)
}

func FuzzFingerprintMatch(f *testing.F) {
	f.Add([]byte{10, 20, 30, 80, 120}, uint32(433_920_000))
	f.Fuzz(func(t *testing.T, data []byte, freqHz uint32) {
		raw := make([]int32, len(data))
		for i, b := range data {
			v := int32(b) * 20
			if i%2 == 1 {
				v = -v
			}
			raw[i] = v
		}
		emb := ComputeEmbeddingFromRaw(raw, freqHz, "AM650")
		sim := CosineSimilarity(emb, emb)
		if math.IsNaN(float64(sim)) || sim < 0 || sim > 1.001 {
			t.Fatalf("invalid self-similarity: %v", sim)
		}
	})
}

func BenchmarkFingerprintMatch500(b *testing.B) {
	raw := buildPrincetonRaw(350, 0)
	emb := ComputeEmbeddingFromRaw(raw, 433_920_000, "AM650")
	entries := make([]FingerprintEntry, 500)
	for i := range entries {
		entries[i] = FingerprintEntry{
			Name:      "remote.sub",
			FreqHz:    433_920_000,
			Protocol:  "RAW",
			Embedding: emb,
		}
	}
	idx := NewFingerprintIndex(entries)
	b.ReportAllocs()
	for b.Loop() {
		_, _ = idx.Match(emb, 433_920_000, 0.85)
	}
}
