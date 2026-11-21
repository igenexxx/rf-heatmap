# Spec 05: CC1101 Hardware Packet Engine, Sub-GHz Pager Framing, POCSAG & Wireless M-Bus Decoders

**App**: `rf-heatmap` (`internal/cc1101`, `internal/sensors`, `internal/mcpserver`)  
**Status**: Ready for Implementation  
**Depends On**: `internal/cc1101`, `internal/sensors`, `internal/mcpserver`

---

## 1. Overview & Motivation

Until now, `rf-heatmap` operated the CC1101 exclusively in **asynchronous serial mode** (`PKTCTRL0 = 0x32`, bit-banging `GDO0`). However, the CC1101 includes a full **64-byte hardware TX/RX FIFO Packet Engine** with automatic Sync Word correlation, CRC-16 generation/checking, PN9 data whitening, and appended RSSI/LQI status bytes.

In addition, two widely deployed Sub-GHz digital protocols—**POCSAG** (alphanumeric/numeric pagers on `138–174 / 433–470 / 929–932 MHz`) and **Wireless M-Bus** (`wM-Bus T1/C1/S1` utility meters on `868.95 MHz`)—can be decoded cleanly in pure Go with zero external dependencies.

This specification adds:
1. **CC1101 Hardware Packet Engine (`internal/cc1101/packet.go`)**: 64-byte FIFO TX/RX with Sync Word, CRC-16, optional PN9 whitening, RSSI/LQI status append, and **guaranteed async-preset restoration** on exit.
2. **Pure-Go POCSAG Decoder (`internal/sensors/pocsag.go`)**: `512 / 1200 / 2400 bps`, BCH(31,21) + even parity verification & 1-bit error correction, 7-bit ASCII alphanumeric & 4-bit BCD numeric decoding, plus `SimPOCSAGFrame()`.
3. **Pure-Go Wireless M-Bus Decoder (`internal/sensors/wmbus.go`)**: `T1 / C1 / S1` (`868.95 MHz`) EN 13757-4 header parsing (L-field, C-field, 3-letter manufacturer ID, 8-digit BCD serial, version, device type, CI-field, CRC-16 `0x3D65`), plus `SimWMBusFrame()`.
4. **MCP Tools**: `rf_send_packet` (`confirm_rf: true`), `rf_recv_packet`, `rf_decode_pocsag`, `rf_decode_wmbus`.

---

## 2. User Stories & Acceptance Criteria

### `US-RF-5.1`: CC1101 Hardware Packet Engine & Sub-GHz Chat/Pager Framing
> **As a** Sub-GHz experimenter building reliable point-to-point pager/chat links between two CardputerZero units,  
> **I want** hardware-framed 64-byte FIFO packet TX/RX with Sync Word, CRC-16, whitening, and RSSI/LQI metadata,  
> **So that** I can exchange structured short messages without bit-banging timing jitter, while never breaking async OOK/Sweep modes.

#### Acceptance Criteria (Given / When / Then)
- **AC-5.1.1 (Packet Engine Register Configuration)**:
  - **Given** a `PacketConfig{FreqHz, Preset, SyncWord, Whitening, CRCEnable, MaxLen}`,
  - **When** `ConfigurePacketEngine(cfg)` is called,
  - **Then** it strobes `IDLE`, tunes `FreqHz`, sets `SYNC1/SYNC0` (default `0xD391`), `PKTLEN` (`1..61`), `PKTCTRL1 = 0x04` (`APPEND_STATUS=1`), `PKTCTRL0` (variable length `0x01` + optional `CRC_EN=0x04` + optional `WHITE_DATA=0x40`), `MDMCFG2` (`SYNC_MODE=3`, 30/32 bits), and `MCSM1 = 0x00` (auto-IDLE after TX/RX).
- **AC-5.1.2 (Safe 64-Byte FIFO TX & Guaranteed Restoration)**:
  - **Given** a payload of `1..61` bytes (leaving 1 byte for length + 2 bytes for RX status within the 64-byte FIFO),
  - **When** `SendPacket(ctx, cfg, payload)` executes,
  - **Then** it rejects empty or `> 61` byte payloads, flushes TX FIFO (`SFTX 0x3B`), burst-writes `[len, payload...]` to `FIFO (0x3F)`, strobes `STX (0x35)`, waits for `MARCSTATE == IDLE` or `ctx.Done()`, and **unconditionally restores** the previous async preset + `IDLE` via `defer`.
- **AC-5.1.3 (Safe 64-Byte FIFO RX & Overflow Handling)**:
  - **Given** `RecvPacket(ctx, cfg, timeout)` is called,
  - **When** `RXBYTES (0x3B)` indicates a complete frame (length byte + payload + 2 status bytes),
  - **Then** it returns `ReceivedPacket{Payload, RSSIDBm, LQI, CRCOK}`; if `RXBYTES & 0x80 != 0` (overflow), it flushes RX FIFO (`SFRX 0x3A`) and returns an error; on timeout or exit it **unconditionally restores** the previous async preset + `IDLE`.

---

### `US-RF-5.2`: Pure-Go POCSAG Decoder (`512 / 1200 / 2400 bps`)
> **As an** RF learner inspecting authorized local pager transmissions,  
> **I want** a pure-Go POCSAG batch decoder with BCH(31,21) verification, 1-bit error correction, and 7-bit alpha / 4-bit numeric decoding,  
> **So that** I can decode POCSAG frames reliably without external tools.

#### Acceptance Criteria (Given / When / Then)
- **AC-5.2.1 (BCH(31,21) + Even Parity)**:
  - **Given** any 32-bit POCSAG codeword with generator polynomial `0x769` and LSB even parity,
  - **When** `VerifyPOCSAGWord(word)` runs,
  - **Then** valid codewords pass with `errBits == 0`, any single-bit flip is corrected (`errBits == 1, ok == true`), and multi-bit corruptions fail (`ok == false`).
- **AC-5.2.2 (Batch Sync, RIC & Function Extraction)**:
  - **Given** a byte or word stream containing sync word `0x7CD215D8` followed by 8 frames × 2 codewords,
  - **When** `DecodePOCSAGBytes` / `DecodePOCSAGWords` runs,
  - **Then** idle words (`0x7A89C197`) are skipped, address codewords (`bit31 == 0`) yield `RIC = (addr18 << 3) | frameIdx` and `Function = (word >> 11) & 0x03`, and subsequent message codewords (`bit31 == 1`) concatenate 20-bit payloads into 7-bit LSB-first ASCII (`Text`) and 4-bit BCD (`Numeric`).
- **AC-5.2.3 (Round-Trip Simulator)**:
  - **Given** `SimPOCSAGFrame(baudRate)` for `512`, `1200`, or `2400` bps,
  - **When** passed to `DecodePOCSAGBytes`,
  - **Then** it decodes cleanly with `Valid == true`, `BCHErrors == 0`, and expected `RIC` and `Text`.

---

### `US-RF-5.3`: Pure-Go Wireless M-Bus (`wM-Bus T1/C1/S1`, `868.95 MHz`) Decoder
> **As an** IoT/RF diagnostics user inspecting my own `868.95 MHz` utility meters,  
> **I want** a pure-Go EN 13757-4 Wireless M-Bus frame parser,  
> **So that** I can extract manufacturer code, serial number, device type, CI-field, and CRC-16 status.

#### Acceptance Criteria (Given / When / Then)
- **AC-5.3.1 (EN 13757-4 Header & CRC-16 Verification)**:
  - **Given** a raw wM-Bus Format A byte frame (`L`, `C`, `M[2]`, `A[4]`, `Ver`, `DevType`, `CRC16[2]`, `CI`, `Payload...`),
  - **When** `DecodeWMBus(raw, mode)` runs,
  - **Then** it validates minimum length (`>= 12`), decodes the 3-letter manufacturer code (`M = (c1-'@')<<10 | (c2-'@')<<5 | (c3-'@')`), extracts 8-digit little-endian BCD serial, maps `DeviceType` to human-readable name (`Water`, `Heat`, `Electricity`, `Gas`, `Smoke`, etc.), extracts `CI-field`, and verifies EN 13757-4 CRC-16 (`poly 0x3D65`, `xorout 0xFFFF`).
- **AC-5.3.2 (Strict Rejection of Malformed Frames)**:
  - **Given** truncated frames, non-BCD nibbles (`0xA..0xF`) in serial, invalid manufacturer letters (`0` or `>26`), or corrupted CRC-16,
  - **When** `DecodeWMBus` runs,
  - **Then** it returns a descriptive error (or `CRCOK: false` when CRC fails as specified).

---

## 3. Exact Go Interfaces & File Layout

- `internal/cc1101/packet.go` (`<= 250` lines): `PacketConfig`, `ReceivedPacket`, `ConfigurePacketEngine`, `SendPacket`, `RecvPacket`.
- `internal/sensors/pocsag.go` (`<= 240` lines): `POCSAGMessage`, `EncodePOCSAGWord`, `VerifyPOCSAGWord`, `DecodePOCSAGWords`, `DecodePOCSAGBytes`, `SimPOCSAGFrame`.
- `internal/sensors/wmbus.go` (`<= 220` lines): `WMBusFrame`, `WMBusCRC16`, `EncodeManufacturer`, `DecodeManufacturer`, `DecodeWMBus`, `SimWMBusFrame`.
- `internal/mcpserver/schema_unlock.go` + `internal/mcpserver/tools_unlock.go`: schemas and handlers for all 7 Spec 04 & 05 MCP tools.

---

## 4. MCP Tool Schemas

1. **`rf_send_packet`**:
   - Inputs: `freq_hz` (`280000000..950000000`, required), `payload_hex` or `text` (1..61 bytes), `sync_word` (optional hex/int, default `0xD391`), `whitening` (bool, default `true`), `crc` (bool, default `true`), `confirm_rf` (`true` required).
2. **`rf_recv_packet`**:
   - Inputs: `freq_hz` (`280000000..950000000`, required), `sync_word` (optional), `whitening` (bool, default `true`), `crc` (bool, default `true`), `timeout_ms` (`50..5000`, default `500`), `sim_sample` (optional bool).
3. **`rf_decode_pocsag`**:
   - Inputs: `data_hex` (hex-encoded byte stream) or `words` (array of uint32), `baud_rate` (`512|1200|2400`, default `1200`), `sim_sample` (optional bool).
4. **`rf_decode_wmbus`**:
   - Inputs: `data_hex` (hex-encoded wM-Bus frame), `mode` (`T1|C1|S1`, default `T1`), `sim_sample` (optional bool).

---

## 5. Edge Cases & Adversarial Test Plan

| Category | Test Cases |
| --- | --- |
| **Packet Engine** | Empty payload, 61-byte max payload, 62-byte rejection; context cancellation during TX/RX; RX FIFO overflow (`0x80` bit); guaranteed restoration of `PKTCTRL0 = 0x32` + `IDLE` after both success and error. |
| **POCSAG** | Valid 512/1200/2400 bps batches; single-bit flip in every bit position `0..31` corrected by BCH(31,21); 2-bit/3-bit corruption rejected; empty/no-sync/truncated streams; `FuzzDecodePOCSAG`. |
| **wM-Bus** | Valid `T1`/`C1`/`S1` frames for `KAM` (Water), `TCH` (Heat), `LUG` (Electricity); 1-bit CRC corruption detected; invalid BCD nibble (`0xFA`) rejected; invalid manufacturer ID (`0x0000`, `0xFFFF`) rejected; `FuzzDecodeWMBus`. |
| **MCP Safety** | `rf_send_packet` rejects missing/`false` `confirm_rf` and payloads `> 61` bytes; all 4 tools tested in SIM & mock hardware modes with `-race`. |
