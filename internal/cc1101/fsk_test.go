package cc1101

import "testing"

func TestGFSKPresetAndSafeModemRestore(t *testing.T) {
	if PresetCount() != 5 {
		t.Fatalf("PresetCount = %d, want 5", PresetCount())
	}
	p, ok := PresetFromSubName("FuriHalSubGhzPresetGFSK9_99KbAsync")
	if !ok || p != PresetGFSK999 {
		t.Fatalf("PresetFromSubName GFSK999 got (%v, %v)", p, ok)
	}
	p2, ok := PresetFromSubName("GFSK99")
	if !ok || p2 != PresetGFSK999 {
		t.Fatalf("PresetFromSubName GFSK99 got (%v, %v)", p2, ok)
	}

	if !IsFSKModulation(0x00) || !IsFSKModulation(0x10) || IsFSKModulation(0x30) {
		t.Fatal("IsFSKModulation classification mismatch")
	}

	paTrack := &paMockTransport{
		mockTransport: mockTransport{regs: make(map[uint8]uint8), partNum: 0x00, version: 0x14, rssi: 0x10},
	}
	r, err := Open(paTrack, DefaultCrystalHz)
	if err != nil {
		t.Fatalf("Open failed: %v", err)
	}
	defer r.Close()

	// Switch to GFSK999 and verify MDMCFG2, AGC, and PATABLE[0] == txPowerMax.
	if err := r.ApplyPreset(PresetGFSK999); err != nil {
		t.Fatalf("ApplyPreset(GFSK999) failed: %v", err)
	}
	if paTrack.regs[regMDMCFG2] != 0x10 {
		t.Fatalf("GFSK MDMCFG2 = 0x%02X, want 0x10", paTrack.regs[regMDMCFG2])
	}
	if paTrack.regs[regAGCCTRL2] != 0x43 || paTrack.regs[regAGCCTRL1] != 0x40 {
		t.Fatalf("GFSK AGC mismatch: CTRL2=0x%02X CTRL1=0x%02X", paTrack.regs[regAGCCTRL2], paTrack.regs[regAGCCTRL1])
	}
	if len(paTrack.lastPA) < 2 || paTrack.lastPA[0] != txPowerMax {
		t.Fatalf("GFSK PATABLE = % X, want PATABLE[0]=0x%02X", paTrack.lastPA, txPowerMax)
	}

	// Restore back to ASK/OOK AM650 and verify zero stale FSK registers.
	if err := r.ApplyPreset(PresetAM650); err != nil {
		t.Fatalf("ApplyPreset(AM650) failed: %v", err)
	}
	if paTrack.regs[regMDMCFG2] != 0x30 || paTrack.regs[regAGCCTRL2] != 0x07 || paTrack.regs[regAGCCTRL1] != 0x00 {
		t.Fatalf("AM650 restore mismatch: MDMCFG2=0x%02X AGC2=0x%02X AGC1=0x%02X",
			paTrack.regs[regMDMCFG2], paTrack.regs[regAGCCTRL2], paTrack.regs[regAGCCTRL1])
	}
	if paTrack.lastPA[0] != 0x00 || paTrack.lastPA[1] != txPowerMax {
		t.Fatalf("AM650 PATABLE = % X, want [00 C0 ...]", paTrack.lastPA)
	}
}

type paMockTransport struct {
	mockTransport
	lastPA []uint8
}

func (m *paMockTransport) Transfer(tx []byte) ([]byte, error) {
	if len(tx) == 9 && tx[0] == (regPATABLE|spiBurst) {
		m.lastPA = append([]uint8(nil), tx[1:]...)
	}
	return m.mockTransport.Transfer(tx)
}
