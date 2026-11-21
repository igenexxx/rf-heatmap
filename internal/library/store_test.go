package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"rf-heatmap/internal/subfile"
)

func sampleSubText(key string) string {
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz Key File",
		Version:  1,
		FreqHz:   433_920_000,
		Preset:   "FuriHalSubGhzPresetOok650Async",
		Protocol: "Princeton",
		Bit:      24,
		Key:      key,
		TE:       350,
	}
	return sf.Format()
}

func TestStoreSeparationAndReadOnlyInvariants(t *testing.T) {
	root := t.TempDir()
	userDir := filepath.Join(root, "captures")
	dbDir := filepath.Join(root, "databases")
	subgDir := filepath.Join(root, "subg")

	mustWrite := func(path, body string) {
		t.Helper()
		if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
			t.Fatalf("mkdir: %v", err)
		}
		if err := os.WriteFile(path, []byte(body), 0o644); err != nil {
			t.Fatalf("write %s: %v", path, err)
		}
	}

	mustWrite(filepath.Join(userDir, "my_gate.sub"), sampleSubText("00 00 00 00 00 11 22 33"))
	mustWrite(filepath.Join(dbDir, "fans", "fan_power.sub"), sampleSubText("00 00 00 00 00 AA BB CC"))
	mustWrite(filepath.Join(dbDir, "fans", "fan_speed.sub"), sampleSubText("00 00 00 00 00 AA BB DD"))
	mustWrite(filepath.Join(dbDir, "blinds", "up.sub"), sampleSubText("00 00 00 00 00 44 55 66"))
	mustWrite(filepath.Join(subgDir, "legacy.sub"), sampleSubText("00 00 00 00 00 77 88 99"))

	store := NewStore(userDir, dbDir, []string{subgDir})
	packs := store.ListPacks()
	if len(packs) != 7 {
		t.Fatalf("expected 7 packs (3 external + 3 generators + user), got %d: %+v", len(packs), packs)
	}
	// User Library must be last and ReadOnly=false; external & generator packs must be ReadOnly=true.
	if packs[len(packs)-1].ID != PackIDUser || packs[len(packs)-1].ReadOnly {
		t.Fatalf("unexpected user pack: %+v", packs[len(packs)-1])
	}
	for _, p := range packs[:len(packs)-1] {
		if !p.ReadOnly || (p.Source != SourceExternal && p.Source != SourceGenerated) {
			t.Fatalf("non-user pack must be read-only external/generated: %+v", p)
		}
	}

	// User entries must strictly contain only ~/rf-heatmap/captures files.
	userEntries := store.ListUserEntries()
	if len(userEntries) != 1 || userEntries[0].Name != "my_gate.sub" {
		t.Fatalf("unexpected user entries: %+v", userEntries)
	}

	// External entries must be lazy-loaded and protected from deletion.
	fanEntries, err := store.ListEntries("db:fans")
	if err != nil || len(fanEntries) != 2 {
		t.Fatalf("ListEntries(db:fans) got (%d, %v), want (2, nil)", len(fanEntries), err)
	}
	if fanEntries[0].File != nil {
		t.Fatal("expected lazy-loaded nil File before LoadSubFile")
	}
	if err := store.DeleteUserEntry(fanEntries[0]); !errors.Is(err, ErrReadOnlySource) {
		t.Fatalf("expected ErrReadOnlySource deleting external entry, got %v", err)
	}

	// Promote external entry to User Library (and verify collision-safe second promote).
	promoted1, err := store.PromoteToUser(fanEntries[0])
	if err != nil {
		t.Fatalf("PromoteToUser failed: %v", err)
	}
	if promoted1.Name != "fan_power.sub" || promoted1.Source != SourceUser || promoted1.ReadOnly {
		t.Fatalf("unexpected promoted entry: %+v", promoted1)
	}
	promoted2, err := store.PromoteToUser(fanEntries[0])
	if err != nil {
		t.Fatalf("second PromoteToUser failed: %v", err)
	}
	if promoted2.Name != "fan_power_2.sub" {
		t.Fatalf("expected collision-safe fan_power_2.sub, got %q", promoted2.Name)
	}

	// Delete promoted user entry succeeds; original external file remains untouched.
	if err := store.DeleteUserEntry(promoted2); err != nil {
		t.Fatalf("DeleteUserEntry failed: %v", err)
	}
	if _, err := os.Stat(fanEntries[0].Path); err != nil {
		t.Fatalf("external DB file must remain intact: %v", err)
	}

	// DeleteDatabaseEntry succeeds for ~/rf-heatmap/databases/fans, but rejects ext:subg.
	subgEntries, err := store.ListEntries(PackIDSubg)
	if err != nil || len(subgEntries) != 1 {
		t.Fatalf("ListEntries(ext:subg) failed: %v", err)
	}
	if err := store.DeleteDatabaseEntry(subgEntries[0]); !errors.Is(err, ErrReadOnlySource) {
		t.Fatalf("expected ErrReadOnlySource deleting ext:subg, got %v", err)
	}
	if err := store.DeleteDatabaseEntry(fanEntries[1]); err != nil {
		t.Fatalf("DeleteDatabaseEntry(db:fans) failed: %v", err)
	}
	if _, err := os.Stat(fanEntries[1].Path); !os.IsNotExist(err) {
		t.Fatalf("expected fanEntries[1] deleted, err=%v", err)
	}
}

func TestSanitizeSubNameAdversarial(t *testing.T) {
	bad := []string{"", ".sub", "../evil.sub", "a/b.sub", `a\b.sub`, "file.txt", "a\x00b.sub"}
	for _, name := range bad {
		if _, err := SanitizeSubName(name); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("SanitizeSubName(%q) = %v, want ErrInvalidPath", name, err)
		}
	}
	if got, err := SanitizeSubName("valid_1.sub"); err != nil || got != "valid_1.sub" {
		t.Fatalf("SanitizeSubName(valid_1.sub) = (%q, %v)", got, err)
	}
}
