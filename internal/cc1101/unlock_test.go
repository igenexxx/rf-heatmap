package cc1101

import (
	"context"
	"testing"
	"time"
)

type unlockMockTransport struct {
	regs      map[uint8]uint8
	patable   []uint8
	strobes   []uint8
	txFIFO    []uint8
	rxFIFO    []uint8
	rxCursor  int
	statusMap map[uint8]uint8
}

func newUnlockMockTransport() *unlockMockTransport {
	return &unlockMockTransport{
		regs: make(map[uint8]uint8),
		statusMap: map[uint8]uint8{
			statusPartnum:   0x00,
			statusVersion:   0x14,
			statusRSSI:      0x00,
			statusFreqEst:   0x01, // +1587 Hz @ 26 MHz
			statusLQI:       0x9C, // CRC_OK=true, LQI=28
			statusMarcstate: marcstateIDLE,
			statusRXBytes:   0x00,
		},
	}
}

func (m *unlockMockTransport) Transfer(tx []byte) ([]byte, error) {
	rx := make([]byte, len(tx))
	if len(tx) == 0 {
		return rx, nil
	}
	hdr := tx[0]
	isRead := (hdr & spiRead) != 0
	isBurst := (hdr & spiBurst) != 0
	addr := hdr & 0x3F

	if len(tx) == 1 && !isRead {
		m.strobes = append(m.strobes, addr)
		if addr == strobeSFRX {
			m.rxCursor = 0
		}
		return rx, nil
	}
	if isRead && isBurst && addr >= 0x30 && addr <= 0x3D {
		rx[1] = m.statusMap[addr]
		return rx, nil
	}
	if isRead && addr == regFIFO {
		for i := 1; i < len(rx); i++ {
			if m.rxCursor < len(m.rxFIFO) {
				rx[i] = m.rxFIFO[m.rxCursor]
				m.rxCursor++
			}
		}
		return rx, nil
	}
	if !isRead {
		if addr == regPATABLE {
			m.patable = append([]uint8(nil), tx[1:]...)
		} else if addr == regFIFO {
			m.txFIFO = append([]uint8(nil), tx[1:]...)
		} else if len(tx) >= 2 {
			m.regs[addr] = tx[1]
		}
	}
	return rx, nil
}

func (m *unlockMockTransport) SetRFSwitch(bool) error                       { return nil }
func (m *unlockMockTransport) TransmitPulses(context.Context, []int32) error { return nil }
func (m *unlockMockTransport) Close() error                                 { return nil }

func TestFreqOffsetHzAndDecodeLQI(t *testing.T) {
	offsetCases := []struct {
		name string
		raw  uint8
		want int32
	}{
		{"zero", 0x00, 0},
		{"plus_one", 0x01, 1587},
		{"minus_one", 0xFF, -1587},
		{"max_pos", 0x7F, 201538},
		{"min_neg", 0x80, -203125},
	}
	for _, tc := range offsetCases {
		t.Run(tc.name, func(t *testing.T) {
			if got := FreqOffsetHz(tc.raw, DefaultCrystalHz); got != tc.want {
				t.Fatalf("FreqOffsetHz(0x%02X) = %d, want %d", tc.raw, got, tc.want)
			}
		})
	}
	if got := FreqOffsetHz(0x01, 0); got != 1587 {
		t.Fatalf("FreqOffsetHz(0x01, 0) fallback = %d, want 1587", got)
	}

	lqiCases := []struct {
		raw     uint8
		wantLQI uint8
		wantCRC bool
	}{
		{0x00, 0, false},
		{0x80, 0, true},
		{0xFF, 127, true},
		{0x55, 85, false},
	}
	for _, tc := range lqiCases {
		lqi, crc := DecodeLQI(tc.raw)
		if lqi != tc.wantLQI || crc != tc.wantCRC {
			t.Fatalf("DecodeLQI(0x%02X) = (%d,%v), want (%d,%v)", tc.raw, lqi, crc, tc.wantLQI, tc.wantCRC)
		}
	}

	tr := newUnlockMockTransport()
	radio, err := Open(tr, DefaultCrystalHz)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer radio.Close()
	off, err := radio.ReadFreqOffsetHz()
	if err != nil || off != 1587 {
		t.Fatalf("ReadFreqOffsetHz() = (%d, %v), want (1587, nil)", off, err)
	}
	lqi, crc, err := radio.ReadLQI()
	if err != nil || lqi != 28 || !crc {
		t.Fatalf("ReadLQI() = (%d, %v, %v), want (28, true, nil)", lqi, crc, err)
	}
}

func TestPATableLookupAndSetTXPower(t *testing.T) {
	bandCases := []struct {
		freq uint32
		dbm  int
		want uint8
	}{
		{315_000_000, -30, 0x12},
		{315_000_000, 10, 0xC2},
		{433_920_000, 0, 0x60},
		{433_920_000, 10, 0xC0},
		{868_350_000, 5, 0x81},
		{915_000_000, 0, 0x8E},
		{915_000_000, 99, 0xC0},  // clamped to +10
		{433_920_000, -99, 0x12}, // clamped to -30
	}
	for _, tc := range bandCases {
		got, _, err := PATableByte(tc.freq, tc.dbm)
		if err != nil || got != tc.want {
			t.Fatalf("PATableByte(%d, %d) = (0x%02X, %v), want 0x%02X", tc.freq, tc.dbm, got, err, tc.want)
		}
	}
	if _, _, err := PATableByte(100_000_000, 10); err == nil {
		t.Fatal("expected error for out-of-range frequency")
	}

	tr := newUnlockMockTransport()
	radio, err := Open(tr, DefaultCrystalHz)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer radio.Close()

	// ASK/OOK: PATABLE[0]=0x00, PATABLE[1]=paByte.
	if err := radio.SetTXPowerDBm(433_920_000, 0); err != nil {
		t.Fatalf("SetTXPowerDBm(OOK) failed: %v", err)
	}
	if len(tr.patable) < 2 || tr.patable[0] != 0x00 || tr.patable[1] != 0x60 {
		t.Fatalf("OOK PATABLE = %X, want [00 60 ...]", tr.patable)
	}

	// GFSK: PATABLE[0]=paByte, PATABLE[1]=paByte.
	if err := radio.ApplyPreset(PresetGFSK999); err != nil {
		t.Fatalf("ApplyPreset(GFSK999) failed: %v", err)
	}
	if err := radio.SetTXPowerDBm(868_350_000, 5); err != nil {
		t.Fatalf("SetTXPowerDBm(GFSK) failed: %v", err)
	}
	if len(tr.patable) < 2 || tr.patable[0] != 0x81 || tr.patable[1] != 0x81 {
		t.Fatalf("GFSK PATABLE = %X, want [81 81 ...]", tr.patable)
	}
}

func TestWORRegistersAndSequence(t *testing.T) {
	for _, bad := range []uint16{0, 9, 60001} {
		if _, _, _, err := WORRegisters(bad, DefaultCrystalHz); err == nil {
			t.Fatalf("WORRegisters(%d) expected error", bad)
		}
	}
	// <= 1890 ms -> WOR_RES=0; > 1890 ms -> WOR_RES=1.
	_, _, ctrl0, err := WORRegisters(500, DefaultCrystalHz)
	if err != nil || (ctrl0&0x03) != 0 {
		t.Fatalf("WORRegisters(500) = (ctrl=0x%02X, err=%v), want WOR_RES=0", ctrl0, err)
	}
	_, _, ctrl1, err := WORRegisters(5000, DefaultCrystalHz)
	if err != nil || (ctrl1&0x03) != 1 {
		t.Fatalf("WORRegisters(5000) = (ctrl=0x%02X, err=%v), want WOR_RES=1", ctrl1, err)
	}

	tr := newUnlockMockTransport()
	radio, err := Open(tr, DefaultCrystalHz)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer radio.Close()

	if err := radio.ConfigureWOR(1000); err != nil {
		t.Fatalf("ConfigureWOR failed: %v", err)
	}
	if err := radio.EnterWOR(); err != nil {
		t.Fatalf("EnterWOR failed: %v", err)
	}
	if tr.regs[regMCSM2] != worMCSM2Duty {
		t.Fatalf("MCSM2 = 0x%02X, want 0x%02X", tr.regs[regMCSM2], worMCSM2Duty)
	}
	lastStrobe := tr.strobes[len(tr.strobes)-1]
	if lastStrobe != strobeSWOR {
		t.Fatalf("last strobe = 0x%02X, want SWOR (0x%02X)", lastStrobe, strobeSWOR)
	}
}

func TestPacketEngineTXRXAndRestoration(t *testing.T) {
	tr := newUnlockMockTransport()
	radio, err := Open(tr, DefaultCrystalHz)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer radio.Close()

	cfg := PacketConfig{
		FreqHz:    433_920_000,
		Preset:    PresetGFSK999,
		SyncWord:  0xD391,
		Whitening: true,
		CRCEnable: true,
	}

	// Reject empty and >61 byte payloads.
	if err := radio.SendPacket(context.Background(), cfg, nil); err == nil {
		t.Fatal("expected error for empty payload")
	}
	if err := radio.SendPacket(context.Background(), cfg, make([]byte, MaxPacketPayloadBytes+1)); err == nil {
		t.Fatal("expected error for oversized payload")
	}

	// Valid TX writes [len, payload...] and restores async PKTCTRL0=0x32.
	payload := []byte("HELLO-CZ")
	if err := radio.SendPacket(context.Background(), cfg, payload); err != nil {
		t.Fatalf("SendPacket failed: %v", err)
	}
	if len(tr.txFIFO) != len(payload)+1 || tr.txFIFO[0] != uint8(len(payload)) {
		t.Fatalf("unexpected TX FIFO frame: %X", tr.txFIFO)
	}
	if tr.regs[regPKTCTRL0] != 0x32 {
		t.Fatalf("PKTCTRL0 not restored to async 0x32 after SendPacket: got 0x%02X", tr.regs[regPKTCTRL0])
	}

	// Valid RX parses [len, payload..., rssi, lqi|crc] and restores async PKTCTRL0=0x32.
	rxPayload := []byte("PONG")
	tr.rxFIFO = append([]uint8{uint8(len(rxPayload))}, rxPayload...)
	tr.rxFIFO = append(tr.rxFIFO, 0x10, 0x95) // RSSI=+8-74=-66dBm, CRC_OK=true, LQI=0x15 (21)
	tr.statusMap[statusRXBytes] = uint8(len(tr.rxFIFO))

	pkt, err := radio.RecvPacket(context.Background(), cfg, 100*time.Millisecond)
	if err != nil {
		t.Fatalf("RecvPacket failed: %v", err)
	}
	if string(pkt.Payload) != "PONG" || pkt.RSSIDBm != -66 || pkt.LQI != 21 || !pkt.CRCOK {
		t.Fatalf("unexpected RecvPacket: %+v", pkt)
	}
	if tr.regs[regPKTCTRL0] != 0x32 {
		t.Fatalf("PKTCTRL0 not restored to async 0x32 after RecvPacket: got 0x%02X", tr.regs[regPKTCTRL0])
	}

	// RX FIFO overflow (bit 7 set) must return error and restore async preset.
	tr.statusMap[statusRXBytes] = 0x80
	if _, err := radio.RecvPacket(context.Background(), cfg, 50*time.Millisecond); err == nil {
		t.Fatal("expected error on RX FIFO overflow")
	}
	if tr.regs[regPKTCTRL0] != 0x32 {
		t.Fatalf("PKTCTRL0 not restored after overflow: got 0x%02X", tr.regs[regPKTCTRL0])
	}
}
