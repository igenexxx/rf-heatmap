package library

import (
	"testing"

	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

func TestRankEntriesByEmbeddingFrequencyGate(t *testing.T) {
	e868 := EntryRef{
		Name: "a_868.sub",
		File: &subfile.SubFile{FreqHz: 868_350_000, Preset: "FuriHalSubGhzPresetOok650Async", Protocol: "Princeton", Bit: 24, TE: 350},
	}
	eCame := EntryRef{
		Name: "b_came.sub",
		File: &subfile.SubFile{FreqHz: 433_920_000, Preset: "FuriHalSubGhzPresetOok650Async", Protocol: "CAME", Bit: 12, TE: 320},
	}
	ePrinc := EntryRef{
		Name: "c_princ.sub",
		File: &subfile.SubFile{FreqHz: 433_920_000, Preset: "FuriHalSubGhzPresetOok650Async", Protocol: "Princeton", Bit: 24, TE: 350},
	}

	query := rfml.ComputeEmbeddingFromKey(433_920_000, "FuriHalSubGhzPresetOok650Async", "Princeton", 24, 350)
	ranked, best := RankEntriesByEmbedding([]EntryRef{e868, eCame, ePrinc}, query, 433_920_000)
	if len(ranked) != 3 || ranked[0].Name != "c_princ.sub" || best < 0.99 {
		t.Fatalf("expected c_princ.sub ranked #1 (sim>=0.99), got %+v best=%.3f", ranked, best)
	}
	// 868 MHz entry must be frequency-gated to the tail.
	if ranked[2].Name != "a_868.sub" {
		t.Fatalf("expected 868 MHz entry gated to tail, got %+v", ranked)
	}
}
