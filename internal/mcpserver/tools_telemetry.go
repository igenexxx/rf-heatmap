package mcpserver

import (
	"encoding/json"
	"fmt"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/config"
)

func (s *Server) toolMeasureFreqOffset(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		FreqHz  uint32 `json:"freq_hz"`
		Preset  string `json:"preset"`
		Samples *int   `json:"samples"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if args.FreqHz < minFreqHz || args.FreqHz > maxFreqHz {
		return toolError(fmt.Sprintf("freq_hz %d out of range [%d..%d]", args.FreqHz, minFreqHz, maxFreqHz))
	}
	preset, err := parsePresetOrDefault(args.Preset)
	if err != nil {
		return toolError(err.Error())
	}
	count := 4
	if args.Samples != nil {
		count = *args.Samples
	}
	if count < 1 || count > 16 {
		return toolError(fmt.Sprintf("samples %d out of range [1..16]", count))
	}
	return s.measureFreqOffsetExec(args.FreqHz, preset, count)
}

func (s *Server) measureFreqOffsetExec(freqHz uint32, preset cc1101.Preset, count int) (*toolCallResult, *rpcError) {
	s.radioMu.Lock()
	defer func() {
		if s.radio != nil {
			_ = s.radio.Idle()
		}
		s.radioMu.Unlock()
	}()

	if ub, ok := s.radio.(HardwareUnlockBackend); ok {
		if err := s.radio.ApplyPreset(preset); err != nil {
			return toolError("apply preset failed: " + err.Error())
		}
		if err := s.radio.StartReceive(freqHz); err != nil {
			return toolError("start receive failed: " + err.Error())
		}
		var sumOff int64
		var lastRSSI int32
		var lastLQI uint8
		var lastCRC bool
		for range count {
			if s.settleDelay > 0 {
				time.Sleep(s.settleDelay)
			}
			off, err := ub.ReadFreqOffsetHz()
			if err != nil {
				return toolError("read freqest failed: " + err.Error())
			}
			sumOff += int64(off)
			lastRSSI, _ = s.radio.ReadRSSIDBm()
			lastLQI, lastCRC, _ = ub.ReadLQI()
		}
		avgOff := int32(sumOff / int64(count))
		return jsonResult(map[string]any{
			"freq_hz": freqHz, "preset": cc1101.GetPresetSpec(preset).Name,
			"offset_hz": avgOff, "offset_ppm": round1(float64(avgOff) * 1e6 / float64(freqHz)),
			"rssi_dbm": lastRSSI, "lqi": lastLQI, "crc_ok": lastCRC, "source": "hardware",
		})
	}
	const simOffsetHz int32 = 1587
	return jsonResult(map[string]any{
		"freq_hz": freqHz, "preset": cc1101.GetPresetSpec(preset).Name,
		"offset_hz": simOffsetHz, "offset_ppm": round1(float64(simOffsetHz) * 1e6 / float64(freqHz)),
		"rssi_dbm": -62, "lqi": 28, "crc_ok": true, "source": "sim",
	})
}

func (s *Server) toolSetTXPower(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		DBm     *int   `json:"dbm"`
		FreqHz  uint32 `json:"freq_hz"`
		Persist *bool  `json:"persist"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if args.DBm == nil {
		return toolError("dbm is required")
	}
	freqHz := args.FreqHz
	if freqHz == 0 {
		freqHz = 433_920_000
	}
	paByte, applied, err := cc1101.PATableByte(freqHz, *args.DBm)
	if err != nil {
		return toolError(err.Error())
	}
	s.radioMu.Lock()
	if ub, ok := s.radio.(HardwareUnlockBackend); ok {
		err = ub.SetTXPowerDBm(freqHz, applied)
	}
	s.radioMu.Unlock()
	if err != nil {
		return toolError("set tx power failed: " + err.Error())
	}
	s.settings.TXPowerDBm = applied
	persisted := false
	if (args.Persist == nil || *args.Persist) && s.configPath != "" {
		if saveErr := config.Save(s.configPath, s.settings); saveErr != nil {
			return toolError("save config failed: " + saveErr.Error())
		}
		persisted = true
	}
	return jsonResult(map[string]any{
		"requested_dbm": *args.DBm, "applied_dbm": applied,
		"freq_hz": freqHz, "patable_hex": fmt.Sprintf("0x%02X", paByte), "persisted": persisted,
	})
}

func (s *Server) toolWORListen(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		FreqHz       uint32 `json:"freq_hz"`
		IntervalMs   *int   `json:"interval_ms"`
		ThresholdDBm *int32 `json:"threshold_dbm"`
		DurationMs   *int   `json:"duration_ms"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if args.FreqHz < minFreqHz || args.FreqHz > maxFreqHz {
		return toolError(fmt.Sprintf("freq_hz %d out of range [%d..%d]", args.FreqHz, minFreqHz, maxFreqHz))
	}
	intervalMs := 500
	if args.IntervalMs != nil {
		intervalMs = *args.IntervalMs
	}
	if intervalMs < int(cc1101.MinWORIntervalMs) || intervalMs > int(cc1101.MaxWORIntervalMs) {
		return toolError(fmt.Sprintf("interval_ms %d out of range [%d..%d]", intervalMs, cc1101.MinWORIntervalMs, cc1101.MaxWORIntervalMs))
	}
	thr := int32(-75)
	if args.ThresholdDBm != nil {
		thr = *args.ThresholdDBm
	}
	durMs := 500
	if args.DurationMs != nil {
		durMs = *args.DurationMs
	}
	if durMs < 50 || durMs > 5000 {
		return toolError(fmt.Sprintf("duration_ms %d out of range [50..5000]", durMs))
	}
	return s.worListenExec(args.FreqHz, uint16(intervalMs), thr, durMs)
}

func (s *Server) worListenExec(freqHz uint32, intervalMs uint16, thr int32, durMs int) (*toolCallResult, *rpcError) {
	s.radioMu.Lock()
	defer func() {
		if s.radio != nil {
			_ = s.radio.Idle()
		}
		s.radioMu.Unlock()
	}()
	if ub, ok := s.radio.(HardwareUnlockBackend); ok {
		if err := s.radio.StartReceive(freqHz); err != nil {
			return toolError("start receive failed: " + err.Error())
		}
		if err := ub.ConfigureWOR(intervalMs); err != nil {
			return toolError("configure WOR failed: " + err.Error())
		}
		if err := ub.EnterWOR(); err != nil {
			return toolError("enter WOR failed: " + err.Error())
		}
		peak, _ := s.radio.ReadRSSIDBm()
		off, _ := ub.ReadFreqOffsetHz()
		lqi, _, _ := ub.ReadLQI()
		return jsonResult(map[string]any{
			"freq_hz": freqHz, "interval_ms": intervalMs, "duration_ms": durMs,
			"woke": peak >= thr, "peak_rssi_dbm": peak, "offset_hz": off, "lqi": lqi, "source": "hardware",
		})
	}
	return jsonResult(map[string]any{
		"freq_hz": freqHz, "interval_ms": intervalMs, "duration_ms": durMs,
		"woke": true, "peak_rssi_dbm": -58, "offset_hz": 1587, "lqi": 24, "source": "sim",
	})
}
