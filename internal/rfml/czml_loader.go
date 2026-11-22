package rfml

import (
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"math"
	"os"
)

const (
	czmlHeaderSize      = 64
	czmlFormatVersion   = 1
	czmlMaxFileBytes    = 2 << 20 // 2 MiB hard security cap
	czmlMaxLayers       = 64
	czmlMaxFineClasses  = 64
	czmlMaxChannels     = 256
	czmlMaxKernelSize   = 7
	czmlMaxLabelBytes   = 32
	czmlLayerHeaderSize = 18

	// Flag bits in .czml v1 header.
	czmlFlagHasDenoiser  uint32 = 1 << 0
	czmlFlagHasCoarse    uint32 = 1 << 1
	czmlFlagHasEmbedding uint32 = 1 << 2

	// Serialized layer operation opcodes.
	opConv1D          uint8 = 1
	opDepthwiseConv1D uint8 = 2
	opPointwiseConv1D uint8 = 3
	opLinear          uint8 = 4
	opConv2D          uint8 = 5
	opDepthwiseConv2D uint8 = 6

	// Fused layer activation opcodes.
	actNone  uint8 = 0
	actReLU6 uint8 = 1
	actTanh  uint8 = 2
)

// CZMLDomain identifies the target model domain in a .czml v1 container.
type CZMLDomain uint16

const (
	DomainRFPulse     CZMLDomain = 1
	DomainIRPulse     CZMLDomain = 2
	DomainRFSpectrum  CZMLDomain = 3
	DomainNFCInduct   CZMLDomain = 4
	DomainNFCDump     CZMLDomain = 5
	DomainTimelineSeq CZMLDomain = 6
)

var (
	// ErrInvalidCZML indicates malformed, oversized, or out-of-bounds .czml binary data.
	ErrInvalidCZML = errors.New("rfml: invalid czml model")
	// ErrCZMLChecksum indicates a SHA-256 verification failure on .czml payload bytes.
	ErrCZMLChecksum = errors.New("rfml: czml sha256 checksum mismatch")
	// ErrCZMLDomainMismatch indicates the .czml file belongs to a different domain.
	ErrCZMLDomainMismatch = errors.New("rfml: czml domain mismatch")
)

// ModelInfo describes a loaded .czml model for UI/MCP inspection.
type ModelInfo struct {
	Domain      CZMLDomain `json:"domain"`
	Version     uint32     `json:"version"`
	SHA256Hex   string     `json:"sha256"`
	Source      string     `json:"source"`
	FineClasses []string   `json:"fine_classes"`
	HasDenoiser bool       `json:"has_denoiser"`
	ByteSize    int        `json:"byte_size"`
}

type czmlLayer struct {
	OpType       uint8
	Activation   uint8
	InChannels   uint16
	OutChannels  uint16
	KernelSize   uint8
	Stride       uint8
	Dilation     uint8
	InputScale   float32
	OutputScale  float32
	WeightScales []float32
	Biases       []float32
	Weights      []int8
}

// CZMLModel is a validated, immutable quantized neural model parsed from .czml v1 bytes.
type CZMLModel struct {
	Domain      CZMLDomain
	Version     uint32
	Flags       uint32
	TempFine    float32
	TempCoarse  float32
	Labels      []string
	Layers      []czmlLayer
	SHA256Hex   string
	ByteSize    int
}

// Info constructs a public ModelInfo summary tagged with source ("embedded" or "override").
func (m *CZMLModel) Info(source string) ModelInfo {
	if m == nil {
		return ModelInfo{Source: source}
	}
	labels := make([]string, len(m.Labels))
	copy(labels, m.Labels)
	return ModelInfo{
		Domain:      m.Domain,
		Version:     m.Version,
		SHA256Hex:   m.SHA256Hex,
		Source:      source,
		FineClasses: labels,
		HasDenoiser: (m.Flags & czmlFlagHasDenoiser) != 0,
		ByteSize:    m.ByteSize,
	}
}

// LoadCZMLFile reads a .czml file enforcing the 2 MiB security cap and validates it.
func LoadCZMLFile(path string, expected CZMLDomain) (*CZMLModel, error) {
	st, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if st.IsDir() || st.Size() < czmlHeaderSize || st.Size() > czmlMaxFileBytes {
		return nil, fmt.Errorf("%w: invalid file size %d", ErrInvalidCZML, st.Size())
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}
	return LoadCZMLBytes(data, expected)
}

// LoadCZMLBytes parses and SHA-256-verifies raw .czml v1 bytes for expected domain.
func LoadCZMLBytes(data []byte, expected CZMLDomain) (*CZMLModel, error) {
	if len(data) < czmlHeaderSize || len(data) > czmlMaxFileBytes {
		return nil, fmt.Errorf("%w: size %d out of range", ErrInvalidCZML, len(data))
	}
	hdr, err := parseCZMLHeader(data, expected)
	if err != nil {
		return nil, err
	}
	body := data[czmlHeaderSize:]
	if uint32(len(body)) != hdr.payloadLen {
		return nil, fmt.Errorf("%w: payload length mismatch", ErrInvalidCZML)
	}
	sum := sha256.Sum256(body)
	if sum != hdr.digest {
		return nil, ErrCZMLChecksum
	}

	labels, pos, err := parseLabelTable(body, int(hdr.numLabels))
	if err != nil {
		return nil, err
	}
	layers, pos, err := parseLayers(body, pos, int(hdr.numLayers))
	if err != nil {
		return nil, err
	}
	if pos != len(body) {
		return nil, fmt.Errorf("%w: %d trailing bytes", ErrInvalidCZML, len(body)-pos)
	}
	return &CZMLModel{
		Domain:     hdr.domain,
		Version:    hdr.modelVer,
		Flags:      hdr.flags,
		TempFine:   hdr.tempFine,
		TempCoarse: hdr.tempCoarse,
		Labels:     labels,
		Layers:     layers,
		SHA256Hex:  hex.EncodeToString(sum[:]),
		ByteSize:   len(data),
	}, nil
}

type czmlHeader struct {
	domain     CZMLDomain
	modelVer   uint32
	flags      uint32
	payloadLen uint32
	digest     [32]byte
	tempFine   float32
	tempCoarse float32
	numLabels  uint16
	numLayers  uint16
}

func parseCZMLHeader(data []byte, expected CZMLDomain) (czmlHeader, error) {
	var h czmlHeader
	if string(data[0:4]) != "CZML" {
		return h, fmt.Errorf("%w: bad magic", ErrInvalidCZML)
	}
	if binary.LittleEndian.Uint16(data[4:6]) != czmlFormatVersion {
		return h, fmt.Errorf("%w: unsupported format version", ErrInvalidCZML)
	}
	h.domain = CZMLDomain(binary.LittleEndian.Uint16(data[6:8]))
	if expected != 0 && h.domain != expected {
		return h, fmt.Errorf("%w: got %d want %d", ErrCZMLDomainMismatch, h.domain, expected)
	}
	h.modelVer = binary.LittleEndian.Uint32(data[8:12])
	h.flags = binary.LittleEndian.Uint32(data[12:16])
	h.payloadLen = binary.LittleEndian.Uint32(data[16:20])
	copy(h.digest[:], data[20:52])
	h.tempFine = math.Float32frombits(binary.LittleEndian.Uint32(data[52:56]))
	h.tempCoarse = math.Float32frombits(binary.LittleEndian.Uint32(data[56:60]))
	h.numLabels = binary.LittleEndian.Uint16(data[60:62])
	h.numLayers = binary.LittleEndian.Uint16(data[62:64])

	if !isValidPositiveFloat32(h.tempFine) || !isValidPositiveFloat32(h.tempCoarse) {
		return h, fmt.Errorf("%w: invalid softmax temperature", ErrInvalidCZML)
	}
	if h.numLabels < 2 || h.numLabels > czmlMaxFineClasses {
		return h, fmt.Errorf("%w: invalid label count %d", ErrInvalidCZML, h.numLabels)
	}
	if h.numLayers == 0 || h.numLayers > czmlMaxLayers {
		return h, fmt.Errorf("%w: invalid layer count %d", ErrInvalidCZML, h.numLayers)
	}
	return h, nil
}

func parseLabelTable(body []byte, count int) ([]string, int, error) {
	labels := make([]string, count)
	pos := 0
	for i := range count {
		if pos >= len(body) {
			return nil, 0, fmt.Errorf("%w: truncated label table", ErrInvalidCZML)
		}
		ln := int(body[pos])
		pos++
		if ln == 0 || ln > czmlMaxLabelBytes || pos+ln > len(body) {
			return nil, 0, fmt.Errorf("%w: invalid label length %d", ErrInvalidCZML, ln)
		}
		labels[i] = string(body[pos : pos+ln])
		pos += ln
	}
	return labels, pos, nil
}

func parseLayers(body []byte, pos, count int) ([]czmlLayer, int, error) {
	layers := make([]czmlLayer, count)
	for i := range count {
		l, nextPos, err := parseOneLayer(body, pos)
		if err != nil {
			return nil, 0, err
		}
		layers[i] = l
		pos = nextPos
	}
	return layers, pos, nil
}

func parseOneLayer(body []byte, pos int) (czmlLayer, int, error) {
	var l czmlLayer
	if pos+czmlLayerHeaderSize > len(body) {
		return l, 0, fmt.Errorf("%w: truncated layer header", ErrInvalidCZML)
	}
	l.OpType = body[pos]
	l.Activation = body[pos+1]
	l.InChannels = binary.LittleEndian.Uint16(body[pos+2 : pos+4])
	l.OutChannels = binary.LittleEndian.Uint16(body[pos+4 : pos+6])
	l.KernelSize = body[pos+6]
	l.Stride = body[pos+7]
	l.Dilation = body[pos+8]
	l.InputScale = math.Float32frombits(binary.LittleEndian.Uint32(body[pos+10 : pos+14]))
	l.OutputScale = math.Float32frombits(binary.LittleEndian.Uint32(body[pos+14 : pos+18]))
	pos += czmlLayerHeaderSize

	wCount, err := validateLayerDims(&l)
	if err != nil {
		return l, 0, err
	}
	outC := int(l.OutChannels)
	need := outC*4 + outC*4 + wCount
	if pos+need > len(body) {
		return l, 0, fmt.Errorf("%w: truncated layer tensors", ErrInvalidCZML)
	}
	l.WeightScales = make([]float32, outC)
	for o := range outC {
		v := math.Float32frombits(binary.LittleEndian.Uint32(body[pos : pos+4]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) || v < 0 {
			return l, 0, fmt.Errorf("%w: non-finite weight scale", ErrInvalidCZML)
		}
		l.WeightScales[o] = v
		pos += 4
	}
	l.Biases = make([]float32, outC)
	for o := range outC {
		v := math.Float32frombits(binary.LittleEndian.Uint32(body[pos : pos+4]))
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			return l, 0, fmt.Errorf("%w: non-finite bias", ErrInvalidCZML)
		}
		l.Biases[o] = v
		pos += 4
	}
	l.Weights = make([]int8, wCount)
	for w := range wCount {
		l.Weights[w] = int8(body[pos+w])
	}
	pos += wCount
	return l, pos, nil
}

func validateLayerDims(l *czmlLayer) (int, error) {
	if l.InChannels == 0 || l.OutChannels == 0 || l.InChannels > czmlMaxChannels || l.OutChannels > czmlMaxChannels {
		return 0, fmt.Errorf("%w: channels out of bounds (%d->%d)", ErrInvalidCZML, l.InChannels, l.OutChannels)
	}
	if l.KernelSize == 0 || l.KernelSize > czmlMaxKernelSize || l.Stride == 0 || l.Dilation == 0 {
		return 0, fmt.Errorf("%w: invalid kernel/stride/dilation", ErrInvalidCZML)
	}
	inC, outC, k := int(l.InChannels), int(l.OutChannels), int(l.KernelSize)
	switch l.OpType {
	case opDepthwiseConv1D:
		if inC != outC {
			return 0, fmt.Errorf("%w: depthwise channel mismatch", ErrInvalidCZML)
		}
		return outC * k, nil
	case opConv1D, opPointwiseConv1D, opLinear:
		return outC * inC * k, nil
	case opDepthwiseConv2D:
		if inC != outC {
			return 0, fmt.Errorf("%w: depthwise2d channel mismatch", ErrInvalidCZML)
		}
		return outC * k * k, nil
	case opConv2D:
		return outC * inC * k * k, nil
	default:
		return 0, fmt.Errorf("%w: unsupported op %d", ErrInvalidCZML, l.OpType)
	}
}

func isValidPositiveFloat32(v float32) bool {
	return !math.IsNaN(float64(v)) && !math.IsInf(float64(v), 0) && v > 0
}
