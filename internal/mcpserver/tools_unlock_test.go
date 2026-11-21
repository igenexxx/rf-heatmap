package mcpserver

import (
	"context"
	"encoding/hex"
	"path/filepath"
	"testing"

	"rf-heatmap/internal/cc1101"
	"rf-heatmap/internal/config"
	"rf-heatmap/internal/sensors"
)

type unlockFakeSPI struct {
	rxFIFO   []byte
	rxCursor int
}

func (f *unlockFakeSPI) Transfer(tx []byte) ([]byte, error) {
	rx := make([]byte, len(tx))
	if len(tx) == 0 {
		return rx, nil
	}
	hdr := tx[0]
	isRead := (hdr & 0x80) != 0
	addr := hdr & 0x3F
	if isRead && addr == 0x3F {
		for i := 1; i < len(rx); i++ {
			if f.rxCursor < len(f.rxFIFO) {
				rx[i] = f.rxFIFO[f.rxCursor]
				f.rxCursor++
			}
		}
		return rx, nil
	}
	if len(tx) >= 2 && isRead {
		switch addr {
		case 0x30:
			rx[1] = 0x00
		case 0x31:
			rx[1] = 0x14
		case 0x32: // FREQEST = +1 (+1587 Hz)
			rx[1] = 0x01
		case 0x33: // LQI = 24, CRC_OK=true
			rx[1] = 0x98
		case 0x34: // RSSI = -66 dBm
			rx[1] = 0x10
		case 0x35: // MARCSTATE = IDLE
			rx[1] = 0x01
		case 0x3B: // RXBYTES
			rx[1] = byte(len(f.rxFIFO))
		}
	}
	if !isRead && len(tx) == 1 && addr == 0x3A {
		f.rxCursor = 0
	}
	return rx, nil
}
func (f *unlockFakeSPI) SetRFSwitch(bool) error                       { return nil }
func (f *unlockFakeSPI) TransmitPulses(context.Context, []int32) error { return nil }
func (f *unlockFakeSPI) Close() error                                 { return nil }

func TestUnlockMCPToolsHardwareAndSim(t *testing.T) {
	cfgPath := filepath.Join(t.TempDir(), "config.json")
	spi := &unlockFakeSPI{rxFIFO: []byte{4, 'P', 'I', 'N', 'G', 0x10, 0x98}}
	radio, err := cc1101.Open(spi, cc1101.DefaultCrystalHz)
	if err != nil {
		t.Fatalf("cc1101.Open failed: %v", err)
	}
	defer radio.Close()

	hwSrv := NewServer(Options{
		Radio:          NewCC1101Backend(radio),
		CapturesDir:    t.TempDir(),
		ExtraSavedDirs: []string{},
		ConfigPath:     cfgPath,
		Settings:       config.DefaultSettings(),
		SettleDelay:    0,
	})

	// 1. rf_measure_freq_offset (hardware)
	res, isErr, rpcErr := callTool(t, hwSrv, "rf_measure_freq_offset", map[string]any{"freq_hz": 433_920_000, "samples": 2})
	if isErr || rpcErr != nil || res["source"] != "hardware" || int(res["offset_hz"].(float64)) != 1587 {
		t.Fatalf("rf_measure_freq_offset(hw) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 2. rf_set_tx_power (hardware + persist)
	res, isErr, rpcErr = callTool(t, hwSrv, "rf_set_tx_power", map[string]any{"dbm": 4, "freq_hz": 433_920_000})
	if isErr || rpcErr != nil || int(res["applied_dbm"].(float64)) != 5 || res["persisted"] != true {
		t.Fatalf("rf_set_tx_power = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}
	loaded, err := config.Load(cfgPath)
	if err != nil || loaded.TXPowerDBm != 5 {
		t.Fatalf("persisted TXPowerDBm = %d (err=%v), want 5", loaded.TXPowerDBm, err)
	}

	// 3. rf_wor_listen (hardware)
	res, isErr, rpcErr = callTool(t, hwSrv, "rf_wor_listen", map[string]any{"freq_hz": 433_920_000, "interval_ms": 250})
	if isErr || rpcErr != nil || res["source"] != "hardware" || res["woke"] != true {
		t.Fatalf("rf_wor_listen(hw) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 4. rf_send_packet guard + hardware TX
	if _, isErr, _ := callTool(t, hwSrv, "rf_send_packet", map[string]any{"freq_hz": 433_920_000, "text": "HI", "confirm_rf": false}); !isErr {
		t.Fatal("rf_send_packet must reject confirm_rf=false")
	}
	res, isErr, rpcErr = callTool(t, hwSrv, "rf_send_packet", map[string]any{"freq_hz": 433_920_000, "text": "HELLO", "confirm_rf": true})
	if isErr || rpcErr != nil || res["sent"] != true || res["source"] != "hardware" {
		t.Fatalf("rf_send_packet(hw) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 5. rf_recv_packet (hardware)
	res, isErr, rpcErr = callTool(t, hwSrv, "rf_recv_packet", map[string]any{"freq_hz": 433_920_000, "timeout_ms": 100})
	if isErr || rpcErr != nil || res["text"] != "PING" || res["crc_ok"] != true || res["source"] != "hardware" {
		t.Fatalf("rf_recv_packet(hw) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 6. rf_decode_pocsag (sim + input)
	res, isErr, rpcErr = callTool(t, hwSrv, "rf_decode_pocsag", map[string]any{"sim_sample": true, "baud_rate": 1200})
	if isErr || rpcErr != nil || res["decoded"] != true || int(res["count"].(float64)) != 1 {
		t.Fatalf("rf_decode_pocsag(sim) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}
	pocsagHex := hex.EncodeToString(sensors.SimPOCSAGFrame(2400))
	res, isErr, rpcErr = callTool(t, hwSrv, "rf_decode_pocsag", map[string]any{"data_hex": pocsagHex, "baud_rate": 2400})
	if isErr || rpcErr != nil || res["decoded"] != true {
		t.Fatalf("rf_decode_pocsag(hex) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// 7. rf_decode_wmbus (sim + input)
	res, isErr, rpcErr = callTool(t, hwSrv, "rf_decode_wmbus", map[string]any{"sim_sample": true, "mode": "T1"})
	if isErr || rpcErr != nil || res["decoded"] != true {
		t.Fatalf("rf_decode_wmbus(sim) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}
	wmbusHex := hex.EncodeToString(sensors.SimWMBusFrame("C1"))
	res, isErr, rpcErr = callTool(t, hwSrv, "rf_decode_wmbus", map[string]any{"data_hex": wmbusHex, "mode": "C1"})
	if isErr || rpcErr != nil || res["decoded"] != true {
		t.Fatalf("rf_decode_wmbus(hex) = (%+v, isErr=%v, rpcErr=%+v)", res, isErr, rpcErr)
	}

	// SIM mode smoke checks for hardware-backed tools.
	simSrv := NewServer(Options{CapturesDir: t.TempDir(), ExtraSavedDirs: []string{}, Settings: config.DefaultSettings()})
	for _, tc := range []struct {
		name string
		args map[string]any
	}{
		{"rf_measure_freq_offset", map[string]any{"freq_hz": 433_920_000}},
		{"rf_wor_listen", map[string]any{"freq_hz": 433_920_000}},
		{"rf_recv_packet", map[string]any{"freq_hz": 433_920_000}},
	} {
		r, isErr, rpcErr := callTool(t, simSrv, tc.name, tc.args)
		if isErr || rpcErr != nil || r["source"] != "sim" {
			t.Fatalf("%s(sim) failed: res=%+v isErr=%v rpcErr=%+v", tc.name, r, isErr, rpcErr)
		}
	}
}
