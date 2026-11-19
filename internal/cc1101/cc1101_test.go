package cc1101

import (
	"errors"
	"testing"
)

func TestFreqRegisters(t *testing.T) {
	tests := []struct {
		name      string
		freqHz    uint32
		crystalHz uint32
		wantF2    uint8
		wantF1    uint8
		wantF0    uint8
		wantErr   bool
	}{
		{"433.92 MHz", 433_920_000, DefaultCrystalHz, 0x10, 0xB0, 0x71, false},
		{"315.00 MHz", 315_000_000, DefaultCrystalHz, 0x0C, 0x1D, 0x8A, false},
		{"868.35 MHz", 868_350_000, DefaultCrystalHz, 0x21, 0x65, 0xE8, false},
		{"915.00 MHz", 915_000_000, DefaultCrystalHz, 0x23, 0x31, 0x3B, false},
		{"zero crystal", 433_920_000, 0, 0, 0, 0, true},
		{"too low", 100_000_000, DefaultCrystalHz, 0, 0, 0, true},
		{"too high", 1_200_000_000, DefaultCrystalHz, 0, 0, 0, true},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			f2, f1, f0, err := FreqRegisters(tc.freqHz, tc.crystalHz)
			if (err != nil) != tc.wantErr {
				t.Fatalf("err=%v wantErr=%v", err, tc.wantErr)
			}
			if !tc.wantErr && (f2 != tc.wantF2 || f1 != tc.wantF1 || f0 != tc.wantF0) {
				t.Fatalf("got (%02X,%02X,%02X) want (%02X,%02X,%02X)", f2, f1, f0, tc.wantF2, tc.wantF1, tc.wantF0)
			}
		})
	}
}

func TestRSSIToDBm(t *testing.T) {
	tests := []struct {
		name string
		raw  uint8
		want int32
	}{
		{"zero", 0x00, -74},
		{"positive max", 0x7F, -11},
		{"negative min (0x80)", 0x80, -138},
		{"negative -20 (0xEC)", 0xEC, -84},
		{"strong 0x40", 0x40, -42},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			if got := RSSIToDBm(tc.raw); got != tc.want {
				t.Fatalf("RSSIToDBm(0x%02X)=%d want %d", tc.raw, got, tc.want)
			}
		})
	}
}

func TestSelectAntennaBand(t *testing.T) {
	tests := []struct {
		name         string
		freqHz       uint32
		wantRFSwitch bool
		wantGDO2     uint8
	}{
		{"315 MHz low", 315_000_000, false, gdo2ConstantHi},
		{"boundary 373.99 MHz", 373_999_999, false, gdo2ConstantHi},
		{"boundary 374.00 MHz", 374_000_000, true, gdo2ConstantLow},
		{"433.92 MHz mid", 433_920_000, true, gdo2ConstantLow},
		{"boundary 650.50 MHz", 650_500_000, true, gdo2ConstantHi},
		{"868.35 MHz high", 868_350_000, true, gdo2ConstantHi},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			got := SelectAntennaBand(tc.freqHz)
			if got.RFSwitchHigh != tc.wantRFSwitch || got.GDO2Register != tc.wantGDO2 {
				t.Fatalf("got %+v want RFSwitch=%v GDO2=0x%02X", got, tc.wantRFSwitch, tc.wantGDO2)
			}
		})
	}
}

type mockTransport struct {
	regs     map[uint8]uint8
	partNum  uint8
	version  uint8
	rssi     uint8
	rfSwitch bool
	failErr  error
}

func (m *mockTransport) Transfer(tx []byte) ([]byte, error) {
	if m.failErr != nil {
		return nil, m.failErr
	}
	rx := make([]byte, len(tx))
	if len(tx) == 2 && tx[0]&spiRead != 0 {
		addr := tx[0] & 0x3F
		switch addr {
		case statusPartnum:
			rx[1] = m.partNum
		case statusVersion:
			rx[1] = m.version
		case statusRSSI:
			rx[1] = m.rssi
		case statusMarcstate:
			rx[1] = marcstateTX
		}
	} else if len(tx) == 2 && tx[0]&spiRead == 0 {
		m.regs[tx[0]&0x3F] = tx[1]
	}
	return rx, nil
}

func (m *mockTransport) SetRFSwitch(high bool) error {
	m.rfSwitch = high
	return m.failErr
}

func (m *mockTransport) Close() error { return nil }

func TestRadioLifecycle(t *testing.T) {
	tr := &mockTransport{regs: make(map[uint8]uint8), partNum: 0x00, version: 0x14, rssi: 0x10}
	r, err := Open(tr, DefaultCrystalHz)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	if info := r.Info(); info.Version != 0x14 {
		t.Fatalf("unexpected version 0x%02X", info.Version)
	}
	if err := r.StartReceive(433_920_000); err != nil {
		t.Fatalf("StartReceive failed: %v", err)
	}
	if !tr.rfSwitch {
		t.Fatal("expected RF_SW0 high for 433.92 MHz")
	}
	dbm, err := r.ReadRSSIDBm()
	if err != nil || dbm != -66 {
		t.Fatalf("ReadRSSIDBm got (%d, %v), want (-66, nil)", dbm, err)
	}

	// Missing chip should fail identification.
	badTr := &mockTransport{regs: make(map[uint8]uint8), version: 0xFF}
	if _, err := Open(badTr, DefaultCrystalHz); err == nil {
		t.Fatal("expected error for missing CC1101")
	}

	// Nil transport should fail.
	if _, err := Open(nil, DefaultCrystalHz); err == nil {
		t.Fatal("expected error for nil transport")
	}

	// Transport error propagates.
	tr.failErr = errors.New("spi bus error")
	if _, err := r.ReadRSSIDBm(); err == nil {
		t.Fatal("expected SPI error")
	}
}
