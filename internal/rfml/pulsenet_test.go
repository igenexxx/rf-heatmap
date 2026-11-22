package rfml

import (
	"encoding/binary"
	"encoding/json"
	"errors"
	"math"
	"os"
	"path/filepath"
	"sync"
	"testing"
)

type goldenPulseVector struct {
	Pulses      []int32   `json:"pulses"`
	FreqHz      uint32    `json:"freq_hz"`
	IsFSK       bool      `json:"is_fsk"`
	SNRDB       float64   `json:"snr_db"`
	FineClass   int       `json:"fine_class"`
	FineProbs   []float32 `json:"fine_probs"`
	CoarseClass int       `json:"coarse_class"`
	CoarseProbs []float32 `json:"coarse_probs"`
	Embedding   []float32 `json:"embedding"`
}

// TestPulseNetGoldenParity verifies bit-exact numerical parity (<= 1e-4 max error
// and 100% argmax match) between Python INT8 reference and Pure-Go PulseNet-RF
// across all 64 golden test vectors exported from Colab A100.
func TestPulseNetGoldenParity(t *testing.T) {
	rawJSON, err := os.ReadFile(filepath.Join("testdata", "golden_rf.json"))
	if err != nil {
		t.Fatalf("read golden_rf.json: %v", err)
	}
	var vectors []goldenPulseVector
	if err := json.Unmarshal(rawJSON, &vectors); err != nil {
		t.Fatalf("unmarshal golden_rf.json: %v", err)
	}
	if len(vectors) == 0 {
		t.Fatal("golden_rf.json is empty")
	}

	m, info := LoadDefaultPulseNet("")
	if m == nil || info.Source != "embedded" {
		t.Fatalf("LoadDefaultPulseNet failed: info=%+v", info)
	}
	var ws PulseNetWorkspace
	maxErr := float64(0)

	for i, vec := range vectors {
		preset := "AM650"
		if vec.IsFSK {
			preset = "FM238"
		}
		res := m.Infer(&ws, vec.Pulses, vec.FreqHz, preset, vec.SNRDB)

		if int(res.FineClassID) != vec.FineClass {
			t.Errorf("vec[%d]: fine class got %d (%s), want %d", i, res.FineClassID, res.ProtocolLabel, vec.FineClass)
		}
		if int(res.CoarseClass) != vec.CoarseClass {
			t.Errorf("vec[%d]: coarse class got %d, want %d", i, res.CoarseClass, vec.CoarseClass)
		}
		for c, want := range vec.CoarseProbs {
			if d := math.Abs(float64(res.CoarseProbs[c] - want)); d > maxErr {
				maxErr = d
			}
			if math.Abs(float64(res.CoarseProbs[c]-want)) > 1e-4 {
				t.Errorf("vec[%d]: coarse prob[%d] got %.6f, want %.6f", i, c, res.CoarseProbs[c], want)
			}
		}
		for dIdx, want := range vec.Embedding {
			if d := math.Abs(float64(res.Embedding[dIdx] - want)); d > maxErr {
				maxErr = d
			}
			if math.Abs(float64(res.Embedding[dIdx]-want)) > 1e-4 {
				t.Errorf("vec[%d]: embedding[%d] got %.6f, want %.6f", i, dIdx, res.Embedding[dIdx], want)
			}
		}
	}
	t.Logf("verified %d golden RF vectors: 100%% argmax parity, max abs diff = %.2e", len(vectors), maxErr)
}

// TestLoadCZMLAdversarial verifies strict binary validation, SHA-256 integrity,
// domain checks, size limits, and safe fallback on malformed .czml blobs.
func TestLoadCZMLAdversarial(t *testing.T) {
	validCopy := append([]byte(nil), embeddedPulseNetRF...)
	badMagic := append([]byte(nil), embeddedPulseNetRF...)
	badMagic[0] = 'X'

	badVer := append([]byte(nil), embeddedPulseNetRF...)
	binary.LittleEndian.PutUint16(badVer[4:6], 99)

	badSHA := append([]byte(nil), embeddedPulseNetRF...)
	badSHA[len(badSHA)-1] ^= 0xFF

	cases := []struct {
		name    string
		data    []byte
		domain  CZMLDomain
		wantErr error
	}{
		{"nil_slice", nil, DomainRFPulse, ErrInvalidCZML},
		{"truncated_header", validCopy[:32], DomainRFPulse, ErrInvalidCZML},
		{"bad_magic", badMagic, DomainRFPulse, ErrInvalidCZML},
		{"unsupported_version", badVer, DomainRFPulse, ErrInvalidCZML},
		{"wrong_domain", validCopy, DomainRFSpectrum, ErrCZMLDomainMismatch},
		{"corrupted_sha256", badSHA, DomainRFPulse, ErrCZMLChecksum},
		{"oversized_blob", make([]byte, czmlMaxFileBytes+1), DomainRFPulse, ErrInvalidCZML},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			_, err := LoadCZMLBytes(tc.data, tc.domain)
			if !errors.Is(err, tc.wantErr) {
				t.Fatalf("LoadCZMLBytes(%s): got err=%v, want errors.Is(_, %v)", tc.name, err, tc.wantErr)
			}
		})
	}

	t.Run("corrupt_override_falls_back_to_embedded", func(t *testing.T) {
		dir := t.TempDir()
		badPath := filepath.Join(dir, "pulsenet_rf.czml")
		if err := os.WriteFile(badPath, badSHA, 0o600); err != nil {
			t.Fatal(err)
		}
		m, info := LoadDefaultPulseNet(badPath)
		if m == nil || info.Source != "embedded" {
			t.Fatalf("expected embedded fallback, got m=%v info=%+v", m, info)
		}
	})

	t.Run("valid_override_loaded", func(t *testing.T) {
		dir := t.TempDir()
		okPath := filepath.Join(dir, "pulsenet_rf.czml")
		if err := os.WriteFile(okPath, embeddedPulseNetRF, 0o600); err != nil {
			t.Fatal(err)
		}
		m, info := LoadDefaultPulseNet(okPath)
		if m == nil || info.Source != "override" {
			t.Fatalf("expected override model, got m=%v info=%+v", m, info)
		}
	})
}

// TestPulseNetEdgeInputsAndDenoise verifies zero-value/NaN/Inf resilience,
// strict +/- sign alternation after neural denoising, and concurrent safety.
func TestPulseNetEdgeInputsAndDenoise(t *testing.T) {
	m, _ := LoadDefaultPulseNet("")
	if m == nil {
		t.Fatal("embedded PulseNet-RF unavailable")
	}
	var ws PulseNetWorkspace

	// Edge inputs must never panic or return NaN.
	edgeCases := [][]int32{
		nil,
		{},
		{0, 0, 0},
		{math.MaxInt32, math.MinInt32, -1, 1},
		{10, -10, 5, -5},
	}
	for idx, raw := range edgeCases {
		res := m.Infer(&ws, raw, 0, "am650", math.NaN())
		if math.IsNaN(float64(res.ProtocolConfidence)) || math.IsNaN(float64(res.CoarseConfidence)) {
			t.Fatalf("edge[%d]: NaN confidence in %+v", idx, res)
		}
	}

	// Neural denoiser must preserve strict +/- sign alternation & non-empty output on valid pulse train.
	noisyPrinceton := make([]int32, 0, 128)
	for range 3 {
		noisyPrinceton = append(noisyPrinceton, -11000)
		for b := range 24 {
			if b%2 == 0 {
				noisyPrinceton = append(noisyPrinceton, 440, -1080) // asymmetric AGC stretch
			} else {
				noisyPrinceton = append(noisyPrinceton, 1140, -280)
			}
		}
		noisyPrinceton = append(noisyPrinceton, 25, -20) // EMI glitches
	}
	dst := make([]int32, 0, len(noisyPrinceton))
	cleaned, _, ok := DenoisePulsesNeural(noisyPrinceton, 433_920_000, "AM650", 12.0)
	if !ok || len(cleaned) == 0 {
		t.Fatalf("expected denoised pulses, got len=%d ok=%v", len(cleaned), ok)
	}
	for i := 1; i < len(cleaned); i++ {
		if (cleaned[i-1] > 0) == (cleaned[i] > 0) {
			t.Fatalf("denoiser violated +/- alternation at %d: %d, %d", i, cleaned[i-1], cleaned[i])
		}
	}
	_ = dst

	// Concurrent inference test for -race detector.
	var wg sync.WaitGroup
	for range 8 {
		wg.Add(1)
		go func() {
			defer wg.Done()
			for range 10 {
				p, _, _ := ClassifyPulsesWithPreset(noisyPrinceton, 433_920_000, "AM650", 18.0)
				if p.ProtocolLabel == "" {
					t.Errorf("empty protocol label under concurrency")
				}
			}
		}()
	}
	wg.Wait()
}

// TestSpectrumNet2DScenes verifies SpectrumNet-2D discriminates key 2D waterfall scenes
// including Quiet floor, CW Jammer, and RollJam (adjacent jammer + bursty fob).
func TestSpectrumNet2DScenes(t *testing.T) {
	info := ActiveSpectrumNetInfo()
	if info.Source != "embedded" || info.ByteSize == 0 || len(info.FineClasses) != SpectrumClasses {
		t.Fatalf("unexpected SpectrumNet info: %+v", info)
	}

	var floor [SpectrumCols]float32
	for c := range SpectrumCols {
		floor[c] = -104
	}

	makeBase := func() [SpectrumRows][SpectrumCols]int32 {
		var wf [SpectrumRows][SpectrumCols]int32
		for r := range SpectrumRows {
			for c := range SpectrumCols {
				wf[r][c] = -104 + int32((r*7+c*3)%5) - 2
			}
		}
		return wf
	}

	t.Run("quiet_floor", func(t *testing.T) {
		wf := makeBase()
		pred, ok := ClassifySpectrum(&wf, &floor)
		if !ok {
			t.Fatal("ClassifySpectrum failed")
		}
		if pred.Class != SpectrumQuiet {
			t.Fatalf("quiet scene got %s (%.2f), want Quiet", pred.Label, pred.Confidence)
		}
		if pred.IsAlert {
			t.Fatalf("quiet scene triggered unexpected alert")
		}
	})

	t.Run("cw_jammer", func(t *testing.T) {
		wf := makeBase()
		for r := range SpectrumRows {
			wf[r][18] = -52
			wf[r][17] = -76
			wf[r][19] = -76
		}
		pred, ok := ClassifySpectrum(&wf, &floor)
		if !ok {
			t.Fatal("ClassifySpectrum failed")
		}
		if pred.Class != SpectrumCWJammer || !pred.IsAlert {
			t.Fatalf("cw jammer got %s (%.2f, alert=%v), want CW_Jammer alert", pred.Label, pred.Confidence, pred.IsAlert)
		}
	})

	t.Run("rolljam_suspect", func(t *testing.T) {
		wf := makeBase()
		for r := 2; r < 28; r++ {
			wf[r][19] = -60 // adjacent jammer
		}
		for _, bt := range []int{6, 14, 21} {
			for dt := range 3 {
				wf[bt+dt][18] = -58 // bursty keyfob
			}
		}
		pred, ok := ClassifySpectrum(&wf, &floor)
		if !ok {
			t.Fatal("ClassifySpectrum failed")
		}
		if pred.Class != SpectrumRollJamSuspect || !pred.IsAlert {
			t.Fatalf("rolljam got %s (%.2f, alert=%v), want RollJam_Suspect alert", pred.Label, pred.Confidence, pred.IsAlert)
		}
	})
}

// TestPulseNetAndSpectrumZeroAlloc enforces strict 0 heap allocations on both hot paths.
func TestPulseNetAndSpectrumZeroAlloc(t *testing.T) {
	mPulse, _ := LoadDefaultPulseNet("")
	mSpec, _ := LoadDefaultSpectrumNet("")
	if mPulse == nil || mSpec == nil {
		t.Fatal("embedded models unavailable")
	}

	var pws PulseNetWorkspace
	sample := make([]int32, 128)
	for i := range sample {
		if i%2 == 0 {
			sample[i] = 420
		} else {
			sample[i] = -1260
		}
	}
	if allocs := testing.AllocsPerRun(50, func() {
		_ = mPulse.Infer(&pws, sample, 433_920_000, "am650", 24.0)
	}); allocs != 0 {
		t.Fatalf("PulseNet.Infer allocated %.1f times/op, want 0", allocs)
	}

	var sws SpectrumWorkspace
	var wf [SpectrumRows][SpectrumCols]int32
	var floor [SpectrumCols]float32
	if allocs := testing.AllocsPerRun(50, func() {
		_ = mSpec.InferSpectrum(&sws, &wf, &floor)
	}); allocs != 0 {
		t.Fatalf("SpectrumNet.InferSpectrum allocated %.1f times/op, want 0", allocs)
	}
}

func FuzzLoadCZML(f *testing.F) {
	f.Add(embeddedPulseNetRF)
	f.Add([]byte("CZML\x01\x00\x01\x00"))
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, data []byte) {
		_, _ = LoadCZMLBytes(data, DomainRFPulse)
	})
}

func FuzzPulseNetInfer(f *testing.F) {
	m, _ := LoadDefaultPulseNet("")
	if m == nil {
		f.Skip("embedded PulseNet-RF missing")
	}
	f.Add([]byte{1, 2, 3, 4, 255, 128, 64, 0}, uint32(433_920_000), "AM650")
	f.Fuzz(func(t *testing.T, b []byte, freq uint32, preset string) {
		raw := make([]int32, 0, len(b))
		for i, v := range b {
			val := int32(v) * 40
			if i%2 == 1 {
				val = -val
			}
			raw = append(raw, val)
		}
		var ws PulseNetWorkspace
		res := m.Infer(&ws, raw, freq, preset, 20.0)
		if math.IsNaN(float64(res.ProtocolConfidence)) {
			t.Fatalf("NaN confidence for len=%d", len(raw))
		}
	})
}

func BenchmarkPulseNetInfer(b *testing.B) {
	m, _ := LoadDefaultPulseNet("")
	if m == nil {
		b.Fatal("embedded PulseNet-RF unavailable")
	}
	var ws PulseNetWorkspace
	sample := make([]int32, 200)
	for i := range sample {
		if i%2 == 0 {
			sample[i] = 420
		} else {
			sample[i] = -1260
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = m.Infer(&ws, sample, 433_920_000, "AM650", 24.0)
	}
}

func BenchmarkSpectrumNetInfer(b *testing.B) {
	m, _ := LoadDefaultSpectrumNet("")
	if m == nil {
		b.Fatal("embedded SpectrumNet unavailable")
	}
	var ws SpectrumWorkspace
	var wf [SpectrumRows][SpectrumCols]int32
	var floor [SpectrumCols]float32
	for r := range SpectrumRows {
		for c := range SpectrumCols {
			wf[r][c] = -102
			floor[c] = -104
		}
	}
	b.ReportAllocs()
	for b.Loop() {
		_ = m.InferSpectrum(&ws, &wf, &floor)
	}
}
