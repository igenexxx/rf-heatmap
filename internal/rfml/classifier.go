package rfml

import "math"

// SignalClass identifies the predicted Sub-GHz RF signal category.
type SignalClass int

const (
	ClassFixedOOK SignalClass = iota
	ClassRollingFob
	ClassSensor
	ClassJammer
	ClassNoise
)

// String returns the human-readable English label for SignalClass.
func (c SignalClass) String() string {
	switch c {
	case ClassFixedOOK:
		return "Fixed OOK"
	case ClassRollingFob:
		return "Rolling/KeyFob"
	case ClassSensor:
		return "Sensor/Weather"
	case ClassJammer:
		return "CW/Jammer"
	case ClassNoise:
		return "Noise"
	default:
		return "Unknown"
	}
}

// Prediction holds the output of the on-device RF signal classifier (PulseNet INT8 + coarse fallback).
type Prediction struct {
	Class              SignalClass           `json:"class_id"`
	Label              string                `json:"label"`
	Confidence         float32               `json:"confidence"`
	Probs              [mlpOutputDim]float32 `json:"probs"`
	IsNoise            bool                  `json:"is_noise"`
	FineClassID        uint16                `json:"fine_class_id,omitempty"`
	ProtocolLabel      string                `json:"protocol_label,omitempty"`
	ProtocolConfidence float32               `json:"protocol_confidence,omitempty"`
	ModelVersion       uint32                `json:"model_version,omitempty"`
}

// Classify runs pure-Go zero-allocation MLP inference (12 -> 24 ReLU -> 5 Softmax).
// Fixed-size stack arrays allow Go 1.27's compiler to eliminate all bounds checks.
func Classify(feat FeatureVector) Prediction {
	x := sanitizeFeatures(feat)
	hidden := forwardHiddenLayer(&x)
	logits, maxLogit := forwardOutputLayer(&hidden)
	probs, bestIdx, bestProb := softmaxArgmax(&logits, maxLogit)

	cls := SignalClass(bestIdx)
	return Prediction{
		Class:      cls,
		Label:      cls.String(),
		Confidence: bestProb,
		Probs:      probs,
		IsNoise:    cls == ClassNoise && bestProb >= 0.85,
	}
}

func sanitizeFeatures(feat FeatureVector) [mlpInputDim]float32 {
	var x [mlpInputDim]float32
	for i := range mlpInputDim {
		v := feat[i]
		if math.IsNaN(float64(v)) || math.IsInf(float64(v), 0) {
			v = 0
		}
		x[i] = max(float32(0), min(float32(1), v))
	}
	return x
}

func forwardHiddenLayer(x *[mlpInputDim]float32) [mlpHiddenDim]float32 {
	var hidden [mlpHiddenDim]float32
	for j := range mlpHiddenDim {
		sum := mlpB1[j]
		row := &mlpW1[j]
		for i := range mlpInputDim {
			sum += row[i] * x[i]
		}
		if sum > 0 {
			hidden[j] = sum
		}
	}
	return hidden
}

func forwardOutputLayer(hidden *[mlpHiddenDim]float32) ([mlpOutputDim]float32, float32) {
	var logits [mlpOutputDim]float32
	maxLogit := float32(-math.MaxFloat32)
	for k := range mlpOutputDim {
		sum := mlpB2[k]
		row := &mlpW2[k]
		for j := range mlpHiddenDim {
			sum += row[j] * hidden[j]
		}
		logits[k] = sum
		maxLogit = max(maxLogit, sum)
	}
	return logits, maxLogit
}

func softmaxArgmax(logits *[mlpOutputDim]float32, maxLogit float32) ([mlpOutputDim]float32, int, float32) {
	var probs [mlpOutputDim]float32
	var expSum float64
	for k := range mlpOutputDim {
		e := math.Exp(float64(logits[k] - maxLogit))
		probs[k] = float32(e)
		expSum += e
	}
	invSum := float32(1.0 / expSum)
	bestIdx := 0
	var bestProb float32
	for k := range mlpOutputDim {
		probs[k] *= invSum
		if probs[k] > bestProb {
			bestProb = probs[k]
			bestIdx = k
		}
	}
	return probs, bestIdx, bestProb
}

// ClassifyPulses extracts features/clusters from RAW_Data and runs PulseNet INT8 inference
// (falling back to the legacy 12->24->5 MLP only when the pulse slice is empty or model unavailable).
func ClassifyPulses(raw []int32, freqHz uint32, snrDB float64) (Prediction, FeatureVector, PulseClusters) {
	return ClassifyPulsesWithPreset(raw, freqHz, "AM650", snrDB)
}

// ClassifyPulsesWithPreset runs PulseNet INT8 inference with explicit modulation preset conditioning.
func ClassifyPulsesWithPreset(raw []int32, freqHz uint32, preset string, snrDB float64) (Prediction, FeatureVector, PulseClusters) {
	feat, clusters := ExtractFeatures(raw, freqHz, snrDB)
	if len(raw) == 0 {
		return Classify(feat), feat, clusters
	}
	if m := defaultPulseNet(); m != nil {
		ws := pulseWS.Get().(*PulseNetWorkspace)
		res := m.Infer(ws, raw, freqHz, preset, snrDB)
		pulseWS.Put(ws)
		return Prediction{
			Class:              res.CoarseClass,
			Label:              res.CoarseClass.String(),
			Confidence:         res.CoarseConfidence,
			Probs:              res.CoarseProbs,
			IsNoise:            res.CoarseClass == ClassNoise && res.CoarseConfidence >= 0.75,
			FineClassID:        res.FineClassID,
			ProtocolLabel:      res.ProtocolLabel,
			ProtocolConfidence: res.ProtocolConfidence,
			ModelVersion:       res.ModelVersion,
		}, feat, clusters
	}
	return Classify(feat), feat, clusters
}
