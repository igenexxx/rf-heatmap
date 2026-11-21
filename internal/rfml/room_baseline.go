package rfml

import (
	"cmp"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"time"
)

// DiffClass classifies a single frequency bin's deviation from a stored RoomBaseline.
type DiffClass string

const (
	DiffNormal     DiffClass = "normal"
	DiffElevated   DiffClass = "elevated"
	DiffNewEmitter DiffClass = "new_emitter"

	// MinRoomStdDBm floors per-bin standard deviation to avoid div-by-zero on flat noise floors.
	MinRoomStdDBm float32 = 2.0
	// DiffElevatedDeltaDB is the positive dB rise threshold for DiffElevated.
	DiffElevatedDeltaDB float32 = 4.0
	// DiffElevatedZScore is the Z-score threshold for DiffElevated.
	DiffElevatedZScore float32 = 2.0
	// DiffNewEmitterDeltaDB is the positive dB rise threshold for DiffNewEmitter.
	DiffNewEmitterDeltaDB float32 = 8.0
	// DiffNewEmitterZScore is the Z-score threshold for DiffNewEmitter.
	DiffNewEmitterZScore float32 = 3.5

	maxBaselineNameLen = 64
	baselineDirPerm    = 0o700
	baselineFilePerm   = 0o600
)

var (
	// ErrInvalidBaselineName indicates a baseline name failed path-sandbox validation.
	ErrInvalidBaselineName = errors.New("invalid baseline name")
	// ErrInvalidBaselineData indicates empty, mismatched, or non-finite sweep frames.
	ErrInvalidBaselineData = errors.New("invalid baseline data")
)

// RoomBaseline stores per-bin ambient RF statistics captured across multiple sweeps.
type RoomBaseline struct {
	Name      string    `json:"name"`
	Band      string    `json:"band"`
	StartHz   uint32    `json:"start_hz"`
	StepHz    uint32    `json:"step_hz"`
	Samples   int       `json:"samples"`
	MeanDBm   []float32 `json:"mean_dbm"`
	StdDBm    []float32 `json:"std_dbm"`
	PeakDBm   []float32 `json:"peak_dbm"`
	CreatedAt time.Time `json:"created_at"`
}

// DiffBin holds the live-vs-baseline comparison metrics for one frequency bin.
type DiffBin struct {
	Bin     int       `json:"bin"`
	FreqHz  uint32    `json:"freq_hz"`
	LiveDBm float32   `json:"live_dbm"`
	BaseDBm float32   `json:"base_dbm"`
	DeltaDB float32   `json:"delta_db"`
	ZScore  float32   `json:"z_score"`
	Class   DiffClass `json:"class"`
}

// DefaultBaselinesDir returns the canonical ~/.config/rf-heatmap/baselines directory.
func DefaultBaselinesDir() string {
	home, err := os.UserHomeDir()
	if err != nil || home == "" {
		return filepath.Join(".", ".config", "rf-heatmap", "baselines")
	}
	return filepath.Join(home, ".config", "rf-heatmap", "baselines")
}

// ValidateBaselineName enforces strict path-sandbox rules on baseline identifiers.
func ValidateBaselineName(name string) error {
	if name == "" || len(name) > maxBaselineNameLen {
		return fmt.Errorf("%w: must be 1..%d chars", ErrInvalidBaselineName, maxBaselineNameLen)
	}
	if strings.HasPrefix(name, ".") || strings.Contains(name, "..") ||
		strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("%w: %q contains forbidden path characters", ErrInvalidBaselineName, name)
	}
	for i := range len(name) {
		if !isSafeBaselineChar(name[i]) {
			return fmt.Errorf("%w: %q contains invalid character %q", ErrInvalidBaselineName, name, name[i])
		}
	}
	return nil
}

func isSafeBaselineChar(ch byte) bool {
	return (ch >= 'a' && ch <= 'z') || (ch >= 'A' && ch <= 'Z') ||
		(ch >= '0' && ch <= '9') || ch == '_' || ch == '-' || ch == '.'
}

// CaptureBaselineFromFrames computes Welford per-bin mean, sample std, and peak RSSI.
func CaptureBaselineFromFrames(name, band string, startHz, stepHz uint32, frames [][]float32) (*RoomBaseline, error) {
	if err := ValidateBaselineName(name); err != nil {
		return nil, err
	}
	if len(frames) == 0 || len(frames[0]) == 0 {
		return nil, fmt.Errorf("%w: at least one non-empty frame is required", ErrInvalidBaselineData)
	}
	bins := len(frames[0])
	if err := validateFrames(frames, bins); err != nil {
		return nil, err
	}
	mean, std, peak := computeWelfordBins(frames, bins)
	return &RoomBaseline{
		Name: name, Band: band, StartHz: startHz, StepHz: stepHz,
		Samples: len(frames), MeanDBm: mean, StdDBm: std, PeakDBm: peak, CreatedAt: time.Now().UTC(),
	}, nil
}

func validateFrames(frames [][]float32, bins int) error {
	for fi, f := range frames {
		if len(f) != bins {
			return fmt.Errorf("%w: frame %d has %d bins, want %d", ErrInvalidBaselineData, fi, len(f), bins)
		}
		for bi, v := range f {
			if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
				return fmt.Errorf("%w: frame %d bin %d is non-finite", ErrInvalidBaselineData, fi, bi)
			}
		}
	}
	return nil
}

func computeWelfordBins(frames [][]float32, bins int) ([]float32, []float32, []float32) {
	mean64 := make([]float64, bins)
	m2 := make([]float64, bins)
	peak := make([]float32, bins)
	for i := range bins {
		peak[i] = frames[0][i]
	}

	for fi, frame := range frames {
		count := float64(fi + 1)
		for i, v := range frame {
			x := float64(v)
			delta := x - mean64[i]
			mean64[i] += delta / count
			m2[i] += delta * (x - mean64[i])
			if v > peak[i] {
				peak[i] = v
			}
		}
	}

	mean := make([]float32, bins)
	std := make([]float32, bins)
	denom := float64(len(frames) - 1)
	for i := range bins {
		mean[i] = float32(mean64[i])
		if denom > 0 && m2[i] > 0 {
			std[i] = float32(math.Sqrt(m2[i] / denom))
		}
	}
	return mean, std, peak
}

// ComputeDiff compares liveDBm against base into caller-owned dst with 0 heap allocations
// whenever cap(dst) >= min(len(base.MeanDBm), len(liveDBm)).
func ComputeDiff(base *RoomBaseline, liveDBm []float32, dst []DiffBin) []DiffBin {
	if base == nil {
		return dst[:0]
	}
	n := min(len(base.MeanDBm), len(liveDBm))
	if n == 0 {
		return dst[:0]
	}
	if cap(dst) < n {
		dst = make([]DiffBin, n)
	} else {
		dst = dst[:n]
	}

	stdSlice := base.StdDBm
	hasStd := len(stdSlice) >= n
	for i := range n {
		baseMean := base.MeanDBm[i]
		live := liveDBm[i]
		delta := live - baseMean
		std := MinRoomStdDBm
		if hasStd && stdSlice[i] > std {
			std = stdSlice[i]
		}
		z := delta / std
		dst[i] = DiffBin{
			Bin:     i,
			FreqHz:  base.StartHz + uint32(i)*base.StepHz,
			LiveDBm: live,
			BaseDBm: baseMean,
			DeltaDB: delta,
			ZScore:  z,
			Class:   classifyDiff(delta, z),
		}
	}
	return dst
}

func classifyDiff(delta, z float32) DiffClass {
	if delta >= DiffNewEmitterDeltaDB || z >= DiffNewEmitterZScore {
		return DiffNewEmitter
	}
	if delta >= DiffElevatedDeltaDB || z >= DiffElevatedZScore {
		return DiffElevated
	}
	return DiffNormal
}

func resolveBaselinePath(dir, name string) (string, error) {
	if err := ValidateBaselineName(name); err != nil {
		return "", err
	}
	if dir == "" {
		dir = DefaultBaselinesDir()
	}
	cleanDir := filepath.Clean(dir)
	full := filepath.Join(cleanDir, name+".json")
	if filepath.Dir(full) != cleanDir {
		return "", fmt.Errorf("%w: path escaped sandbox", ErrInvalidBaselineName)
	}
	return full, nil
}

// SaveRoomBaselineAtomic writes base as 0600 JSON into dir/<name>.json using temp-file + rename.
func SaveRoomBaselineAtomic(dir string, base *RoomBaseline) (string, error) {
	if base == nil {
		return "", fmt.Errorf("%w: nil baseline", ErrInvalidBaselineData)
	}
	if len(base.MeanDBm) == 0 || len(base.StdDBm) != len(base.MeanDBm) || len(base.PeakDBm) != len(base.MeanDBm) {
		return "", fmt.Errorf("%w: inconsistent bin slices", ErrInvalidBaselineData)
	}
	target, err := resolveBaselinePath(dir, base.Name)
	if err != nil {
		return "", err
	}
	parent := filepath.Dir(target)
	if err := os.MkdirAll(parent, baselineDirPerm); err != nil {
		return "", fmt.Errorf("create baselines dir: %w", err)
	}
	data, err := json.MarshalIndent(base, "", "  ")
	if err != nil {
		return "", fmt.Errorf("marshal baseline: %w", err)
	}
	if err := writeAtomicFile(parent, target, data); err != nil {
		return "", err
	}
	return target, nil
}

func writeAtomicFile(dir, target string, data []byte) error {
	tmp, err := os.CreateTemp(dir, ".baseline-*.tmp")
	if err != nil {
		return fmt.Errorf("create temp baseline: %w", err)
	}
	tmpName := tmp.Name()
	cleanup := true
	defer func() {
		if cleanup {
			_ = os.Remove(tmpName)
		}
	}()
	if err := tmp.Chmod(baselineFilePerm); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("chmod temp baseline: %w", err)
	}
	if _, err := tmp.Write(data); err != nil {
		_ = tmp.Close()
		return fmt.Errorf("write temp baseline: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return fmt.Errorf("close temp baseline: %w", err)
	}
	if err := os.Rename(tmpName, target); err != nil {
		return fmt.Errorf("rename baseline: %w", err)
	}
	cleanup = false
	return nil
}

// LoadRoomBaseline loads and validates a named baseline from dir.
func LoadRoomBaseline(dir, name string) (*RoomBaseline, error) {
	target, err := resolveBaselinePath(dir, name)
	if err != nil {
		return nil, err
	}
	data, err := os.ReadFile(target)
	if err != nil {
		return nil, err
	}
	var base RoomBaseline
	if err := json.Unmarshal(data, &base); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalidBaselineData, err)
	}
	if base.Name == "" {
		base.Name = name
	}
	if len(base.MeanDBm) == 0 || len(base.StdDBm) != len(base.MeanDBm) || len(base.PeakDBm) != len(base.MeanDBm) {
		return nil, fmt.Errorf("%w: corrupt bin arrays in %q", ErrInvalidBaselineData, name)
	}
	return &base, nil
}

// ListRoomBaselines lists all valid saved baselines in dir, sorted by Name.
func ListRoomBaselines(dir string) ([]RoomBaseline, error) {
	if dir == "" {
		dir = DefaultBaselinesDir()
	}
	entries, err := os.ReadDir(dir)
	if err != nil {
		if errors.Is(err, os.ErrNotExist) {
			return []RoomBaseline{}, nil
		}
		return nil, err
	}
	out := make([]RoomBaseline, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		name := strings.TrimSuffix(entry.Name(), ".json")
		base, err := LoadRoomBaseline(dir, name)
		if err != nil {
			continue
		}
		out = append(out, *base)
	}
	slices.SortFunc(out, func(a, b RoomBaseline) int {
		return cmp.Compare(a.Name, b.Name)
	})
	return out, nil
}

// DeleteRoomBaseline removes a sandboxed baseline JSON file from dir.
func DeleteRoomBaseline(dir, name string) error {
	target, err := resolveBaselinePath(dir, name)
	if err != nil {
		return err
	}
	return os.Remove(target)
}
