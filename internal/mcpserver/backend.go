package mcpserver

import (
	"context"
	"sync"
	"time"

	"rf-heatmap/internal/cc1101"
)

const (
	minFreqHz uint32 = 280_000_000
	maxFreqHz uint32 = 950_000_000
)

// RadioBackend abstracts CC1101 hardware operations so MCP tools are testable without hardware.
type RadioBackend interface {
	Info() cc1101.ChipInfo
	ApplyPreset(p cc1101.Preset) error
	StartReceive(freqHz uint32) error
	ReadRSSIDBm() (int32, error)
	TransmitPulses(ctx context.Context, freqHz uint32, preset cc1101.Preset, pulses []int32) error
	Idle() error
}

// CC1101Backend wraps a real *cc1101.Radio with a mutex to serialize all SPI/GPIO access.
type CC1101Backend struct {
	mu    sync.Mutex
	radio *cc1101.Radio
}

// NewCC1101Backend wraps a *cc1101.Radio as a thread-safe RadioBackend.
// Returns nil if radio is nil.
func NewCC1101Backend(radio *cc1101.Radio) *CC1101Backend {
	if radio == nil {
		return nil
	}
	return &CC1101Backend{radio: radio}
}

func (b *CC1101Backend) Info() cc1101.ChipInfo {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.radio.Info()
}

func (b *CC1101Backend) ApplyPreset(p cc1101.Preset) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.radio.ApplyPreset(p)
}

func (b *CC1101Backend) StartReceive(freqHz uint32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.radio.StartReceive(freqHz)
}

func (b *CC1101Backend) ReadRSSIDBm() (int32, error) {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.radio.ReadRSSIDBm()
}

func (b *CC1101Backend) TransmitPulses(ctx context.Context, freqHz uint32, preset cc1101.Preset, pulses []int32) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.radio.TransmitPulses(ctx, freqHz, preset, pulses)
}

func (b *CC1101Backend) Idle() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.radio.Idle()
}

func (s *Server) sampleFreqLocked(freqHz uint32, sweepIdx, binIdx, totalBins int) (int32, error) {
	if s.radio != nil {
		if err := s.radio.StartReceive(freqHz); err != nil {
			return -128, err
		}
		if s.settleDelay > 0 {
			time.Sleep(s.settleDelay)
		}
		return s.radio.ReadRSSIDBm()
	}
	// Deterministic simulator mode: quiet noise floor with an occasional 433.92 MHz burst after warmup.
	noise := int32(-104 + ((sweepIdx*3 + binIdx*5) % 4))
	if totalBins > 1 && sweepIdx >= 6 && binIdx == totalBins/2 {
		return -48, nil
	}
	return noise, nil
}
