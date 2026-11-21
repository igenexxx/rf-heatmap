package cc1101

import (
	"context"
	"errors"
	"testing"
	"unsafe"
)

type recordingTransport struct {
	mockTransport
	strobes    []uint8
	iocfg0Hist []uint8
	txPulses   []int32
	txErr      error
}

func (rt *recordingTransport) Transfer(tx []byte) ([]byte, error) {
	if len(tx) == 1 {
		rt.strobes = append(rt.strobes, tx[0])
	} else if len(tx) == 2 && tx[0]&spiRead == 0 && (tx[0]&0x3F) == regIOCFG0 {
		rt.iocfg0Hist = append(rt.iocfg0Hist, tx[1])
	}
	return rt.mockTransport.Transfer(tx)
}

func (rt *recordingTransport) TransmitPulses(ctx context.Context, pulses []int32) error {
	rt.txPulses = append([]int32(nil), pulses...)
	if err := ctx.Err(); err != nil {
		return err
	}
	return rt.txErr
}

func TestRadioTransmitPulsesLifecycle(t *testing.T) {
	tr := &recordingTransport{
		mockTransport: mockTransport{regs: make(map[uint8]uint8), partNum: 0x00, version: 0x14},
	}
	r, err := Open(tr, DefaultCrystalHz)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	tr.strobes = nil
	tr.iocfg0Hist = nil

	pulses := []int32{-1000, 350, -1050, 1050, -350}
	if err := r.TransmitPulses(context.Background(), 433_920_000, PresetAM270, pulses); err != nil {
		t.Fatalf("TransmitPulses failed: %v", err)
	}
	if len(tr.txPulses) != len(pulses) {
		t.Fatalf("got %d transmitted pulses, want %d", len(tr.txPulses), len(pulses))
	}
	// Must enable async serial GDO0 (0x0D) before TX and restore High-Z (0x2E) after TX.
	if len(tr.iocfg0Hist) < 2 || tr.iocfg0Hist[len(tr.iocfg0Hist)-1] != gdoHighZ {
		t.Fatalf("expected final IOCFG0=0x%02X, got history %X", gdoHighZ, tr.iocfg0Hist)
	}
	// Final strobe must return CC1101 to IDLE.
	if len(tr.strobes) == 0 || tr.strobes[len(tr.strobes)-1] != strobeIdle {
		t.Fatalf("expected final strobe IDLE (0x%02X), got %X", strobeIdle, tr.strobes)
	}
}

func TestRadioTransmitPulsesErrorsAndCleanup(t *testing.T) {
	tr := &recordingTransport{
		mockTransport: mockTransport{regs: make(map[uint8]uint8), partNum: 0x00, version: 0x14},
	}
	r, err := Open(tr, DefaultCrystalHz)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}

	if err := r.TransmitPulses(context.Background(), 433_920_000, PresetAM650, nil); err == nil {
		t.Fatal("expected error for empty pulses")
	}

	canceledCtx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := r.TransmitPulses(canceledCtx, 433_920_000, PresetAM650, []int32{350, -350}); !errors.Is(err, context.Canceled) {
		t.Fatalf("expected context.Canceled, got %v", err)
	}

	// Transport TX error must still restore GDO0 High-Z and IDLE.
	tr.strobes = nil
	tr.iocfg0Hist = nil
	tr.txErr = errors.New("gpio busy")
	if err := r.TransmitPulses(context.Background(), 433_920_000, PresetAM650, []int32{350, -350}); err == nil {
		t.Fatal("expected transport TX error")
	}
	if tr.iocfg0Hist[len(tr.iocfg0Hist)-1] != gdoHighZ {
		t.Fatalf("expected High-Z cleanup on TX error, got %X", tr.iocfg0Hist)
	}
	if tr.strobes[len(tr.strobes)-1] != strobeIdle {
		t.Fatalf("expected IDLE cleanup on TX error, got %X", tr.strobes)
	}
}

func TestGPIOV2StructSizes(t *testing.T) {
	// Guard exact Linux kernel ABI sizes for GPIO v2 ioctl structs on 64-bit platforms.
	if got := unsafe.Sizeof(gpioV2LineRequest{}); got != 592 {
		t.Fatalf("sizeof(gpioV2LineRequest) = %d, want 592", got)
	}
	if got := unsafe.Sizeof(gpioV2LineValues{}); got != 16 {
		t.Fatalf("sizeof(gpioV2LineValues) = %d, want 16", got)
	}
	if got := unsafe.Sizeof(gpioV2LineEvent{}); got != 48 {
		t.Fatalf("sizeof(gpioV2LineEvent) = %d, want 48", got)
	}
}

func TestEdgeToSignedPulse(t *testing.T) {
	if p, ok := edgeToSignedPulse(1_000_000, gpioV2EventRising, 1_350_000, gpioV2EventFalling); !ok || p != 350 {
		t.Fatalf("rising->falling got (%d, %v), want (350, true)", p, ok)
	}
	if p, ok := edgeToSignedPulse(1_350_000, gpioV2EventFalling, 2_400_000, gpioV2EventRising); !ok || p != -1050 {
		t.Fatalf("falling->rising got (%d, %v), want (-1050, true)", p, ok)
	}
	if _, ok := edgeToSignedPulse(0, gpioV2EventRising, 1_000_000, gpioV2EventFalling); ok {
		t.Fatal("first edge with prevTs=0 must be ignored")
	}
}
