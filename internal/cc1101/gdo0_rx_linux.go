package cc1101

import (
	"context"
	"errors"
	"fmt"
	"os"
	"syscall"
	"time"
	"unsafe"
)

const (
	gpioV2LineFlagInput       uint64 = 1 << 2
	gpioV2LineFlagEdgeRising  uint64 = 1 << 4
	gpioV2LineFlagEdgeFalling uint64 = 1 << 5

	gpioV2EventRising  uint32 = 1
	gpioV2EventFalling uint32 = 2

	rxEventBufSize  = 512
	rxMaxPulseUs    = 100_000
	rxReadPollSlice = 25 * time.Millisecond
)

type gpioV2LineEvent struct {
	timestampNs uint64
	id          uint32
	offset      uint32
	seqno       uint32
	lineSeqno   uint32
	padding     [6]uint32
}

// PulseCapturer is an optional Transport capability for live GDO0 (GPIO 15) RX edge capture.
type PulseCapturer interface {
	CapturePulses(ctx context.Context, out chan<- int32) error
}

// CapturePulses tunes CC1101 to freqHz in async serial RX mode, streams signed microsecond
// pulse durations (+high / -low) from GDO0 into out until ctx is cancelled, and always
// restores GDO0 to High-Z and CC1101 to IDLE on exit.
func (r *Radio) CapturePulses(ctx context.Context, freqHz uint32, preset Preset, out chan<- int32) error {
	capTr, ok := r.tr.(PulseCapturer)
	if !ok {
		return errors.New("cc1101: transport does not support GDO0 pulse capture")
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if r.preset != preset {
		if err := r.ApplyPreset(preset); err != nil {
			return err
		}
	}
	if err := r.SetDataOutput(true); err != nil {
		return err
	}
	defer func() {
		_ = r.SetDataOutput(false)
		_ = r.Idle()
	}()
	if err := r.StartReceive(freqHz); err != nil {
		return err
	}
	return capTr.CapturePulses(ctx, out)
}

// CapturePulses reads rising/falling kernel edge events on Cap CC1101 GDO0 (GPIO 15)
// via Linux GPIO v2 character device ioctl and emits signed microsecond durations.
func (lt *LinuxTransport) CapturePulses(ctx context.Context, out chan<- int32) error {
	lineFD, err := openGDO0InputEdgeLine()
	if err != nil {
		return err
	}
	if err := syscall.SetNonblock(lineFD, true); err != nil {
		_ = syscall.Close(lineFD)
		return fmt.Errorf("cc1101: set nonblock GPIO %d: %w", capGDO0GPIO, err)
	}
	f := os.NewFile(uintptr(lineFD), "gdo0-rx")
	defer f.Close()

	return readGDO0Events(ctx, f, out)
}

func openGDO0InputEdgeLine() (int, error) {
	chip, err := os.OpenFile(gpioChipPath, os.O_RDWR, 0)
	if err != nil {
		return -1, fmt.Errorf("cc1101: open %s: %w", gpioChipPath, err)
	}
	defer chip.Close()

	var req gpioV2LineRequest
	req.offsets[0] = capGDO0GPIO
	copy(req.consumer[:], "rf-heatmap-rx")
	req.config.flags = gpioV2LineFlagInput | gpioV2LineFlagEdgeRising | gpioV2LineFlagEdgeFalling
	req.numLines = 1
	req.eventBufferSize = rxEventBufSize

	_, _, errno := syscall.Syscall(syscall.SYS_IOCTL, chip.Fd(), gpioV2GetLineIOCTL, uintptr(unsafe.Pointer(&req)))
	if errno != 0 {
		return -1, fmt.Errorf("cc1101: request RX GPIO %d: %w", capGDO0GPIO, errno)
	}
	return int(req.fd), nil
}

func readGDO0Events(ctx context.Context, f *os.File, out chan<- int32) error {
	const evSize = int(unsafe.Sizeof(gpioV2LineEvent{}))
	buf := make([]byte, evSize*16)
	var lastTs uint64
	var lastID uint32

	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		_ = f.SetReadDeadline(time.Now().Add(rxReadPollSlice))
		n, err := f.Read(buf)
		if err != nil {
			if errors.Is(err, os.ErrDeadlineExceeded) {
				continue
			}
			return err
		}
		for off := 0; off+evSize <= n; off += evSize {
			ev := *(*gpioV2LineEvent)(unsafe.Pointer(&buf[off]))
			if pulse, ok := edgeToSignedPulse(lastTs, lastID, ev.timestampNs, ev.id); ok {
				select {
				case out <- pulse:
				default:
				}
			}
			lastTs, lastID = ev.timestampNs, ev.id
		}
	}
}

func edgeToSignedPulse(prevTs uint64, prevID uint32, currTs uint64, currID uint32) (int32, bool) {
	if prevTs == 0 || currTs <= prevTs {
		return 0, false
	}
	durUs := (currTs - prevTs) / 1000
	if durUs == 0 {
		return 0, false
	}
	if durUs > rxMaxPulseUs {
		durUs = rxMaxPulseUs
	}
	switch {
	case prevID == gpioV2EventRising && currID == gpioV2EventFalling:
		return int32(durUs), true
	case prevID == gpioV2EventFalling && currID == gpioV2EventRising:
		return -int32(durUs), true
	default:
		return 0, false
	}
}
