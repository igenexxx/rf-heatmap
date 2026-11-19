package cc1101

import (
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"syscall"
	"time"
	"unsafe"
)

const (
	// SPI ioctl constants for Linux spidev (aarch64/generic).
	spiIOCMagic      = 'k'
	spiIOCWrMode     = 0x40016B01
	spiIOCWrBits     = 0x40016B03
	spiIOCWrMaxSpeed = 0x40046B04
	spiIOCMessage1   = 0x40206B00
)

type spiIOCTransfer struct {
	txBuf       uint64
	rxBuf       uint64
	len         uint32
	speedHz     uint32
	delayUsecs  uint16
	bitsPerWord uint8
	csChange    uint8
	txNBits     uint8
	rxNBits     uint8
	wordDelay   uint8
	pad         uint8
}

// Transport abstracts SPI + GPIO control so the radio driver is testable without hardware.
type Transport interface {
	Transfer(tx []byte) ([]byte, error)
	SetRFSwitch(high bool) error
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
	paTable := []uint8{0x00, txPowerMax, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00}
	if spec.MDMCFG2&0x70 == 0x00 {
		// 2-FSK uses PATABLE[0]
		paTable[0] = txPowerMax
	}

	steps := []struct {
		fn func() error
	}{
		{func() error { return r.strobe(strobeIdle) }},
		{func() error { return r.writeReg(regIOCFG0, gdo0) }},
		{func() error { return r.writeReg(regPKTCTRL0, 0x32) }}, // async serial mode
		{func() error { return r.writeReg(regFSCTRL1, 0x06) }},
		{func() error { return r.writeReg(regMDMCFG4, spec.MDMCFG4) }},
		{func() error { return r.writeReg(regMDMCFG3, spec.MDMCFG3) }},
		{func() error { return r.writeReg(regMDMCFG2, spec.MDMCFG2) }},
		{func() error { return r.writeReg(regDEVIATN, spec.DEVIATN) }},
		{func() error { return r.writeReg(regMCSM0, 0x18) }},
		{func() error { return r.writeReg(regFOCCFG, spec.FOCCFG) }},
		{func() error { return r.writeReg(regAGCCTRL2, 0x07) }},
		{func() error { return r.writeReg(regAGCCTRL1, 0x00) }},
		{func() error { return r.writeReg(regAGCCTRL0, 0x91) }},
		{func() error { return r.writeReg(regFREND1, spec.FREND1) }},
		{func() error { return r.writeReg(regFREND0, spec.FREND0) }},
		{func() error { return r.writeBurst(regPATABLE, paTable) }},
	}
	for _, s := range steps {
		if err := s.fn(); err != nil {
			return err
		}
	}
	return nil
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

// LinuxTransport implements Transport using /dev/spidev0.1, /dev/gpiochip0, and ext_5v_out.
type LinuxTransport struct {
	spiFile     *os.File
	speedHz     uint32
	rfSwPin     int
	rfSwState   string
	pinctrlUsed bool
}

// OpenLinuxTransport powers the Cap CC1101, opens spidev, and claims GPIO 14 (RF_SW0).
func OpenLinuxTransport(spiPath string, speedHz uint32) (*LinuxTransport, error) {
	if err := enableCapPower(); err != nil {
		return nil, err
	}
	f, err := os.OpenFile(spiPath, os.O_RDWR, 0)
	if err != nil {
		return nil, fmt.Errorf("open %s: %w", spiPath, err)
	}
	mode := uint8(0)
	bits := uint8(8)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), spiIOCWrMode, uintptr(unsafe.Pointer(&mode))); errno != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("spi set mode: %w", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), spiIOCWrBits, uintptr(unsafe.Pointer(&bits))); errno != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("spi set bits: %w", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), spiIOCWrMaxSpeed, uintptr(unsafe.Pointer(&speedHz))); errno != 0 {
		_ = f.Close()
		return nil, fmt.Errorf("spi set speed: %w", errno)
	}

	lt := &LinuxTransport{spiFile: f, speedHz: speedHz, rfSwPin: 14}
	lt.snapshotAndClaimRFSwitch()
	return lt, nil
}

func (lt *LinuxTransport) Transfer(tx []byte) ([]byte, error) {
	if len(tx) == 0 {
		return nil, nil
	}
	rx := make([]byte, len(tx))
	tr := spiIOCTransfer{
		txBuf:       uint64(uintptr(unsafe.Pointer(&tx[0]))),
		rxBuf:       uint64(uintptr(unsafe.Pointer(&rx[0]))),
		len:         uint32(len(tx)),
		speedHz:     lt.speedHz,
		bitsPerWord: 8,
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, lt.spiFile.Fd(), spiIOCMessage1, uintptr(unsafe.Pointer(&tr)))
	if errno != 0 {
		return nil, fmt.Errorf("spi transfer: %w", errno)
	}
	return rx, nil
}

func (lt *LinuxTransport) snapshotAndClaimRFSwitch() {
	out, err := exec.Command("pinctrl", "get", strconv.Itoa(lt.rfSwPin)).Output()
	if err == nil {
		lt.rfSwState = strings.TrimSpace(string(out))
		lt.pinctrlUsed = true
	}
	_ = exec.Command("pinctrl", "set", strconv.Itoa(lt.rfSwPin), "op", "dl").Run()
}

func (lt *LinuxTransport) SetRFSwitch(high bool) error {
	lvl := "dl"
	if high {
		lvl = "dh"
	}
	return exec.Command("pinctrl", "set", strconv.Itoa(lt.rfSwPin), "op", lvl).Run()
}

func (lt *LinuxTransport) Close() error {
	if lt.pinctrlUsed && strings.Contains(lt.rfSwState, "a0") {
		_ = exec.Command("pinctrl", "set", strconv.Itoa(lt.rfSwPin), "a0").Run()
	} else {
		_ = exec.Command("pinctrl", "set", strconv.Itoa(lt.rfSwPin), "ip").Run()
	}
	if lt.spiFile != nil {
		return lt.spiFile.Close()
	}
	return nil
}

func enableCapPower() error {
	// Ensure GPIO 26 is output high and ext_5v_out is enabled.
	_ = exec.Command("pinctrl", "set", "26", "op", "dh").Run()
	const ledPath = "/sys/class/leds/ext_5v_out/brightness"
	data, err := os.ReadFile(ledPath)
	if err != nil {
		return nil // image without LED class control
	}
	val, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if val <= 0 {
		if err := os.WriteFile(ledPath, []byte("1"), 0o644); err != nil {
			return fmt.Errorf("enable ext_5v_out: %w", err)
		}
		time.Sleep(50 * time.Millisecond)
	}
	return nil
}
