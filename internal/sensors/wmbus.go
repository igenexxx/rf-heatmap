package sensors

import (
	"encoding/binary"
	"encoding/hex"
	"errors"
	"fmt"
	"strings"
)

const (
	wmbusCRCPoly  uint16 = 0x3D65 // EN 13757-4 CRC-16 polynomial
	wmbusMinBytes        = 12     // 10B Format A header + 2B Block 1 CRC
	wmbusMaxBytes        = 292
)

// WMBusFrame is a parsed EN 13757-4 Wireless M-Bus (T1/C1/S1, 868.95 MHz) telegram.
type WMBusFrame struct {
	Mode             string `json:"mode"`
	Length           uint8  `json:"length"`
	ControlField     uint8  `json:"control_field"`
	ManufacturerCode string `json:"manufacturer_code"`
	ManufacturerID   uint16 `json:"manufacturer_id"`
	Serial           string `json:"serial"`
	Version          uint8  `json:"version"`
	DeviceType       uint8  `json:"device_type"`
	DeviceTypeName   string `json:"device_type_name"`
	CIField          uint8  `json:"ci_field"`
	PayloadHex       string `json:"payload_hex"`
	CRCOK            bool   `json:"crc_ok"`
}

// WMBusCRC16 computes the EN 13757-4 CRC-16 (poly 0x3D65, init 0x0000, xorout 0xFFFF).
func WMBusCRC16(data []byte) uint16 {
	var crc uint16
	for _, b := range data {
		crc ^= uint16(b) << 8
		for range 8 {
			if crc&0x8000 != 0 {
				crc = (crc << 1) ^ wmbusCRCPoly
			} else {
				crc <<= 1
			}
		}
	}
	return ^crc
}

// DecodeManufacturer unpacks a 16-bit EN 13757 M-field into a 3-letter uppercase code (e.g. "KAM").
func DecodeManufacturer(m uint16) (string, error) {
	c1 := (m >> 10) & 0x1F
	c2 := (m >> 5) & 0x1F
	c3 := m & 0x1F
	if c1 < 1 || c1 > 26 || c2 < 1 || c2 > 26 || c3 < 1 || c3 > 26 {
		return "", fmt.Errorf("wmbus: invalid manufacturer ID 0x%04X", m)
	}
	return string([]byte{byte('A' + c1 - 1), byte('A' + c2 - 1), byte('A' + c3 - 1)}), nil
}

// EncodeManufacturer packs a 3-letter uppercase ASCII code into a 16-bit EN 13757 M-field.
func EncodeManufacturer(code string) (uint16, error) {
	up := strings.ToUpper(strings.TrimSpace(code))
	if len(up) != 3 {
		return 0, fmt.Errorf("wmbus: manufacturer code %q must be 3 letters", code)
	}
	var vals [3]uint16
	for i := range 3 {
		if up[i] < 'A' || up[i] > 'Z' {
			return 0, fmt.Errorf("wmbus: invalid manufacturer character %q", up[i])
		}
		vals[i] = uint16(up[i]-'A') + 1
	}
	return (vals[0] << 10) | (vals[1] << 5) | vals[2], nil
}

// WMBusDeviceTypeName maps an EN 13757-3 device type byte to a human-readable meter type.
func WMBusDeviceTypeName(devType uint8) string {
	switch devType {
	case 0x00:
		return "Other"
	case 0x01:
		return "Oil"
	case 0x02:
		return "Electricity"
	case 0x03:
		return "Gas"
	case 0x04:
		return "Heat"
	case 0x05:
		return "Steam"
	case 0x06:
		return "Warm Water"
	case 0x07:
		return "Water"
	case 0x08:
		return "Heat Cost Allocator"
	case 0x0E:
		return "Bus/System"
	case 0x16:
		return "Cold Water"
	case 0x1A:
		return "Smoke Detector"
	case 0x1B:
		return "Room Sensor"
	default:
		return fmt.Sprintf("Unknown(0x%02X)", devType)
	}
}

func normalizeWMBusMode(mode string) (string, error) {
	up := strings.ToUpper(strings.TrimSpace(mode))
	if up == "" {
		return "T1", nil
	}
	switch up {
	case "T1", "C1", "S1":
		return up, nil
	default:
		return "", fmt.Errorf("wmbus: unsupported mode %q (use T1, C1, or S1)", mode)
	}
}

func decodeWMBusSerialBCD(b []byte) (string, error) {
	if len(b) != 4 {
		return "", errors.New("wmbus: serial must be 4 BCD bytes")
	}
	var digits [8]byte
	for i := range 4 {
		v := b[3-i]
		hi := (v >> 4) & 0x0F
		lo := v & 0x0F
		if hi > 9 || lo > 9 {
			return "", fmt.Errorf("wmbus: invalid BCD byte 0x%02X in serial", v)
		}
		digits[i*2] = '0' + hi
		digits[i*2+1] = '0' + lo
	}
	return string(digits[:]), nil
}

// DecodeWMBus parses an EN 13757-4 Format A Wireless M-Bus frame (T1/C1/S1).
func DecodeWMBus(raw []byte, mode string) (WMBusFrame, error) {
	normMode, err := normalizeWMBusMode(mode)
	if err != nil {
		return WMBusFrame{}, err
	}
	if len(raw) < wmbusMinBytes || len(raw) > wmbusMaxBytes {
		return WMBusFrame{}, fmt.Errorf("wmbus: frame length %d out of [%d..%d] range", len(raw), wmbusMinBytes, wmbusMaxBytes)
	}
	lField := raw[0]
	if lField < 9 {
		return WMBusFrame{}, fmt.Errorf("wmbus: invalid L-field %d (minimum 9)", lField)
	}
	mID := binary.LittleEndian.Uint16(raw[2:4])
	mCode, err := DecodeManufacturer(mID)
	if err != nil {
		return WMBusFrame{}, err
	}
	serial, err := decodeWMBusSerialBCD(raw[4:8])
	if err != nil {
		return WMBusFrame{}, err
	}
	gotCRC := binary.BigEndian.Uint16(raw[10:12])
	wantCRC := WMBusCRC16(raw[0:10])
	crcOK := gotCRC == wantCRC

	var ci uint8
	var payload []byte
	if len(raw) >= 13 {
		ci = raw[12]
		payload = raw[13:]
		if len(payload) >= 2 {
			// If Block 2 CRC is appended at end, verify and strip it from payload display.
			blk2 := raw[12 : len(raw)-2]
			blk2CRC := binary.BigEndian.Uint16(raw[len(raw)-2:])
			if blk2CRC == WMBusCRC16(blk2) {
				payload = raw[13 : len(raw)-2]
			}
		}
	}

	return WMBusFrame{
		Mode:             normMode,
		Length:           lField,
		ControlField:     raw[1],
		ManufacturerCode: mCode,
		ManufacturerID:   mID,
		Serial:           serial,
		Version:          raw[8],
		DeviceType:       raw[9],
		DeviceTypeName:   WMBusDeviceTypeName(raw[9]),
		CIField:          ci,
		PayloadHex:       strings.ToUpper(hex.EncodeToString(payload)),
		CRCOK:            crcOK,
	}, nil
}

// SimWMBusFrame builds a valid EN 13757-4 Format A Wireless M-Bus meter telegram for testing/SIM.
func SimWMBusFrame(mode string) []byte {
	_ = mode
	mID, _ := EncodeManufacturer("KAM") // Kamstrup water meter
	hdr := []byte{
		0x11, // L-field: 17 bytes excluding CRCs (9B header + 8B block 2)
		0x44, // C-field: SND-NR
		byte(mID & 0xFF),
		byte(mID >> 8),
		0x78, 0x56, 0x34, 0x12, // Serial: 12345678 (little-endian BCD)
		0x02, // Version
		0x07, // Device type: Water
	}
	crc1 := WMBusCRC16(hdr)
	blk2 := []byte{
		0x7A,                   // CI-field: Short Transport Layer header
		0x2A, 0x00, 0x00, 0x00, // Access#, Status, Config
		0x04, 0x13, 0x42, 0x01, // VIF/DIF sample volume
	}
	crc2 := WMBusCRC16(blk2)

	frame := make([]byte, 0, len(hdr)+2+len(blk2)+2)
	frame = append(frame, hdr...)
	frame = append(frame, byte(crc1>>8), byte(crc1&0xFF))
	frame = append(frame, blk2...)
	frame = append(frame, byte(crc2>>8), byte(crc2&0xFF))
	return frame
}
