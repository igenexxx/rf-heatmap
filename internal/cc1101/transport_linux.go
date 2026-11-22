package cc1101

import (
	"fmt"
	"os"
	"os/exec"
	"strconv"
	"strings"
	"sync"
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

	capRFSwitchGPIO   = 14
	capPowerGPIO      = "26"
	ext5vLedPath      = "/sys/class/leds/ext_5v_out/brightness"
	capPowerSettleMs  = 50 * time.Millisecond
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

// LinuxTransport implements Transport using /dev/spidev0.1, /dev/gpiochip0, and ext_5v_out.
type LinuxTransport struct {
	spiMu       sync.Mutex
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
	if err := configureSPIDev(f, speedHz); err != nil {
		_ = f.Close()
		return nil, err
	}
	lt := &LinuxTransport{spiFile: f, speedHz: speedHz, rfSwPin: capRFSwitchGPIO}
	lt.snapshotAndClaimRFSwitch()
	return lt, nil
}

func configureSPIDev(f *os.File, speedHz uint32) error {
	mode := uint8(0)
	bits := uint8(8)
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), spiIOCWrMode, uintptr(unsafe.Pointer(&mode))); errno != 0 {
		return fmt.Errorf("spi set mode: %w", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), spiIOCWrBits, uintptr(unsafe.Pointer(&bits))); errno != 0 {
		return fmt.Errorf("spi set bits: %w", errno)
	}
	if _, _, errno := syscall.Syscall(syscall.SYS_IOCTL, f.Fd(), spiIOCWrMaxSpeed, uintptr(unsafe.Pointer(&speedHz))); errno != 0 {
		return fmt.Errorf("spi set speed: %w", errno)
	}
	return nil
}

// Transfer performs a full-duplex SPI transaction.
func (lt *LinuxTransport) Transfer(tx []byte) ([]byte, error) {
	if len(tx) == 0 {
		return nil, nil
	}
	lt.spiMu.Lock()
	defer lt.spiMu.Unlock()
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

// SetRFSwitch drives GPIO 14 (RF_SW0) high or low for antenna band selection.
func (lt *LinuxTransport) SetRFSwitch(high bool) error {
	lvl := "dl"
	if high {
		lvl = "dh"
	}
	return exec.Command("pinctrl", "set", strconv.Itoa(lt.rfSwPin), "op", lvl).Run()
}

// Close restores GPIO 14 pinmux state and closes the SPI file descriptor.
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
	_ = exec.Command("pinctrl", "set", capPowerGPIO, "op", "dh").Run()
	data, err := os.ReadFile(ext5vLedPath)
	if err != nil {
		return nil // image without LED class control
	}
	val, _ := strconv.Atoi(strings.TrimSpace(string(data)))
	if val <= 0 {
		if err := os.WriteFile(ext5vLedPath, []byte("1"), 0o644); err != nil {
			return fmt.Errorf("enable ext_5v_out: %w", err)
		}
		time.Sleep(capPowerSettleMs)
	}
	return nil
}
