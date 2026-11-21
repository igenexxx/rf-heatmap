package cc1101

// AGCConfig holds CC1101 AGCCTRL2..0 register values for a modem preset.
type AGCConfig struct {
	CTRL2 uint8
	CTRL1 uint8
	CTRL0 uint8
}

// DefaultOOKAGC returns the standard AGC configuration for ASK/OOK presets.
func DefaultOOKAGC() AGCConfig {
	return AGCConfig{CTRL2: 0x07, CTRL1: 0x00, CTRL0: 0x91}
}

// DefaultFSKAGC returns the tuned AGC configuration for 2-FSK/GFSK presets.
func DefaultFSKAGC() AGCConfig {
	return AGCConfig{CTRL2: 0x43, CTRL1: 0x40, CTRL0: 0x91}
}

// IsFSKModulation reports whether MDMCFG2 selects 2-FSK (000) or GFSK (001).
func IsFSKModulation(mdmcfg2 uint8) bool {
	mod := mdmcfg2 & 0x70
	return mod == 0x00 || mod == 0x10
}

// PATableForPreset builds the 8-byte CC1101 PATABLE for ASK/OOK vs 2-FSK/GFSK.
// ASK/OOK keys between PATABLE[0]=0x00 (off) and PATABLE[1]=txPowerMax (on).
// 2-FSK and GFSK use PATABLE[0]=txPowerMax continuously.
func PATableForPreset(spec PresetSpec) [8]uint8 {
	pa := [8]uint8{0x00, txPowerMax}
	if IsFSKModulation(spec.MDMCFG2) {
		pa[0] = txPowerMax
	}
	return pa
}

// GFSK999PresetSpec returns the 9.997 kbps GFSK async preset tuned for TPMS and FSK sensors.
func GFSK999PresetSpec() PresetSpec {
	return PresetSpec{
		Name:        "GFSK99",
		SubFileName: "FuriHalSubGhzPresetGFSK9_99KbAsync",
		MDMCFG4:     0xC8, // ~101 kHz RX filter BW, DRATE_E = 8
		MDMCFG3:     0x93, // ~9.997 kbps data rate
		MDMCFG2:     0x10, // GFSK modulation, no sync/preamble
		DEVIATN:     0x34, // ~19.04 kHz frequency deviation
		FOCCFG:      0x16,
		FREND1:      0x56,
		FREND0:      0x10,
		FSCTRL1:     0x06,
		AGC:         DefaultFSKAGC(),
	}
}
