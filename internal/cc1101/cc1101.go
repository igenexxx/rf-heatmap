package cc1101

import (
	"fmt"
	"math"
)

const (
	// DefaultCrystalHz is the standard 26 MHz CC1101 reference crystal on Cap CC1101.
	DefaultCrystalHz uint32 = 26_000_000

	// SPI header bits.
	spiRead  uint8 = 0x80
	spiBurst uint8 = 0x40

	// Configuration registers.
	regIOCFG2   uint8 = 0x00
	regIOCFG0   uint8 = 0x02
	regPKTCTRL0 uint8 = 0x08
	regFSCTRL1  uint8 = 0x0B
	regFREQ2    uint8 = 0x0D
	regFREQ1    uint8 = 0x0E
	regFREQ0    uint8 = 0x0F
	regMDMCFG4  uint8 = 0x10
	regMDMCFG3  uint8 = 0x11
	regMDMCFG2  uint8 = 0x12
	regDEVIATN  uint8 = 0x15
	regMCSM0    uint8 = 0x18
	regFOCCFG   uint8 = 0x19
	regAGCCTRL2 uint8 = 0x1B
	regAGCCTRL1 uint8 = 0x1C
	regAGCCTRL0 uint8 = 0x1D
	regFREND1   uint8 = 0x21
	regFREND0   uint8 = 0x22
	regPATABLE  uint8 = 0x3E

	// Status registers (read with burst bit set).
	statusPartnum   uint8 = 0x30
	statusVersion   uint8 = 0x31
	statusRSSI      uint8 = 0x34
	statusMarcstate uint8 = 0x35

	marcstateTX uint8 = 0x13

	// Command strobes.
	strobeReset uint8 = 0x30
	strobeRX    uint8 = 0x34
	strobeTX    uint8 = 0x35
	strobeIdle  uint8 = 0x36

	gdoHighZ        uint8 = 0x2E
	gdoAsyncSerial  uint8 = 0x0D
	gdo2ConstantLow uint8 = 0x2F
	gdo2ConstantHi  uint8 = 0x6F
	txPowerMax      uint8 = 0xC0
	rssiOffsetDB    int32 = 74
)

// Preset represents a Flipper-compatible CC1101 asynchronous modem configuration.
type Preset int

const (
	PresetAM650 Preset = iota
	PresetAM270
	PresetFM238
	PresetFM476
)

// PresetSpec holds register values and Flipper .sub naming for a modem preset.
type PresetSpec struct {
	Name        string
	SubFileName string
	MDMCFG4     uint8
	MDMCFG3     uint8
	MDMCFG2     uint8
	DEVIATN     uint8
	FOCCFG      uint8
	FREND1      uint8
	FREND0      uint8
}

var presets = []PresetSpec{
	{
		Name: "AM650", SubFileName: "FuriHalSubGhzPresetOok650Async",
		MDMCFG4: 0x17, MDMCFG3: 0x32, MDMCFG2: 0x30, DEVIATN: 0x47, FOCCFG: 0x18, FREND1: 0xB6, FREND0: 0x11,
	},
	{
		Name: "AM270", SubFileName: "FuriHalSubGhzPresetOok270Async",
		MDMCFG4: 0x67, MDMCFG3: 0x32, MDMCFG2: 0x30, DEVIATN: 0x47, FOCCFG: 0x18, FREND1: 0xB6, FREND0: 0x11,
	},
	{
		Name: "FM238", SubFileName: "FuriHalSubGhzPreset2FSKDev238Async",
		MDMCFG4: 0x67, MDMCFG3: 0x83, MDMCFG2: 0x04, DEVIATN: 0x04, FOCCFG: 0x16, FREND1: 0x56, FREND0: 0x10,
	},
	{
		Name: "FM476", SubFileName: "FuriHalSubGhzPreset2FSKDev476Async",
		MDMCFG4: 0x67, MDMCFG3: 0x83, MDMCFG2: 0x04, DEVIATN: 0x47, FOCCFG: 0x16, FREND1: 0x56, FREND0: 0x10,
	},
}

// GetPresetSpec returns the modem specification for a preset (defaults to AM650 if out of range).
func GetPresetSpec(p Preset) PresetSpec {
	if int(p) < 0 || int(p) >= len(presets) {
		return presets[0]
	}
	return presets[p]
}

// PresetCount returns the number of supported presets.
func PresetCount() int { return len(presets) }

// PresetFromSubName maps a Flipper .sub Preset string to a Preset.
func PresetFromSubName(name string) (Preset, bool) {
	for i, spec := range presets {
		if spec.SubFileName == name || spec.Name == name {
			return Preset(i), true
		}
	}
	return PresetAM650, false
}

// FreqRegisters computes FREQ2, FREQ1, FREQ0 for a target carrier frequency and crystal.
// Formula: f_carrier = (f_xosc / 2^16) * FREQ
func FreqRegisters(freqHz uint32, crystalHz uint32) (f2, f1, f0 uint8, err error) {
	if crystalHz == 0 {
		return 0, 0, 0, fmt.Errorf("cc1101: crystal frequency must be non-zero")
	}
	if freqHz < 280_000_000 || freqHz > 950_000_000 {
		return 0, 0, 0, fmt.Errorf("cc1101: frequency %d Hz out of 280-950 MHz range", freqHz)
	}
	val := uint64(math.Round(float64(freqHz) * 65536.0 / float64(crystalHz)))
	return uint8((val >> 16) & 0xFF), uint8((val >> 8) & 0xFF), uint8(val & 0xFF), nil
}

// RSSIToDBm converts a raw CC1101 RSSI status byte (2's complement) to dBm.
func RSSIToDBm(raw uint8) int32 {
	var signed int32
	if raw >= 128 {
		signed = int32(raw) - 256
	} else {
		signed = int32(raw)
	}
	return (signed / 2) - rssiOffsetDB
}

// AntennaBand describes the Cap CC1101 dual SP3T switch state for a frequency.
type AntennaBand struct {
	RFSwitchHigh bool  // GPIO 14 (RF_SW0)
	GDO2Register uint8 // IOCFG2 (0x2F = low, 0x6F = high)
}

// SelectAntennaBand returns the Cap CC1101 RF_SW0 and GDO2 states for a given frequency.
// Band split: < 374 MHz (low), 374..650.5 MHz (mid), >= 650.5 MHz (high).
func SelectAntennaBand(freqHz uint32) AntennaBand {
	lowBand := freqHz < 374_000_000
	midBand := !lowBand && freqHz < 650_500_000
	gdo2 := gdo2ConstantHi
	if midBand {
		gdo2 = gdo2ConstantLow
	}
	return AntennaBand{
		RFSwitchHigh: !lowBand,
		GDO2Register: gdo2,
	}
}
