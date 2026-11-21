package mcpserver

import (
	"context"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/sensors"
)

func parsePacketArgs(freqHz uint32, presetName string, syncWord *int, whitening, crc *bool) (cc1101.PacketConfig, error) {
	if freqHz < minFreqHz || freqHz > maxFreqHz {
		return cc1101.PacketConfig{}, fmt.Errorf("freq_hz %d out of range [%d..%d]", freqHz, minFreqHz, maxFreqHz)
	}
	preset := cc1101.PresetGFSK999
	if presetName != "" {
		p, err := parsePresetOrDefault(presetName)
		if err != nil {
			return cc1101.PacketConfig{}, err
		}
		preset = p
	}
	sw := cc1101.DefaultPacketSyncWord
	if syncWord != nil {
		if *syncWord < 1 || *syncWord > 0xFFFF {
			return cc1101.PacketConfig{}, fmt.Errorf("sync_word %d out of range [1..65535]", *syncWord)
		}
		sw = uint16(*syncWord)
	}
	return cc1101.PacketConfig{
		FreqHz:    freqHz,
		Preset:    preset,
		SyncWord:  sw,
		Whitening: whitening == nil || *whitening,
		CRCEnable: crc == nil || *crc,
		MaxLen:    cc1101.MaxPacketPayloadBytes,
	}, nil
}

func (s *Server) toolSendPacket(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		FreqHz     uint32 `json:"freq_hz"`
		PayloadHex string `json:"payload_hex"`
		Text       string `json:"text"`
		Preset     string `json:"preset"`
		SyncWord   *int   `json:"sync_word"`
		Whitening  *bool  `json:"whitening"`
		CRC        *bool  `json:"crc"`
		ConfirmRF  bool   `json:"confirm_rf"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	if !args.ConfirmRF {
		return toolError("RF packet transmission refused: set confirm_rf=true to authorize TX")
	}
	cfg, err := parsePacketArgs(args.FreqHz, args.Preset, args.SyncWord, args.Whitening, args.CRC)
	if err != nil {
		return toolError(err.Error())
	}
	payload, err := resolvePacketPayload(args.PayloadHex, args.Text)
	if err != nil {
		return toolError(err.Error())
	}
	source := "sim"
	s.radioMu.Lock()
	if ub, ok := s.radio.(HardwareUnlockBackend); ok {
		source = "hardware"
		err = ub.SendPacket(context.Background(), cfg, payload)
	}
	s.radioMu.Unlock()
	if err != nil {
		return toolError("send packet failed: " + err.Error())
	}
	return jsonResult(map[string]any{
		"sent": true, "freq_hz": cfg.FreqHz, "bytes": len(payload),
		"payload_hex": strings.ToUpper(hex.EncodeToString(payload)),
		"sync_word":   fmt.Sprintf("0x%04X", cfg.SyncWord), "source": source,
	})
}

func resolvePacketPayload(payloadHex, text string) ([]byte, error) {
	var payload []byte
	if payloadHex != "" {
		clean := strings.ReplaceAll(strings.TrimSpace(payloadHex), " ", "")
		b, err := hex.DecodeString(clean)
		if err != nil {
			return nil, fmt.Errorf("invalid payload_hex: %w", err)
		}
		payload = b
	} else if text != "" {
		payload = []byte(text)
	}
	if len(payload) == 0 || len(payload) > cc1101.MaxPacketPayloadBytes {
		return nil, fmt.Errorf("payload length %d must be 1..%d bytes", len(payload), cc1101.MaxPacketPayloadBytes)
	}
	return payload, nil
}

func (s *Server) toolRecvPacket(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		FreqHz    uint32 `json:"freq_hz"`
		Preset    string `json:"preset"`
		SyncWord  *int   `json:"sync_word"`
		Whitening *bool  `json:"whitening"`
		CRC       *bool  `json:"crc"`
		TimeoutMs *int   `json:"timeout_ms"`
		SimSample bool   `json:"sim_sample"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	cfg, err := parsePacketArgs(args.FreqHz, args.Preset, args.SyncWord, args.Whitening, args.CRC)
	if err != nil {
		return toolError(err.Error())
	}
	timeoutMs := 500
	if args.TimeoutMs != nil {
		timeoutMs = *args.TimeoutMs
	}
	if timeoutMs < 50 || timeoutMs > 5000 {
		return toolError(fmt.Sprintf("timeout_ms %d out of range [50..5000]", timeoutMs))
	}
	if !args.SimSample {
		s.radioMu.Lock()
		ub, ok := s.radio.(HardwareUnlockBackend)
		var pkt cc1101.ReceivedPacket
		if ok {
			pkt, err = ub.RecvPacket(context.Background(), cfg, time.Duration(timeoutMs)*time.Millisecond)
		}
		s.radioMu.Unlock()
		if ok {
			if err != nil {
				return toolError("recv packet failed: " + err.Error())
			}
			return jsonResult(map[string]any{
				"received": true, "freq_hz": cfg.FreqHz, "payload_hex": strings.ToUpper(hex.EncodeToString(pkt.Payload)),
				"text": string(pkt.Payload), "rssi_dbm": pkt.RSSIDBm, "lqi": pkt.LQI, "crc_ok": pkt.CRCOK, "source": "hardware",
			})
		}
	}
	simPayload := []byte("CZ-PAGER: PING OK")
	return jsonResult(map[string]any{
		"received": true, "freq_hz": cfg.FreqHz, "payload_hex": strings.ToUpper(hex.EncodeToString(simPayload)),
		"text": string(simPayload), "rssi_dbm": -56, "lqi": 22, "crc_ok": true, "source": "sim",
	})
}

func (s *Server) toolDecodePOCSAG(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		DataHex   string   `json:"data_hex"`
		Words     []uint32 `json:"words"`
		BaudRate  int      `json:"baud_rate"`
		SimSample bool     `json:"sim_sample"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	baud, err := sensors.ValidatePOCSAGBaud(args.BaudRate)
	if err != nil {
		return toolError(err.Error())
	}
	source := "input"
	var msgs []sensors.POCSAGMessage
	switch {
	case len(args.Words) > 0:
		msgs, err = sensors.DecodePOCSAGWords(args.Words, baud)
	case args.DataHex != "":
		raw, hexErr := hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(args.DataHex), " ", ""))
		if hexErr != nil {
			return toolError("invalid data_hex: " + hexErr.Error())
		}
		msgs, err = sensors.DecodePOCSAGBytes(raw, baud)
	case args.SimSample:
		source = "sim"
		msgs, err = sensors.DecodePOCSAGBytes(sensors.SimPOCSAGFrame(baud), baud)
	default:
		return toolError("provide data_hex, words, or sim_sample=true")
	}
	if err != nil {
		return toolError(err.Error())
	}
	return jsonResult(map[string]any{
		"decoded": len(msgs) > 0, "baud_rate": baud, "count": len(msgs), "messages": msgs, "source": source,
	})
}

func (s *Server) toolDecodeWMBus(rawArgs json.RawMessage) (*toolCallResult, *rpcError) {
	var args struct {
		DataHex   string `json:"data_hex"`
		Mode      string `json:"mode"`
		SimSample bool   `json:"sim_sample"`
	}
	if err := decodeArgs(rawArgs, &args); err != nil {
		return nil, err
	}
	source := "input"
	var raw []byte
	var err error
	if args.DataHex != "" {
		raw, err = hex.DecodeString(strings.ReplaceAll(strings.TrimSpace(args.DataHex), " ", ""))
		if err != nil {
			return toolError("invalid data_hex: " + err.Error())
		}
	} else if args.SimSample {
		source = "sim"
		raw = sensors.SimWMBusFrame(args.Mode)
	} else {
		return toolError("provide data_hex or sim_sample=true")
	}
	frame, err := sensors.DecodeWMBus(raw, args.Mode)
	if err != nil {
		return toolError(err.Error())
	}
	return jsonResult(map[string]any{
		"decoded": true, "frame": frame, "source": source,
	})
}
