package library

import (
	"errors"
	"os"
	"path/filepath"
	"testing"

	"rf-heatmap/internal/subfile"
)

func TestSaveUserSubFileAtomicAndSandbox(t *testing.T) {
	tmp := t.TempDir()
	userDir := filepath.Join(tmp, "captures")
	dbDir := filepath.Join(tmp, "databases")
	packDir := filepath.Join(dbDir, "fans")
	if err := os.MkdirAll(packDir, 0o755); err != nil {
		t.Fatal(err)
	}
	origDBPath := filepath.Join(packDir, "fan.sub")
	origDBData := "Filetype: Flipper SubGhz RAW File\nVersion: 1\nFrequency: 433920000\nPreset: FuriHalSubGhzPresetOok650Async\nProtocol: RAW\nRAW_Data: 350 -1050\n"
	if err := os.WriteFile(origDBPath, []byte(origDBData), 0o644); err != nil {
		t.Fatal(err)
	}

	store := NewStore(userDir, dbDir, nil)
	sf := &subfile.SubFile{
		FileType: "Flipper SubGhz RAW File",
		Version:  1,
		FreqHz:   433_920_000,
		Preset:   "FuriHalSubGhzPresetOok650Async",
		Protocol: "RAW",
		RawData:  []int32{350, -1050, 1050, -350},
	}

	// First save -> clean_trim.sub.
	ref1, err := store.SaveUserSubFileAtomic("clean_trim.sub", sf)
	if err != nil || ref1.Name != "clean_trim.sub" {
		t.Fatalf("first save got (%+v, %v)", ref1, err)
	}
	// Second save with same name -> collision-safe clean_trim_2.sub.
	ref2, err := store.SaveUserSubFileAtomic("clean_trim.sub", sf)
	if err != nil || ref2.Name != "clean_trim_2.sub" {
		t.Fatalf("collision-safe save got (%+v, %v)", ref2, err)
	}

	// Path traversal & nil SubFile MUST reject.
	for _, bad := range []string{"../databases/evil.sub", "/tmp/evil.sub", "sub/evil.sub", "noext"} {
		if _, err := store.SaveUserSubFileAtomic(bad, sf); !errors.Is(err, ErrInvalidPath) {
			t.Fatalf("expected ErrInvalidPath for %q, got %v", bad, err)
		}
	}
	if _, err := store.SaveUserSubFileAtomic("ok.sub", nil); !errors.Is(err, subfile.ErrNilSubFile) {
		t.Fatalf("expected ErrNilSubFile, got %v", err)
	}

	// Read-only DB must remain untouched.
	gotDB, err := os.ReadFile(origDBPath)
	if err != nil || string(gotDB) != origDBData {
		t.Fatalf("external database was modified! err=%v", err)
	}
}
