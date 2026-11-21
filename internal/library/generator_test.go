package library

import (
	"errors"
	"os"
	"testing"

	"rf-heatmap/internal/subfile"
)

func TestGeneratorCatalogSynthesisAndRoundTrip(t *testing.T) {
	tests := []struct {
		packID   string
		wantLen  int
		wantFreq uint32
		idx      int
		wantName string
		wantProt string
	}{
		{PackIDGenCAME12_433, 4096, 433_920_000, 0x12A, "came12_12A.sub", "CAME"},
		{PackIDGenLinear10_315, 1024, 315_000_000, 0x2A5, "linear10_2A5.sub", "Linear"},
		{PackIDGenCAME12_868, 4096, 868_350_000, 0xFFF, "came12_868_FFF.sub", "CAME"},
	}

	for _, tc := range tests {
		t.Run(tc.packID, func(t *testing.T) {
			gen, ok := NewGeneratorCatalog(tc.packID)
			if !ok {
				t.Fatalf("NewGeneratorCatalog(%q) not found", tc.packID)
			}
			if gen.Len() != tc.wantLen {
				t.Fatalf("Len() = %d, want %d", gen.Len(), tc.wantLen)
			}
			if _, ok := gen.EntryAt(-1); ok {
				t.Fatal("EntryAt(-1) must return false")
			}
			if _, ok := gen.EntryAt(tc.wantLen); ok {
				t.Fatal("EntryAt(Len) must return false")
			}

			ref, ok := gen.EntryAt(tc.idx)
			if !ok || ref.Name != tc.wantName || ref.Source != SourceGenerated || !ref.ReadOnly {
				t.Fatalf("unexpected EntryAt(%d): %+v ok=%v", tc.idx, ref, ok)
			}
			if ref.File == nil || ref.File.FreqHz != tc.wantFreq || ref.File.Protocol != tc.wantProt {
				t.Fatalf("unexpected generated SubFile: %+v", ref.File)
			}

			// Verify O(1) ResolveByName round-trip.
			resolved, ok := gen.ResolveByName(tc.wantName)
			if !ok || resolved.Name != tc.wantName || resolved.File.Key != ref.File.Key {
				t.Fatalf("ResolveByName(%q) failed: %+v ok=%v", tc.wantName, resolved, ok)
			}

			// Verify generated key encodes and decodes cleanly.
			pulses, err := subfile.EncodePulses(ref.File)
			if err != nil {
				t.Fatalf("EncodePulses failed: %v", err)
			}
			dec, ok := subfile.DecodeOOK(pulses)
			if !ok || dec.Protocol != tc.wantProt || dec.KeyHex != ref.File.Key {
				t.Fatalf("DecodeOOK mismatch: got %+v ok=%v want %s %q", dec, ok, tc.wantProt, ref.File.Key)
			}
		})
	}
}

func TestGeneratorPromoteAndReadOnlyGuard(t *testing.T) {
	userDir := t.TempDir()
	store := NewStore(userDir, "", nil)

	cat, pack, err := store.OpenCatalog(PackIDGenCAME12_433)
	if err != nil || pack.Source != SourceGenerated {
		t.Fatalf("OpenCatalog failed: pack=%+v err=%v", pack, err)
	}
	ref, ok := cat.EntryAt(0x042)
	if !ok {
		t.Fatal("EntryAt(0x042) failed")
	}

	// Generated entries cannot be deleted.
	if err := store.DeleteUserEntry(ref); !errors.Is(err, ErrReadOnlySource) {
		t.Fatalf("expected ErrReadOnlySource, got %v", err)
	}

	// PromoteToUser writes valid .sub file to User Library.
	promoted, err := store.PromoteToUser(ref)
	if err != nil {
		t.Fatalf("PromoteToUser failed: %v", err)
	}
	if promoted.Name != "came12_042.sub" || promoted.Source != SourceUser {
		t.Fatalf("unexpected promoted entry: %+v", promoted)
	}
	raw, err := os.ReadFile(promoted.Path)
	if err != nil {
		t.Fatalf("read promoted file: %v", err)
	}
	parsed, err := subfile.Parse(string(raw))
	if err != nil || parsed.Protocol != "CAME" || parsed.Bit != 12 {
		t.Fatalf("parsed promoted file invalid: %+v err=%v", parsed, err)
	}
}

func TestProbeSession12SlotRecentHistory(t *testing.T) {
	ps := NewProbeSession(50)
	if ps.IntervalLabel() != "0.05s" {
		t.Fatalf("expected 0.05s, got %s", ps.IntervalLabel())
	}
	gen, _ := NewGeneratorCatalog(PackIDGenLinear10_315)
	ps.SetCatalogSource(gen.Pack, gen)
	for i := range 15 {
		ps.OnTXStarted(i)
	}
	if len(ps.RecentSent) != maxRecentSent {
		t.Fatalf("RecentSent len = %d, want %d", len(ps.RecentSent), maxRecentSent)
	}
	if ps.IsRecentSent(2) {
		t.Fatal("index 2 should have been evicted from 12-slot trail")
	}
	if !ps.IsRecentSent(3) || !ps.IsRecentSent(13) {
		t.Fatalf("expected indices 3..13 marked recent: %v", ps.RecentSent)
	}
}
