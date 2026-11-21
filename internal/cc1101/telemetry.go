package cc1101

import (
	"fmt"
	"math"
)

const (
	// Status registers for frequency offset and link quality.
	statusFreqEst uint8 = 0x32
	statusLQI     uint8 = 0x33

	// Wake-On-Radio (WOR) configuration registers and strobes.
	regMCSM2      uint8 = 0x16
	regWOREVT1    uint8 = 0x1E
	regWOREVT0    uint8 = 0x1F
	regWORCTRL    uint8 = 0x20
	strobeSWOR    uint8 = 0x38
	strobeSWORRST uint8 = 0x3C

	// WOR timing bounds (ms).
	MinWORIntervalMs uint16 = 10
	MaxWORIntervalMs uint16 = 60000

	worMCSM2Duty  uint8 = 0x01 // RX_TIME=1 (~1.95% duty cycle)
	worCtrlEvent1 uint8 = 0x78 // EVENT1=7 (1.333ms), RC_CAL=1
)

// AllowedTXPowerDBm lists the supported CC1101 TX power steps in dBm.
var AllowedTXPowerDBm = [6]int{-30, -20, -10, 0, 5, 10}

// patableBands maps band index (0=315, 1=433, 2=868, 3=915 MHz) x power step -> PATABLE byte (TI DN013).
var patableBands = [4][6]uint8{
	{0x12, 0x0D, 0x1C, 0x51, 0x85, 0xC2}, // 315 MHz
	{0x12, 0x0E, 0x1D, 0x60, 0x84, 0xC0}, // 433 MHz
	{0x03, 0x0F, 0x27, 0x50, 0x81, 0xC2}, // 868 MHz
	{0x03, 0x0E, 0x27, 0x8E, 0x8D, 0xC0}, // 915 MHz
}

// ClampTXPowerDBm clamps an arbitrary dBm value to the nearest supported step in AllowedTXPowerDBm.
func ClampTXPowerDBm(dbm int) int {
	best := AllowedTXPowerDBm[0]
	bestDiff := absInt(dbm - best)
	for _, lvl := range AllowedTXPowerDBm[1:] {
		if d := absInt(dbm - lvl); d < bestDiff {
			best = lvl
			bestDiff = d
		}
	}
	return best
}

func txPowerStepIndex(dbm int) (int, int) {
	clamped := ClampTXPowerDBm(dbm)
	for i, lvl := range AllowedTXPowerDBm {
		if lvl == clamped {
			return i, clamped
		}
	}
	return len(AllowedTXPowerDBm) - 1, 10
}

func patableBandIndex(freqHz uint32) int {
	switch {
	case freqHz < 374_000_000:
		return 0
	case freqHz < 650_500_000:
		return 1
	case freqHz < 890_000_000:
		return 2
	default:
		return 3
	}
}

// PATableByte returns the datasheet PATABLE register byte and clamped dBm level for freqHz.
func PATableByte(freqHz uint32, dbm int) (uint8, int, error) {
	if freqHz < 280_000_000 || freqHz > 950_000_000 {
		return 0, 0, fmt.Errorf("cc1101: frequency %d Hz out of 280-950 MHz range", freqHz)
	}
	idx, clamped := txPowerStepIndex(dbm)
	band := patableBandIndex(freqHz)
	return patableBands[band][idx], clamped, nil
}

// FreqOffsetHz converts a raw FREQEST (0x32) status byte (two's complement) to Hz.
// Formula: delta_f = (f_xosc / 2^14) * int8(raw).
func FreqOffsetHz(raw uint8, crystalHz uint32) int32 {
	if crystalHz == 0 {
		crystalHz = DefaultCrystalHz
	}
	signed := int8(raw)
	return int32(math.Round(float64(signed) * float64(crystalHz) / 16384.0))
}

// DecodeLQI splits a raw LQI (0x33) status byte into 7-bit LQI (0..127) and CRC_OK (bit 7).
func DecodeLQI(raw uint8) (lqi uint8, crcOK bool) {
	return raw & 0x7F, (raw & 0x80) != 0
}

// WORRegisters computes WOREVT1, WOREVT0, and WORCTRL for a target wake interval in milliseconds.
// Formula: t_event0 = (750 / f_xosc) * EVENT0 * 2^(5 * WOR_RES).
func WORRegisters(intervalMs uint16, crystalHz uint32) (evt1, evt0, worCtrl uint8, err error) {
	if intervalMs < MinWORIntervalMs || intervalMs > MaxWORIntervalMs {
		return 0, 0, 0, fmt.Errorf("cc1101: WOR interval %d ms out of [%d..%d] range", intervalMs, MinWORIntervalMs, MaxWORIntervalMs)
	}
	if crystalHz == 0 {
		crystalHz = DefaultCrystalHz
	}
	var worRes uint8
	scale := 1.0
	if intervalMs > 1890 {
		worRes = 1
		scale = 32.0
	}
	rawEvt := math.Round(float64(intervalMs) * float64(crystalHz) / (750_000.0 * scale))
	ev := uint16(min(65535.0, max(1.0, rawEvt)))
	return uint8(ev >> 8), uint8(ev & 0xFF), worCtrlEvent1 | worRes, nil
}

// ReadFreqOffsetHz reads FREQEST (0x32) and returns the estimated carrier offset in Hz.
func (r *Radio) ReadFreqOffsetHz() (int32, error) {
	raw, err := r.readStatus(statusFreqEst)
	if err != nil {
		return 0, err
	}
	return FreqOffsetHz(raw, r.crystalHz), nil
}

// ReadLQI reads LQI (0x33) and returns the 7-bit Link Quality Indicator and CRC_OK flag.
func (r *Radio) ReadLQI() (uint8, bool, error) {
	raw, err := r.readStatus(statusLQI)
	if err != nil {
		return 0, false, err
	}
	lqi, crcOK := DecodeLQI(raw)
	return lqi, crcOK, nil
}

// SetTXPowerDBm updates the CC1101 PATABLE for the given carrier band and desired TX power in dBm.
func (r *Radio) SetTXPowerDBm(freqHz uint32, dbm int) error {
	paByte, _, err := PATableByte(freqHz, dbm)
	if err != nil {
		return err
	}
	spec := GetPresetSpec(r.preset)
	pa := [8]uint8{0x00, paByte}
	if IsFSKModulation(spec.MDMCFG2) {
		pa[0] = paByte
	}
	return r.writeBurst(regPATABLE, pa[:])
}

// ConfigureWOR programs MCSM2, WOREVT1/0, and WORCTRL and resets the WOR timer.
func (r *Radio) ConfigureWOR(intervalMs uint16) error {
	evt1, evt0, worCtrl, err := WORRegisters(intervalMs, r.crystalHz)
	if err != nil {
		return err
	}
	if err := r.strobe(strobeIdle); err != nil {
		return err
	}
	regs := [][2]uint8{
		{regMCSM2, worMCSM2Duty},
		{regWOREVT1, evt1},
		{regWOREVT0, evt0},
		{regWORCTRL, worCtrl},
	}
	for _, rv := range regs {
		if err := r.writeReg(rv[0], rv[1]); err != nil {
			return err
		}
	}
	return r.strobe(strobeSWORRST)
}

// EnterWOR strobes SWOR (0x38) to start autonomous Wake-On-Radio polling.
func (r *Radio) EnterWOR() error {
	return r.strobe(strobeSWOR)
}

func absInt(v int) int {
	if v < 0 {
		return -v
	}
	return v
}
