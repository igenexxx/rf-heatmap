package mcpserver

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"rf-heatmap/internal/rfml"
	"rf-heatmap/internal/subfile"
)

const (
	defaultAnalysisFreqHz = 433_920_000
	defaultAnalysisSNRDB  = 25.0
	defaultFPMinSim       = float32(0.85)
	defaultFPTopK         = 3
)

func resolveFreqAndSNR(freqHz uint32, snr *float64) (uint32, float64) {
	if freqHz == 0 {
		freqHz = defaultAnalysisFreqHz
	}
	snrDB := defaultAnalysisSNRDB
	if snr != nil {
		snrDB = *snr
	}
	return freqHz, snrDB
}

func formatDecodedMap(decoded subfile.DecodedKey, ok bool) map[string]any {
	if !ok {
		return map[string]any{"decoded": false}
	}
	return map[string]any{
		"decoded":  true,
		"protocol": decoded.Protocol,
		"bits":     decoded.Bits,
		"key_hex":  decoded.KeyHex,
		"te":       decoded.TE,
		"denoised": decoded.Denoised,
	}
}

func (s *Server) toolDecodeOOK(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Pulses []int32 `json:"pulses"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if len(args.Pulses) == 0 {
		return toolError("pulses must be a non-empty array of signed microsecond durations")
	}
	decoded, ok := subfile.DecodeOOK(args.Pulses)
	res := formatDecodedMap(decoded, ok)
	if !ok {
		res["pulse_count"] = len(args.Pulses)
	}
	return jsonResult(res)
}

func (s *Server) toolAnalyzePulses(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Pulses      []int32  `json:"pulses"`
		MinGlitchUs int32    `json:"min_glitch_us"`
		FreqHz      uint32   `json:"freq_hz"`
		SnrDB       *float64 `json:"snr_db"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if len(args.Pulses) == 0 {
		return toolError("pulses must be a non-empty array of signed microsecond durations")
	}
	minGlitch := args.MinGlitchUs
	if minGlitch <= 0 {
		minGlitch = rfml.DefaultMinGlitchUs
	}
	freqHz, snrDB := resolveFreqAndSNR(args.FreqHz, args.SnrDB)
	clusters := rfml.ClusterTimings(args.Pulses, minGlitch)
	quantized := rfml.QuantizePulses(args.Pulses, clusters, minGlitch)
	pred, _, _ := rfml.ClassifyPulses(args.Pulses, freqHz, snrDB)
	decoded, ok := subfile.DecodeOOK(args.Pulses)

	return jsonResult(map[string]any{
		"input_pulses":    len(args.Pulses),
		"quantized_count": len(quantized),
		"clusters":        clusters,
		"prediction":      pred,
		"decode":          formatDecodedMap(decoded, ok),
	})
}

func (s *Server) toolClassifySignal(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Pulses []int32  `json:"pulses"`
		FreqHz uint32   `json:"freq_hz"`
		SnrDB  *float64 `json:"snr_db"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if len(args.Pulses) == 0 {
		return toolError("pulses must be a non-empty array of signed microsecond durations")
	}
	freqHz, snrDB := resolveFreqAndSNR(args.FreqHz, args.SnrDB)
	pred, feat, clusters := rfml.ClassifyPulses(args.Pulses, freqHz, snrDB)
	return jsonResult(map[string]any{
		"prediction": pred,
		"features":   feat,
		"clusters":   clusters,
	})
}

func (s *Server) toolMatchFingerprint(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		Pulses        []int32  `json:"pulses"`
		FreqHz        uint32   `json:"freq_hz"`
		Preset        string   `json:"preset"`
		MinSimilarity *float32 `json:"min_similarity"`
		TopK          int      `json:"top_k"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if len(args.Pulses) == 0 {
		return toolError("pulses must be a non-empty array of signed microsecond durations")
	}
	freqHz, _ := resolveFreqAndSNR(args.FreqHz, nil)
	minSim := defaultFPMinSim
	if args.MinSimilarity != nil {
		minSim = *args.MinSimilarity
	}
	topK := args.TopK
	if topK <= 0 {
		topK = defaultFPTopK
	}

	idx := rfml.NewFingerprintIndex(s.loadFingerprintEntries())
	query := rfml.ComputeEmbeddingFromRaw(args.Pulses, freqHz, args.Preset)
	top := idx.TopK(query, freqHz, topK, minSim)

	res := map[string]any{
		"matched":         len(top) > 0,
		"indexed_count":   idx.Len(),
		"top_matches":     top,
		"query_embedding": query,
	}
	if len(top) > 0 {
		res["best_match"] = top[0]
	}
	return jsonResult(res)
}

func (s *Server) loadFingerprintEntries() []rfml.FingerprintEntry {
	entries := make([]rfml.FingerprintEntry, 0)
	for _, d := range s.savedSearchDirs() {
		dirEntries, err := os.ReadDir(d.dir)
		if err != nil {
			continue
		}
		for _, e := range dirEntries {
			if entry, ok := loadFingerprintEntry(d.dir, e); ok {
				entries = append(entries, entry)
			}
		}
	}
	return entries
}

func loadFingerprintEntry(dir string, e os.DirEntry) (rfml.FingerprintEntry, bool) {
	if e.IsDir() || !strings.HasSuffix(strings.ToLower(e.Name()), ".sub") {
		return rfml.FingerprintEntry{}, false
	}
	full := filepath.Join(dir, e.Name())
	raw, err := os.ReadFile(full)
	if err != nil {
		return rfml.FingerprintEntry{}, false
	}
	sf, err := subfile.Parse(string(raw))
	if err != nil {
		return rfml.FingerprintEntry{}, false
	}
	var emb rfml.Embedding
	if len(sf.RawData) > 0 {
		emb = rfml.ComputeEmbeddingFromRaw(sf.RawData, sf.FreqHz, sf.Preset)
	} else {
		emb = rfml.ComputeEmbeddingFromKey(sf.FreqHz, sf.Preset, sf.Protocol, sf.Bit, sf.TE)
	}
	return rfml.FingerprintEntry{
		Name: e.Name(), Path: full, FreqHz: sf.FreqHz, Protocol: sf.Protocol, Embedding: emb,
	}, true
}
