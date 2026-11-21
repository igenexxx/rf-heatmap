package mcpserver

import (
	"encoding/json"
	"fmt"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/rfml"
)

const (
	defaultFoxhuntSamples    = 16
	minFoxhuntSamples        = 8
	maxFoxhuntSamples        = 64
	defaultFoxhuntIntervalMs = 20
	maxFoxhuntIntervalMs     = 200
)

type foxhuntParams struct {
	freqHz   uint32
	preset   cc1101.Preset
	count    int
	interval time.Duration
}

func parseFoxhuntParams(rawArgs json.RawMessage) (foxhuntParams, *toolCallResult, *rpcError) {
	var args struct {
		FreqHz     uint32 `json:"freq_hz"`
		Preset     string `json:"preset"`
		Samples    int    `json:"samples"`
		IntervalMs *int   `json:"interval_ms"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return foxhuntParams{}, nil, err
	}
	if args.FreqHz < minFreqHz || args.FreqHz > maxFreqHz {
		res, rerr := toolError(fmt.Sprintf("freq_hz must be between %d and %d", minFreqHz, maxFreqHz))
		return foxhuntParams{}, res, rerr
	}
	preset, err := parsePresetOrDefault(args.Preset)
	if err != nil {
		res, rerr := toolError(err.Error())
		return foxhuntParams{}, res, rerr
	}
	count := args.Samples
	if count == 0 {
		count = defaultFoxhuntSamples
	}
	count = max(minFoxhuntSamples, min(maxFoxhuntSamples, count))
	interval := defaultFoxhuntIntervalMs * time.Millisecond
	if args.IntervalMs != nil {
		ms := max(0, min(maxFoxhuntIntervalMs, *args.IntervalMs))
		interval = time.Duration(ms) * time.Millisecond
	}
	return foxhuntParams{freqHz: args.FreqHz, preset: preset, count: count, interval: interval}, nil, nil
}

func (s *Server) toolFoxhuntTrack(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	p, errRes, rpcErr := parseFoxhuntParams(rawArgs)
	if errRes != nil || rpcErr != nil {
		return errRes, rpcErr
	}
	blCfg := rfml.DefaultBaselineConfig(1)
	blCfg.SquelchK = s.settings.SquelchK
	bl := rfml.NewBinBaseline(blCfg)

	samples, kState, actRep, err := s.runFoxhuntLoop(p, bl)
	if err != nil {
		return toolError(err.Error())
	}
	return jsonResult(map[string]any{
		"freq_hz":             p.freqHz,
		"preset":              cc1101.GetPresetSpec(p.preset).Name,
		"samples_count":       p.count,
		"raw_last_dbm":        samples[p.count-1],
		"smart_threshold_dbm": bl.SmartThreshold(0, defaultFallbackSquelchDBm),
		"kalman":              kState,
		"activity":            actRep,
		"samples_dbm":         samples,
	})
}

func (s *Server) runFoxhuntLoop(p foxhuntParams, bl *rfml.BinBaseline) ([]int32, rfml.KalmanState, rfml.ActivityReport, error) {
	kf := rfml.NewKalmanRSSI(rfml.DefaultKalmanQ, rfml.DefaultKalmanR)
	pt := rfml.NewPeriodicityTracker(rfml.DefaultMinBurstGap)
	samples := make([]int32, p.count)
	var kState rfml.KalmanState
	var actRep rfml.ActivityReport

	s.radioMu.Lock()
	defer func() {
		if s.radio != nil {
			_ = s.radio.Idle()
		}
		s.radioMu.Unlock()
	}()

	if s.radio != nil {
		if err := s.radio.ApplyPreset(p.preset); err != nil {
			return nil, kState, actRep, fmt.Errorf("apply preset failed: %w", err)
		}
	}
	for i := range p.count {
		rssi, err := s.sampleFreqLocked(p.freqHz, i+1, 0, 1)
		if err != nil {
			return nil, kState, actRep, fmt.Errorf("read rssi failed: %w", err)
		}
		samples[i] = rssi
		bl.UpdateBin(0, rssi)
		kState = kf.Update(rssi)
		actRep = pt.Observe(time.Now(), rssi >= bl.SmartThreshold(0, defaultFallbackSquelchDBm))
		if i+1 < p.count && p.interval > 0 && s.radio != nil {
			time.Sleep(p.interval)
		}
	}
	return samples, kState, actRep, nil
}
