package rfml

import (
	"errors"
	"math"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestValidateBaselineName(t *testing.T) {
	tests := []struct {
		name    string
		input   string
		wantErr bool
	}{
		{name: "simple name", input: "room_live", wantErr: false},
		{name: "alphanumeric with dash and dot", input: "lab-floor1.433", wantErr: false},
		{name: "empty", input: "", wantErr: true},
		{name: "too long", input: strings.Repeat("a", 65), wantErr: true},
		{name: "dotdot traversal", input: "../secret", wantErr: true},
		{name: "embedded dotdot", input: "foo..bar", wantErr: true},
		{name: "forward slash", input: "sub/dir", wantErr: true},
		{name: "backslash", input: `sub\dir`, wantErr: true},
		{name: "hidden file", input: ".hidden", wantErr: true},
		{name: "space rejected", input: "my room", wantErr: true},
		{name: "null byte rejected", input: "room\x00x", wantErr: true},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			err := ValidateBaselineName(tc.input)
			if tc.wantErr {
				if !errors.Is(err, ErrInvalidBaselineName) {
					t.Fatalf("ValidateBaselineName(%q) = %v, want ErrInvalidBaselineName", tc.input, err)
				}
				return
			}
			if err != nil {
				t.Fatalf("ValidateBaselineName(%q) unexpected err: %v", tc.input, err)
			}
		})
	}
}

func TestCaptureBaselineFromFrames(t *testing.T) {
	t.Run("exact Welford mean std peak", func(t *testing.T) {
		frames := [][]float32{
			{-100, -90, -80},
			{-102, -88, -80},
			{-98, -92, -76},
		}
		base, err := CaptureBaselineFromFrames("office", "433 ISM", 433_050_000, 50_000, frames)
		if err != nil {
			t.Fatalf("CaptureBaselineFromFrames failed: %v", err)
		}
		if base.Samples != 3 || base.Name != "office" || base.Band != "433 ISM" {
			t.Fatalf("unexpected header: %+v", base)
		}
		// Bin 0: {-100, -102, -98} -> mean=-100, sample std=2.0, peak=-98
		if math.Abs(float64(base.MeanDBm[0]-(-100))) > 1e-4 {
			t.Fatalf("bin0 mean = %v, want -100", base.MeanDBm[0])
		}
		if math.Abs(float64(base.StdDBm[0]-2.0)) > 1e-4 {
			t.Fatalf("bin0 std = %v, want 2.0", base.StdDBm[0])
		}
		if base.PeakDBm[0] != -98 {
			t.Fatalf("bin0 peak = %v, want -98", base.PeakDBm[0])
		}
		// Bin 2: {-80, -80, -76} -> peak=-76
		if base.PeakDBm[2] != -76 {
			t.Fatalf("bin2 peak = %v, want -76", base.PeakDBm[2])
		}
	})

	t.Run("single frame has zero std", func(t *testing.T) {
		base, err := CaptureBaselineFromFrames("single", "868 EU", 868_000_000, 50_000, [][]float32{{-105, -101}})
		if err != nil {
			t.Fatalf("unexpected err: %v", err)
		}
		if base.StdDBm[0] != 0 || base.StdDBm[1] != 0 {
			t.Fatalf("expected 0 std for single frame, got %v", base.StdDBm)
		}
	})

	t.Run("invalid inputs rejected", func(t *testing.T) {
		cases := []struct {
			name    string
			bname   string
			frames  [][]float32
			wantErr error
		}{
			{"bad name", "../bad", [][]float32{{-100}}, ErrInvalidBaselineName},
			{"no frames", "ok", nil, ErrInvalidBaselineData},
			{"empty frame", "ok", [][]float32{{}}, ErrInvalidBaselineData},
			{"mismatched bins", "ok", [][]float32{{-100, -101}, {-100}}, ErrInvalidBaselineData},
			{"NaN value", "ok", [][]float32{{float32(math.NaN())}}, ErrInvalidBaselineData},
			{"Inf value", "ok", [][]float32{{float32(math.Inf(1))}}, ErrInvalidBaselineData},
		}
		for _, tc := range cases {
			t.Run(tc.name, func(t *testing.T) {
				_, err := CaptureBaselineFromFrames(tc.bname, "433 ISM", 433_050_000, 50_000, tc.frames)
				if !errors.Is(err, tc.wantErr) {
					t.Fatalf("got err %v, want %v", err, tc.wantErr)
				}
			})
		}
	})
}

func TestComputeDiffClassificationAndBounds(t *testing.T) {
	base := &RoomBaseline{
		Name:    "room",
		StartHz: 433_000_000,
		StepHz:  50_000,
		MeanDBm: []float32{-100, -100, -100, -100, -100},
		StdDBm:  []float32{0.5, 2.0, 2.0, 5.0, 2.0}, // bin 0 floored to 2.0
		PeakDBm: []float32{-99, -98, -98, -95, -98},
	}
	// Bin 0: delta=+2.0, z=1.0 -> normal
	// Bin 1: delta=+4.0, z=2.0 -> elevated (exact boundary)
	// Bin 2: delta=+7.0, z=3.5 -> new_emitter (Z boundary)
	// Bin 3: delta=+8.0, z=1.6 -> new_emitter (Delta boundary)
	// Bin 4: delta=-5.0, z=-2.5 -> normal (drop is not an emitter)
	live := []float32{-98, -96, -93, -92, -105}
	dst := make([]DiffBin, 0, 8)
	got := ComputeDiff(base, live, dst)
	if len(got) != 5 {
		t.Fatalf("len(got) = %d, want 5", len(got))
	}
	wantClasses := []DiffClass{DiffNormal, DiffElevated, DiffNewEmitter, DiffNewEmitter, DiffNormal}
	for i, want := range wantClasses {
		if got[i].Class != want {
			t.Errorf("bin %d class = %q, want %q (delta=%.1f z=%.2f)", i, got[i].Class, want, got[i].DeltaDB, got[i].ZScore)
		}
		if got[i].FreqHz != 433_000_000+uint32(i)*50_000 {
			t.Errorf("bin %d freq = %d, want %d", i, got[i].FreqHz, 433_000_000+uint32(i)*50_000)
		}
	}

	// Nil baseline and empty live slice must return empty without panicking.
	if out := ComputeDiff(nil, live, dst); len(out) != 0 {
		t.Fatalf("nil base returned %d bins", len(out))
	}
	if out := ComputeDiff(base, nil, dst); len(out) != 0 {
		t.Fatalf("nil live returned %d bins", len(out))
	}
	// Under-sized dst safely allocates and succeeds.
	if out := ComputeDiff(base, live, nil); len(out) != 5 {
		t.Fatalf("nil dst returned %d bins, want 5", len(out))
	}
}

func TestRoomBaselinePersistenceRoundTripAndSandbox(t *testing.T) {
	dir := filepath.Join(t.TempDir(), "baselines")

	// Missing directory lists cleanly as empty.
	list, err := ListRoomBaselines(dir)
	if err != nil || len(list) != 0 {
		t.Fatalf("ListRoomBaselines(missing) = (%d, %v), want (0, nil)", len(list), err)
	}

	b1, err := CaptureBaselineFromFrames("zeta_room", "433 ISM", 433_050_000, 50_000, [][]float32{{-104, -102}, {-102, -100}})
	if err != nil {
		t.Fatalf("capture b1: %v", err)
	}
	b2, err := CaptureBaselineFromFrames("alpha_lab", "868 EU", 868_000_000, 50_000, [][]float32{{-108, -107}})
	if err != nil {
		t.Fatalf("capture b2: %v", err)
	}

	path1, err := SaveRoomBaselineAtomic(dir, b1)
	if err != nil {
		t.Fatalf("SaveRoomBaselineAtomic(b1): %v", err)
	}
	if _, err := SaveRoomBaselineAtomic(dir, b2); err != nil {
		t.Fatalf("SaveRoomBaselineAtomic(b2): %v", err)
	}

	// Verify 0600 permissions.
	info, err := os.Stat(path1)
	if err != nil {
		t.Fatalf("stat %s: %v", path1, err)
	}
	if perm := info.Mode().Perm(); perm != 0o600 {
		t.Fatalf("baseline file perm = %04o, want 0600", perm)
	}

	// Load and compare.
	loaded, err := LoadRoomBaseline(dir, "zeta_room")
	if err != nil {
		t.Fatalf("LoadRoomBaseline: %v", err)
	}
	if loaded.Name != "zeta_room" || loaded.Samples != 2 || len(loaded.MeanDBm) != 2 {
		t.Fatalf("loaded mismatch: %+v", loaded)
	}

	// List returns sorted order.
	list, err = ListRoomBaselines(dir)
	if err != nil || len(list) != 2 || list[0].Name != "alpha_lab" || list[1].Name != "zeta_room" {
		t.Fatalf("ListRoomBaselines = %+v (err=%v)", list, err)
	}

	// Path traversal rejected on save, load, delete.
	if _, err := SaveRoomBaselineAtomic(dir, &RoomBaseline{Name: "../escape", MeanDBm: []float32{-100}, StdDBm: []float32{2}, PeakDBm: []float32{-100}}); !errors.Is(err, ErrInvalidBaselineName) {
		t.Fatalf("expected ErrInvalidBaselineName on save traversal, got %v", err)
	}
	if _, err := LoadRoomBaseline(dir, "../escape"); !errors.Is(err, ErrInvalidBaselineName) {
		t.Fatalf("expected ErrInvalidBaselineName on load traversal, got %v", err)
	}
	if err := DeleteRoomBaseline(dir, "../escape"); !errors.Is(err, ErrInvalidBaselineName) {
		t.Fatalf("expected ErrInvalidBaselineName on delete traversal, got %v", err)
	}

	// Delete removes file cleanly.
	if err := DeleteRoomBaseline(dir, "zeta_room"); err != nil {
		t.Fatalf("DeleteRoomBaseline: %v", err)
	}
	if _, err := LoadRoomBaseline(dir, "zeta_room"); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("expected ErrNotExist after delete, got %v", err)
	}
}

func BenchmarkComputeDiff(b *testing.B) {
	const bins = 36
	mean := make([]float32, bins)
	std := make([]float32, bins)
	peak := make([]float32, bins)
	live := make([]float32, bins)
	for i := range bins {
		mean[i] = -104.0
		std[i] = 2.0
		peak[i] = -100.0
		live[i] = -103.0
	}
	live[18] = -52.0 // active new emitter
	base := &RoomBaseline{
		Name:    "bench_room",
		Band:    "433 ISM",
		StartHz: 433_050_000,
		StepHz:  49_714,
		Samples: 10,
		MeanDBm: mean,
		StdDBm:  std,
		PeakDBm: peak,
	}
	dst := make([]DiffBin, bins)

	b.ReportAllocs()
	for b.Loop() {
		dst = ComputeDiff(base, live, dst)
	}
	if len(dst) != bins || dst[18].Class != DiffNewEmitter {
		b.Fatalf("unexpected diff result: len=%d bin18=%+v", len(dst), dst[18])
	}
}
