package cc1101

import (
	"context"
	"errors"
	"fmt"
	"time"
)

const (
	// Hardware Packet Engine registers, status bytes, and FIFO strobes.
	regSYNC1    uint8 = 0x04
	regSYNC0    uint8 = 0x05
	regPKTLEN   uint8 = 0x06
	regPKTCTRL1 uint8 = 0x07
	regMCSM1    uint8 = 0x17
	regFIFO     uint8 = 0x3F

	statusTXBytes uint8 = 0x3A
	statusRXBytes uint8 = 0x3B

	strobeSFRX uint8 = 0x3A
	strobeSFTX uint8 = 0x3B

	marcstateIDLE uint8 = 0x01

	// DefaultPacketSyncWord is the TI CC1101 default 16-bit sync word (0xD391).
	DefaultPacketSyncWord uint16 = 0xD391
	// MaxPacketPayloadBytes is the max payload length fitting in the 64B FIFO with 1B length + 2B appended status.
	MaxPacketPayloadBytes = 61
)

// PacketConfig configures the CC1101 64-byte hardware FIFO Packet Engine.
type PacketConfig struct {
	FreqHz    uint32
	Preset    Preset
	SyncWord  uint16 // 0 defaults to DefaultPacketSyncWord (0xD391)
	Whitening bool
	CRCEnable bool
	MaxLen    uint8 // 0 defaults to MaxPacketPayloadBytes (61)
}

// ReceivedPacket holds a hardware-demodulated CC1101 packet and appended status metadata.
type ReceivedPacket struct {
	Payload []byte `json:"payload"`
	RSSIDBm int32  `json:"rssi_dbm"`
	LQI     uint8  `json:"lqi"`
	CRCOK   bool   `json:"crc_ok"`
}

func normalizePacketConfig(cfg PacketConfig) (PacketConfig, error) {
	if cfg.FreqHz < 280_000_000 || cfg.FreqHz > 950_000_000 {
		return cfg, fmt.Errorf("cc1101: packet freq %d Hz out of 280-950 MHz range", cfg.FreqHz)
	}
	if cfg.SyncWord == 0 {
		cfg.SyncWord = DefaultPacketSyncWord
	}
	if cfg.MaxLen == 0 {
		cfg.MaxLen = MaxPacketPayloadBytes
	}
	if cfg.MaxLen > MaxPacketPayloadBytes {
		return cfg, fmt.Errorf("cc1101: packet max_len %d exceeds %d-byte FIFO limit", cfg.MaxLen, MaxPacketPayloadBytes)
	}
	return cfg, nil
}

func (r *Radio) readBurst(addr uint8, n int) ([]uint8, error) {
	if n <= 0 {
		return nil, nil
	}
	tx := make([]uint8, 1+n)
	tx[0] = addr | spiRead | spiBurst
	rx, err := r.tr.Transfer(tx)
	if err != nil {
		return nil, err
	}
	if len(rx) < 1+n {
		return nil, errors.New("cc1101: short SPI burst read")
	}
	out := make([]uint8, n)
	copy(out, rx[1:1+n])
	return out, nil
}

func (r *Radio) readFIFOByte() (uint8, error) {
	rx, err := r.tr.Transfer([]byte{regFIFO | spiRead, 0x00})
	if err != nil {
		return 0, err
	}
	if len(rx) < 2 {
		return 0, errors.New("cc1101: short FIFO byte read")
	}
	return rx[1], nil
}

// ConfigurePacketEngine switches the CC1101 from async serial into 64B FIFO variable-length packet mode.
func (r *Radio) ConfigurePacketEngine(cfg PacketConfig) error {
	norm, err := normalizePacketConfig(cfg)
	if err != nil {
		return err
	}
	if err := r.ApplyPreset(norm.Preset); err != nil {
		return err
	}
	if err := r.Tune(norm.FreqHz); err != nil {
		return err
	}
	spec := GetPresetSpec(norm.Preset)
	pktctrl0 := uint8(0x01) // normal FIFO mode, variable packet length
	if norm.CRCEnable {
		pktctrl0 |= 0x04
	}
	if norm.Whitening {
		pktctrl0 |= 0x40
	}
	mdmcfg2 := (spec.MDMCFG2 & 0x70) | 0x03 // preserve modulation, enable 30/32 sync word
	regs := [][2]uint8{
		{regSYNC1, uint8(norm.SyncWord >> 8)},
		{regSYNC0, uint8(norm.SyncWord & 0xFF)},
		{regPKTLEN, norm.MaxLen},
		{regPKTCTRL1, 0x04}, // APPEND_STATUS=1 (RSSI + LQI/CRC_OK)
		{regPKTCTRL0, pktctrl0},
		{regMDMCFG2, mdmcfg2},
		{regMCSM1, 0x00}, // return to IDLE after TX/RX
	}
	for _, rv := range regs {
		if err := r.writeReg(rv[0], rv[1]); err != nil {
			return err
		}
	}
	return nil
}

// SendPacket transmits a variable-length payload (1..61 bytes) using the CC1101 64B TX FIFO
// and unconditionally restores the previous async preset and IDLE state before returning.
func (r *Radio) SendPacket(ctx context.Context, cfg PacketConfig, payload []byte) error {
	if len(payload) == 0 || len(payload) > MaxPacketPayloadBytes {
		return fmt.Errorf("cc1101: payload length %d must be 1..%d bytes", len(payload), MaxPacketPayloadBytes)
	}
	prevPreset := r.preset
	defer func() {
		_ = r.Idle()
		_ = r.strobe(strobeSFTX)
		_ = r.ApplyPreset(prevPreset)
	}()

	if err := r.ConfigurePacketEngine(cfg); err != nil {
		return err
	}
	if err := r.strobe(strobeSFTX); err != nil {
		return err
	}
	frame := make([]uint8, 1+len(payload))
	frame[0] = uint8(len(payload))
	copy(frame[1:], payload)
	if err := r.writeBurst(regFIFO, frame); err != nil {
		return err
	}
	if err := r.strobe(strobeTX); err != nil {
		return err
	}
	return r.waitForIdle(ctx, 2*time.Second)
}

func (r *Radio) waitForIdle(ctx context.Context, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return err
		}
		st, err := r.readStatus(statusMarcstate)
		if err != nil {
			return err
		}
		if (st & 0x1F) == marcstateIDLE {
			return nil
		}
		if time.Now().After(deadline) {
			return errors.New("cc1101: timed out waiting for packet TX IDLE")
		}
		time.Sleep(time.Millisecond)
	}
}

// RecvPacket listens in 64B RX FIFO mode up to timeout and returns the received packet with RSSI/LQI/CRC.
// Unconditionally restores the previous async preset and IDLE state before returning.
func (r *Radio) RecvPacket(ctx context.Context, cfg PacketConfig, timeout time.Duration) (ReceivedPacket, error) {
	if timeout <= 0 {
		timeout = 500 * time.Millisecond
	}
	prevPreset := r.preset
	defer func() {
		_ = r.Idle()
		_ = r.strobe(strobeSFRX)
		_ = r.ApplyPreset(prevPreset)
	}()

	if err := r.ConfigurePacketEngine(cfg); err != nil {
		return ReceivedPacket{}, err
	}
	if err := r.strobe(strobeSFRX); err != nil {
		return ReceivedPacket{}, err
	}
	if err := r.strobe(strobeRX); err != nil {
		return ReceivedPacket{}, err
	}
	return r.pollRXFIFO(ctx, timeout)
}

func (r *Radio) pollRXFIFO(ctx context.Context, timeout time.Duration) (ReceivedPacket, error) {
	deadline := time.Now().Add(timeout)
	for {
		if err := ctx.Err(); err != nil {
			return ReceivedPacket{}, err
		}
		rxBytes, err := r.readStatus(statusRXBytes)
		if err != nil {
			return ReceivedPacket{}, err
		}
		if rxBytes&0x80 != 0 {
			_ = r.strobe(strobeSFRX)
			return ReceivedPacket{}, errors.New("cc1101: RX FIFO overflow")
		}
		avail := int(rxBytes & 0x7F)
		if avail >= 3 {
			return r.readPacketFromFIFO(avail)
		}
		if time.Now().After(deadline) {
			return ReceivedPacket{}, errors.New("cc1101: RX packet timeout")
		}
		time.Sleep(time.Millisecond)
	}
}

func (r *Radio) readPacketFromFIFO(avail int) (ReceivedPacket, error) {
	pktLen, err := r.readFIFOByte()
	if err != nil {
		return ReceivedPacket{}, err
	}
	if pktLen == 0 || int(pktLen) > MaxPacketPayloadBytes {
		return ReceivedPacket{}, fmt.Errorf("cc1101: invalid RX packet length %d", pktLen)
	}
	need := int(pktLen) + 2
	if avail-1 < need {
		return ReceivedPacket{}, fmt.Errorf("cc1101: truncated RX FIFO (%d < %d)", avail-1, need)
	}
	buf, err := r.readBurst(regFIFO, need)
	if err != nil {
		return ReceivedPacket{}, err
	}
	lqi, crcOK := DecodeLQI(buf[pktLen+1])
	return ReceivedPacket{
		Payload: buf[:pktLen],
		RSSIDBm: RSSIToDBm(buf[pktLen]),
		LQI:     lqi,
		CRCOK:   crcOK,
	}, nil
}
