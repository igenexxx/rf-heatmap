package sensors

import (
	"math"
	"time"
)

const (
	kPaToPSI         = 0.1450377377
	maxSensorPulses  = 65536
	minSensorPulses  = 32
)

// SensorKind distinguishes automotive TPMS from environmental Weather stations.
type SensorKind string

const (
	KindTPMS    SensorKind = "tpms"
	KindWeather SensorKind = "weather"
)

// ReadingMeta holds reception metadata embedded in Reading.
type ReadingMeta struct {
	Flags     uint8     `json:"flags,omitempty"`
	RSSIDBm   int32     `json:"rssi_dbm,omitempty"`
	SeenAt    time.Time `json:"seen_at,omitempty"`
	SeenCount int       `json:"seen_count,omitempty"`
}

// Reading is a verified telemetry frame from a Sub-GHz TPMS or Weather sensor.
type Reading struct {
	Kind        SensorKind `json:"kind"`
	Protocol    string     `json:"protocol"`
	SensorID    string     `json:"sensor_id"`
	FreqHz      uint32     `json:"freq_hz"`
	Channel     int        `json:"channel,omitempty"`
	PressureKPa float64    `json:"pressure_kpa,omitempty"`
	PressurePSI float64    `json:"pressure_psi,omitempty"`
	TempC       float64    `json:"temp_c"`
	HasHumidity bool       `json:"has_humidity,omitempty"`
	HumidityPct int        `json:"humidity_pct,omitempty"`
	BatteryOK   bool       `json:"battery_ok"`
	ReadingMeta
}

// Decoder is a Strategy interface implemented by each sensor protocol.
type Decoder interface {
	Name() string
	Decode(pulses []int32, freqHz uint32) (Reading, bool)
}

var defaultDecoders = []Decoder{
	schraderTPMSDecoder{},
	toyotaTPMSDecoder{},
	oregonDecoder{},
	laCrosseDecoder{},
	acuriteDecoder{},
}

// Decode attempts all registered TPMS and Weather decoders against pulses.
func Decode(pulses []int32, freqHz uint32, preset string) (Reading, bool) {
	if len(pulses) < minSensorPulses || len(pulses) > maxSensorPulses {
		return Reading{}, false
	}
	_ = preset
	for _, dec := range defaultDecoders {
		if r, ok := dec.Decode(pulses, freqHz); ok {
			return r, true
		}
	}
	return Reading{}, false
}

// CelsiusToFahrenheit converts degrees Celsius to Fahrenheit rounded to 0.1.
func CelsiusToFahrenheit(c float64) float64 {
	return round1(c*9.0/5.0 + 32.0)
}

// KPaToPSI converts kilopascals to PSI rounded to 0.1.
func KPaToPSI(kpa float64) float64 {
	return round1(kpa * kPaToPSI)
}

func round1(v float64) float64 {
	return math.Round(v*10) / 10
}

func absInt32(v int32) int32 {
	if v < 0 {
		if v == math.MinInt32 {
			return math.MaxInt32
		}
		return -v
	}
	return v
}

func withinTol(val, target int32, tol float64) bool {
	if val <= 0 {
		return false
	}
	return float64(val) >= float64(target)*(1.0-tol) && float64(val) <= float64(target)*(1.0+tol)
}

func crc8(data []byte, poly, initVal uint8) uint8 {
	crc := initVal
	for _, b := range data {
		crc ^= b
		for range 8 {
			if crc&0x80 != 0 {
				crc = (crc << 1) ^ poly
			} else {
				crc <<= 1
			}
		}
	}
	return crc
}
