package cc1101

import (
	"context"
	"errors"
	"fmt"
	"time"
)

// Transport abstracts SPI + GPIO control so the radio driver is testable without hardware.
type Transport interface {
	Transfer(tx []byte) ([]byte, error)
	SetRFSwitch(high bool) error
	TransmitPulses(ctx context.Context, pulses []int32) error
	Close() error
}

// Radio controls a TI CC1101 transceiver.
type Radio struct {
	tr        Transport
	crystalHz uint32
	partNum   uint8
	version   uint8
	preset    Preset
	dataOut   bool
}

// ChipInfo holds CC1101 hardware identification bytes.
type ChipInfo struct {
	PartNumber uint8
	Version    uint8
}

// Open initializes a CC1101 over the given Transport, resets it, and verifies its identity.
func Open(tr Transport, crystalHz uint32) (*Radio, error) {
	if tr == nil {
		return nil, errors.New("cc1101: nil transport")
	}
	if crystalHz == 0 {
		crystalHz = DefaultCrystalHz
	}
	r := &Radio{tr: tr, crystalHz: crystalHz, preset: PresetAM650}
	if err := r.resetAndIdentify(); err != nil {
		return nil, err
	}
	if err := r.ApplyPreset(PresetAM650); err != nil {
		return nil, err
	}
	return r, nil
}

// Info returns the chip's PARTNUM and VERSION registers.
func (r *Radio) Info() ChipInfo {
	return ChipInfo{PartNumber: r.partNum, Version: r.version}
}

// Close returns the radio to IDLE, tristates GDO0, and closes the underlying transport.
func (r *Radio) Close() error {
	_ = r.SetDataOutput(false)
	_ = r.Idle()
	return r.tr.Close()
}

func (r *Radio) strobe(cmd uint8) error {
	_, err := r.tr.Transfer([]byte{cmd})
	return err
}

func (r *Radio) writeReg(addr, val uint8) error {
	_, err := r.tr.Transfer([]byte{addr, val})
	return err
}

func (r *Radio) writeBurst(addr uint8, vals []uint8) error {
	buf := make([]uint8, 1+len(vals))
	buf[0] = addr | spiBurst
	copy(buf[1:], vals)
	_, err := r.tr.Transfer(buf)
	return err
}

func (r *Radio) readStatus(addr uint8) (uint8, error) {
	rx, err := r.tr.Transfer([]byte{addr | spiRead | spiBurst, 0x00})
	if err != nil {
		return 0, err
	}
	if len(rx) < 2 {
		return 0, errors.New("cc1101: short SPI status read")
	}
	return rx[1], nil
}

func (r *Radio) resetAndIdentify() error {
	if err := r.strobe(strobeReset); err != nil {
		return fmt.Errorf("cc1101: reset strobe: %w", err)
	}
	for range 5 {
		time.Sleep(15 * time.Millisecond)
		p, err1 := r.readStatus(statusPartnum)
		v, err2 := r.readStatus(statusVersion)
		if err1 != nil || err2 != nil {
			continue
		}
		if v != 0x00 && v != 0xFF {
			r.partNum = p
			r.version = v
			return nil
		}
	}
	return fmt.Errorf("cc1101: chip not detected (part=0x%02X ver=0x%02X)", r.partNum, r.version)
}

// ApplyPreset loads a Flipper-compatible asynchronous modem preset and leaves the chip IDLE.
func (r *Radio) ApplyPreset(p Preset) error {
	spec := GetPresetSpec(p)
	r.preset = p
	gdo0 := gdoHighZ
	if r.dataOut {
		gdo0 = gdoAsyncSerial
	}
	paTable := PATableForPreset(spec)
	fsctrl1 := spec.FSCTRL1
	if fsctrl1 == 0 {
		fsctrl1 = 0x06
	}
	agc := spec.AGC
	if agc == (AGCConfig{}) {
		agc = DefaultOOKAGC()
	}

	if err := r.strobe(strobeIdle); err != nil {
		return err
	}
	regs := [][2]uint8{
		{regIOCFG0, gdo0},
		{regPKTCTRL0, 0x32}, // async serial mode
		{regFSCTRL1, fsctrl1},
		{regMDMCFG4, spec.MDMCFG4},
		{regMDMCFG3, spec.MDMCFG3},
		{regMDMCFG2, spec.MDMCFG2},
		{regDEVIATN, spec.DEVIATN},
		{regMCSM0, 0x18},
		{regFOCCFG, spec.FOCCFG},
		{regAGCCTRL2, agc.CTRL2},
		{regAGCCTRL1, agc.CTRL1},
		{regAGCCTRL0, agc.CTRL0},
		{regFREND1, spec.FREND1},
		{regFREND0, spec.FREND0},
	}
	for _, rv := range regs {
		if err := r.writeReg(rv[0], rv[1]); err != nil {
			return err
		}
	}
	return r.writeBurst(regPATABLE, paTable[:])
}

// SetDataOutput enables or disables GDO0 asynchronous serial output.
func (r *Radio) SetDataOutput(enabled bool) error {
	r.dataOut = enabled
	val := gdoHighZ
	if enabled {
		val = gdoAsyncSerial
	}
	return r.writeReg(regIOCFG0, val)
}

// Tune sets carrier frequency and configures the Cap CC1101 antenna switch.
func (r *Radio) Tune(freqHz uint32) error {
	f2, f1, f0, err := FreqRegisters(freqHz, r.crystalHz)
	if err != nil {
		return err
	}
	band := SelectAntennaBand(freqHz)
	if err := r.tr.SetRFSwitch(band.RFSwitchHigh); err != nil {
		return err
	}
	if err := r.writeReg(regIOCFG2, band.GDO2Register); err != nil {
		return err
	}
	if err := r.writeReg(regFREQ2, f2); err != nil {
		return err
	}
	if err := r.writeReg(regFREQ1, f1); err != nil {
		return err
	}
	return r.writeReg(regFREQ0, f0)
}

// StartReceive tunes to freqHz and enters continuous RX.
func (r *Radio) StartReceive(freqHz uint32) error {
	if err := r.strobe(strobeIdle); err != nil {
		return err
	}
	if err := r.Tune(freqHz); err != nil {
		return err
	}
	return r.strobe(strobeRX)
}

// StartTransmit tunes to freqHz and enters TX mode; caller keys GDO0 (GPIO 15).
func (r *Radio) StartTransmit(freqHz uint32) error {
	if err := r.strobe(strobeIdle); err != nil {
		return err
	}
	if err := r.Tune(freqHz); err != nil {
		return err
	}
	if err := r.strobe(strobeTX); err != nil {
		return err
	}
	for range 20 {
		st, err := r.readStatus(statusMarcstate)
		if err == nil && (st&0x1F) == marcstateTX {
			return nil
		}
		time.Sleep(time.Millisecond)
	}
	return errors.New("cc1101: timed out entering TX state")
}

// ReadRSSIDBm reads the current RSSI in dBm while receiving.
func (r *Radio) ReadRSSIDBm() (int32, error) {
	raw, err := r.readStatus(statusRSSI)
	if err != nil {
		return -128, err
	}
	return RSSIToDBm(raw), nil
}

// Idle puts the CC1101 into IDLE state.
func (r *Radio) Idle() error {
	return r.strobe(strobeIdle)
}
