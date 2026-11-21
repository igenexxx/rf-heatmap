package cc1101

import (
	"context"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	gpioChipPath       = "/dev/gpiochip0"
	capGDO0GPIO uint32 = 15

	gpioV2LineFlagOutput uint64 = 1 << 3

	// Linux <linux/gpio.h> v2 ioctl command numbers (64-bit ARM64/x86_64).
	gpioV2GetLineIOCTL       uintptr = 0xC250B407 // _IOWR(0xB4, 0x07, 592)
	gpioV2LineSetValuesIOCTL uintptr = 0xC010B40F // _IOWR(0xB4, 0x0F, 16)

	txSleepThreshold = 1500 * time.Microsecond
	txSleepMargin    = 400 * time.Microsecond
)

type gpioV2LineAttr struct {
	id      uint32
	padding uint32
	data    uint64
}

type gpioV2LineConfigAttr struct {
	attr gpioV2LineAttr
	mask uint64
}

type gpioV2LineConfig struct {
	flags    uint64
	numAttrs uint32
	padding  [5]uint32
	attrs    [10]gpioV2LineConfigAttr
}

type gpioV2LineRequest struct {
	offsets         [64]uint32
	consumer        [32]byte
	config          gpioV2LineConfig
	numLines        uint32
	eventBufferSize uint32
	padding         [5]uint32
	fd              int32
}

type gpioV2LineValues struct {
	bits uint64
	mask uint64
}

// TransmitPulses drives Cap CC1101 GDO0 (GPIO 15) according to signed microsecond
// pulse durations (+high carrier on, -low carrier off) via Linux GPIO v2 ioctl.
func (lt *LinuxTransport) TransmitPulses(ctx context.Context, pulses []int32) error {
	lineFD, err := openGDO0OutputLine()
	if err != nil {
		return err
	}
	defer func() {
		_ = setGDO0Level(lineFD, false)
		_ = syscall.Close(lineFD)
	}()

	if err := setGDO0Level(lineFD, false); err != nil {
		return err
	}
	return keyPulseTrain(ctx, lineFD, pulses)
}

func openGDO0OutputLine() (int, error) {
	chip, err := os.OpenFile(gpioChipPath, os.O_RDWR, 0)
	if err != nil {
		return -1, fmt.Errorf("cc1101: open %s: %w", gpioChipPath, err)
	}
	defer chip.Close()

	var req gpioV2LineRequest
	req.offsets[0] = capGDO0GPIO
	copy(req.consumer[:], "rf-heatmap-tx")
	req.config.flags = gpioV2LineFlagOutput
	req.numLines = 1

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, chip.Fd(), gpioV2GetLineIOCTL, uintptr(unsafe.Pointer(&req)))
	if errno != 0 {
		return -1, fmt.Errorf("cc1101: request GPIO %d: %w", capGDO0GPIO, errno)
	}
	return int(req.fd), nil
}

func setGDO0Level(lineFD int, high bool) error {
	vals := gpioV2LineValues{mask: 1}
	if high {
		vals.bits = 1
	}
	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, uintptr(lineFD), gpioV2LineSetValuesIOCTL, uintptr(unsafe.Pointer(&vals)))
	if errno != 0 {
		return fmt.Errorf("cc1101: set GPIO %d: %w", capGDO0GPIO, errno)
	}
	return nil
}

func keyPulseTrain(ctx context.Context, lineFD int, pulses []int32) error {
	deadline := time.Now()
	for _, p := range pulses {
		if err := ctx.Err(); err != nil {
			return err
		}
		if p == 0 {
			continue
		}
		high := p > 0
		durUs := p
		if durUs < 0 {
			durUs = -durUs
		}
		if err := setGDO0Level(lineFD, high); err != nil {
			return err
		}
		deadline = deadline.Add(time.Duration(durUs) * time.Microsecond)
		if err := waitUntil(ctx, deadline); err != nil {
			return err
		}
	}
	return nil
}

func waitUntil(ctx context.Context, target time.Time) error {
	for {
		rem := time.Until(target)
		if rem <= 0 {
			return nil
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if rem > txSleepThreshold {
			time.Sleep(rem - txSleepMargin)
		}
	}
}
