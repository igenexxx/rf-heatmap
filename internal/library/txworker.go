package library

import (
	"context"
	"errors"
	"sync"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/subfile"
)

const (
	maxSimBurstDuration = 120 * time.Millisecond
	minSimBurstDuration = 25 * time.Millisecond
)

// Transmitter sends a parsed Flipper .sub signal over hardware or a simulator.
type Transmitter interface {
	Transmit(ctx context.Context, sf *subfile.SubFile) error
}

// RadioTransmitter transmits .sub files through a real *cc1101.Radio.
type RadioTransmitter struct {
	radio *cc1101.Radio
}

// NewRadioTransmitter wraps a *cc1101.Radio as a Transmitter.
func NewRadioTransmitter(radio *cc1101.Radio) *RadioTransmitter {
	return &RadioTransmitter{radio: radio}
}

// Transmit encodes sf into signed microsecond pulses and transmits them via CC1101.
func (rt *RadioTransmitter) Transmit(ctx context.Context, sf *subfile.SubFile) error {
	if rt.radio == nil {
		return errors.New("library: nil CC1101 radio")
	}
	pulses, err := subfile.EncodePulses(sf)
	if err != nil {
		return err
	}
	preset, ok := cc1101.PresetFromSubName(sf.Preset)
	if !ok {
		preset = cc1101.PresetAM650
	}
	return rt.radio.TransmitPulses(ctx, sf.FreqHz, preset, pulses)
}

// SimTransmitter validates .sub encoding and simulates burst transmission timing.
type SimTransmitter struct {
	FixedDelay time.Duration // optional override for deterministic tests
}

// Transmit encodes sf and waits for a short simulated burst duration, respecting ctx.Done().
func (st *SimTransmitter) Transmit(ctx context.Context, sf *subfile.SubFile) error {
	pulses, err := subfile.EncodePulses(sf)
	if err != nil {
		return err
	}
	dur := st.FixedDelay
	if dur <= 0 {
		dur = estimateSimDuration(pulses)
	}
	timer := time.NewTimer(dur)
	defer timer.Stop()
	select {
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return nil
	}
}

func estimateSimDuration(pulses []int32) time.Duration {
	var totalUs int64
	for _, p := range pulses {
		if p < 0 {
			totalUs -= int64(p)
		} else {
			totalUs += int64(p)
		}
	}
	d := time.Duration(totalUs) * time.Microsecond
	if d < minSimBurstDuration {
		return minSimBurstDuration
	}
	if d > maxSimBurstDuration {
		return maxSimBurstDuration
	}
	return d
}

// TXRequest represents a single non-blocking transmission job.
type TXRequest struct {
	Entry   EntryRef
	SubFile *subfile.SubFile // optional; loaded from Entry.Path if nil
	Index   int              // caller-defined list index (-1 for standalone TX)
	IsProbe bool             // true when triggered by Auto-Probe
}

// TXResult reports the outcome of a completed or cancelled TXRequest.
type TXResult struct {
	Entry    EntryRef
	Index    int
	IsProbe  bool
	Canceled bool
	Err      error
}

// TXWorker executes Sub-GHz transmissions off the UI thread with instant cancellation.
type TXWorker struct {
	tx      Transmitter
	reqCh   chan TXRequest
	resCh   chan TXResult
	doneCh  chan struct{}
	closeWg sync.WaitGroup

	mu         sync.Mutex
	cancelCurr context.CancelFunc
	closed     bool
}

// NewTXWorker starts a background worker goroutine backed by tx.
func NewTXWorker(tx Transmitter) *TXWorker {
	if tx == nil {
		tx = &SimTransmitter{}
	}
	w := &TXWorker{
		tx:     tx,
		reqCh:  make(chan TXRequest, 1),
		resCh:  make(chan TXResult, 4),
		doneCh: make(chan struct{}),
	}
	w.closeWg.Add(1)
	go w.loop()
	return w
}

// Submit enqueues a transmission job without blocking the caller. Returns false if busy or closed.
func (w *TXWorker) Submit(req TXRequest) bool {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return false
	}
	w.mu.Unlock()
	select {
	case w.reqCh <- req:
		return true
	default:
		return false
	}
}

// Cancel immediately cancels any in-flight transmission.
func (w *TXWorker) Cancel() {
	w.mu.Lock()
	cancel := w.cancelCurr
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
}

// PollResult returns a completed TXResult if one is ready (non-blocking).
func (w *TXWorker) PollResult() (TXResult, bool) {
	select {
	case res := <-w.resCh:
		return res, true
	default:
		return TXResult{}, false
	}
}

// Close cancels any active TX and waits for the worker goroutine to exit cleanly.
func (w *TXWorker) Close() {
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		return
	}
	w.closed = true
	cancel := w.cancelCurr
	w.mu.Unlock()
	if cancel != nil {
		cancel()
	}
	close(w.doneCh)
	w.closeWg.Wait()
}

func (w *TXWorker) loop() {
	defer w.closeWg.Done()
	for {
		select {
		case <-w.doneCh:
			return
		case req := <-w.reqCh:
			w.execute(req)
		}
	}
}

func (w *TXWorker) execute(req TXRequest) {
	ctx, cancel := context.WithCancel(context.Background())
	w.mu.Lock()
	if w.closed {
		w.mu.Unlock()
		cancel()
		return
	}
	w.cancelCurr = cancel
	w.mu.Unlock()

	defer func() {
		cancel()
		w.mu.Lock()
		w.cancelCurr = nil
		w.mu.Unlock()
	}()

	sf := req.SubFile
	var err error
	if sf == nil {
		sf, err = LoadSubFile(req.Entry)
	}
	if err == nil {
		err = w.tx.Transmit(ctx, sf)
	}
	res := TXResult{
		Entry:    req.Entry,
		Index:    req.Index,
		IsProbe:  req.IsProbe,
		Canceled: errors.Is(err, context.Canceled),
		Err:      err,
	}
	select {
	case w.resCh <- res:
	case <-w.doneCh:
	}
}
