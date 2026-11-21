# Spec 01: Active Rolling-Code Detector & Extended OOK Decoders

**App**: `rf-heatmap` (`internal/subfile`, `internal/ui`, `internal/mcpserver`)  
**Status**: Ready for Implementation  
**Depends On**: Existing `subfile.DecodeOOK` (`internal/subfile/decoder.go`), `rfml.ClusterTimings` (`internal/rfml/cluster.go`), `ui.view_decode.go`

---

## 1. Overview & Motivation

Currently `rf-heatmap` decodes only 3 fixed-code OOK protocols (`Princeton`, `CAME`, `Linear`) and cannot warn a user when consecutive captures come from a **rolling-code** (hopping-code) transmitter such as `KeeLoq / HCS301` or a proprietary garage/automotive keyfob. Replaying a rolling-code frame fails or desynchronizes the receiver.

This specification adds:
1. An **Active Rolling-Code Detector** (`KeeLoq` / `HCS301` 66-bit frame parser + generic rolling-code heuristic comparing consecutive captures with matching preamble/serial/timing and changing encrypted payload).
2. **Five additional pure-Go OOK protocol decoders**: `EV1527`, `PT2262` (tri-state), `Holtek` (`HT12E` / `HT6P20B`), `Nice FLO` (12/24-bit), and `Somfy Telis` (56-bit Manchester RTS).
3. **UI Badges & Replay Guard** on the `DECODE` screen (`ROLLING CODE` amber/red badge + toast warning before TX).
4. **MCP Tool** `rf_analyze_rolling_code` for automated rolling-code detection over SSH stdio.

---

## 2. User Stories & Acceptance Criteria

### `US-RF-1.1`: Active Rolling-Code Detector (`KeeLoq` / `HCS301` & Generic Heuristic)
> **As a** hardware researcher capturing keyfob presses on CardputerZero,  
> **I want** `rf-heatmap` to automatically detect `KeeLoq`/`HCS301` frames and generic rolling-code transmitters across consecutive button presses,  
> **So that** I immediately see a `ROLLING CODE` warning instead of wasting time trying to replay a hopping code.

#### Acceptance Criteria (Given / When / Then)
- **AC-1.1.1 (KeeLoq / HCS301 Single-Frame Detection)**:
  - **Given** a signed `RAW_Data` pulse train containing a valid KeeLoq preamble (`>= 10` alternating `1×TE` pulses, `TE ∈ [250..500] µs`), header gap (`~10×TE` low), and `66` PWM bits (`1:2` / `2:1` ratio: 32-bit encrypted hop + 28-bit serial + 4-bit button + 2-bit status/VLOW),
  - **When** `subfile.DecodeOOK(pulses)` or `subfile.AnalyzeRollingCode(frames)` is called,
  - **Then** it returns `Protocol: "KeeLoq"`, `Bits: 66`, `IsRolling: true`, extracted `SerialHex` (28-bit), `Button` (4-bit), and `EncryptedHex` (32-bit hop code).
- **AC-1.1.2 (Generic Consecutive-Capture Rolling Heuristic)**:
  - **Given** `>= 2` consecutive captures (`frames[0]`, `frames[1]`) on the same frequency (`|Δf| <= 250 kHz`) with matching `TE` (`±15%`) and matching bit count (`>= 24` bits) where the static prefix/serial bits match (`>= 25%` of bits identical in the same positions) while the remaining payload bits change with Hamming distance `>= 3` bits,
  - **When** `RollingDetector.Observe(frame)` or `AnalyzeRollingCode(frames)` runs,
  - **Then** it classifies the sequence as `IsRolling: true`, `Kind: RollingGeneric`, `Confidence >= 0.80`, and returns `HammingDelta` and `StaticMaskHex`.
- **AC-1.1.3 (Fixed-Code Negative Case)**:
  - **Given** `>= 2` identical captures of a `Princeton` or `EV1527` remote (`HammingDelta == 0`),
  - **When** `AnalyzeRollingCode(frames)` runs,
  - **Then** `IsRolling` MUST be `false` and `Kind` MUST be `RollingNone`.

---

### `US-RF-1.2`: Extended OOK Protocol Decoders (`EV1527`, `PT2262`, `Holtek`, `Nice FLO`, `Somfy Telis`)
> **As a** Sub-GHz analyst,  
> **I want** `rf-heatmap` to decode `EV1527`, `PT2262` (tri-state), `Holtek`, `Nice FLO`, and `Somfy Telis` frames (with K-Means denoiser fallback),  
> **So that** common gates, blinds, doorbells, and outlets decode cleanly on-device.

#### Acceptance Criteria (Given / When / Then)
- **AC-1.2.1 (EV1527 — 24-bit 20-bit Address + 4-bit Data)**:
  - **Given** a 24-bit `1:3` / `3:1` PWM frame with sync gap `~31×TE` (`TE ∈ [180..450] µs`),
  - **When** decoded via `subfile.DecodeOOK(pulses)`,
  - **Then** `EV1527` is distinguished from generic `Princeton` when explicitly requested or annotated with `AddressHex` (20-bit) + `Button` (4-bit); existing `Princeton` tests MUST remain 100% backward-compatible.
- **AC-1.2.2 (PT2262 Tri-State)**:
  - **Given** a 24-bit `1:3` / `3:1` frame where every bit-pair forms valid tri-state symbols (`00='0'`, `11='1'`, `01='F'`),
  - **When** decoded,
  - **Then** `DecodedKey.TriState` exposes the 12-trit string (e.g. `"0F10FF01001F"`), preserving existing `Protocol: "Princeton"` compatibility when all pairs are valid or reporting `"PT2262"` when tri-state formatting is requested.
- **AC-1.2.3 (Holtek HT12E / HT6P20B)**:
  - **Given** a 12-bit (`HT12E`: `TE ∈ [300..500] µs`, pilot `~36×TE`, `1:2` / `2:1` inverted PWM) or 28-bit (`HT6P20B`: 22-bit addr + 2-bit data + 4-bit anti-code `0101`, `TE ∈ [400..550] µs`, `1:2` / `2:1`),
  - **When** decoded via `subfile.DecodeOOK(pulses)`,
  - **Then** it returns `Protocol: "Holtek"`, `Bits: 12` or `28`, and valid `KeyHex`.
- **AC-1.2.4 (Nice FLO 12-bit & 24-bit)**:
  - **Given** a Nice FLO frame (`TE ∈ [650..750] µs`, `1:2` / `2:1` PWM, sync gap `~36×TE`, `12` or `24` bits),
  - **When** decoded via `subfile.DecodeOOK(pulses)`,
  - **Then** it returns `Protocol: "Nice FLO"`, `Bits: 12` or `24`, `TE ~ 700`.
- **AC-1.2.5 (Somfy Telis RTS — 56-bit Manchester)**:
  - **Given** a Somfy Telis frame with hardware wake-up pulse (`+9415, -89565 µs` optional), `>= 2` hardware sync pulses (`+2560, -2560 µs`), software sync (`+4550, -640 µs`), and 56 Manchester-encoded bits (`TE ~ 640 µs`) with valid Somfy XOR obfuscation and 4-bit checksum,
  - **When** decoded via `subfile.DecodeOOK(pulses)`,
  - **Then** it de-obfuscates the 7-byte payload, verifies the 4-bit XOR checksum, sets `Protocol: "Somfy Telis"`, `Bits: 56`, `IsRolling: true`, `Counter` (16-bit), `SerialHex` (24-bit), and `Button` (4-bit).
- **AC-1.2.6 (K-Means Fallback Parity)**:
  - **Given** any of the above frames corrupted by `<= 15%` timing jitter and up to 4 `< 55 µs` EMI glitch spikes,
  - **When** `subfile.DecodeOOK(pulses)` runs,
  - **Then** the second-pass `rfml.ClusterTimings` + `rfml.QuantizePulses` fallback recovers the exact key with `Denoised: true`.

---

### `US-RF-1.3`: `DECODE` UI Rolling-Code Badge & Replay Guard
> **As a** CardputerZero user viewing the `320×170` `DECODE` screen,  
> **I want** a prominent `ROLLING CODE` badge and a replay-warning confirmation before transmitting a rolling-code capture,  
> **So that** I do not accidentally desync a vehicle or garage receiver.

#### Acceptance Criteria (Given / When / Then)
- **AC-1.3.1 (Visual Badge)**:
  - **Given** `LastDecoded != nil && LastDecoded.IsRolling` (or `RollingReport.IsRolling == true`),
  - **When** `drawDecodeBanner` renders on the `320×170` LCD,
  - **Then** a bordered pill badge `"ROLLING"` (`ColAmber` border/text on `ColPanelAlt`, `x=244..308, y=24..35` — shifting `"ML-CLEAN"` left to `x=176..240` if both are active) is displayed, and line 2 shows `SER:<hex> CNT/HOP:<hex>` instead of raw key bytes alone.
- **AC-1.3.2 (Replay Guard)**:
  - **Given** a rolling-code frame is selected on `DECODE` and the user presses `5 (TX)` / `Enter`,
  - **When** pressed the first time,
  - **Then** TX is NOT started; a 3-second toast `"ROLLING CODE! TX may desync - 5 again"` appears. Pressing `5 (TX)` again within 3 seconds forces TX (for authorized lab testing).
- **AC-1.3.3 (Decoder Registry Extensibility)**:
  - **Given** the Open/Closed Principle,
  - **When** decoders are invoked in `internal/subfile/`,
  - **Then** each protocol implements a small `OOKDecoder` strategy registered in a prioritized slice (no giant switch/if-else block).

---

### `US-RF-1.4`: MCP Tool `rf_analyze_rolling_code`
> **As an** AI agent connected over SSH `--mcp`,  
> **I want** to submit 1 or more captured pulse trains to `rf_analyze_rolling_code`,  
> **So that** I can programmatically classify KeeLoq/Somfy/generic rolling codes and inspect serials, counters, and Hamming deltas.

#### Acceptance Criteria (Given / When / Then)
- **AC-1.4.1 (Single-Frame KeeLoq/Somfy)**:
  - **Given** `frames: [[...pulses...]]` containing a KeeLoq or Somfy Telis frame,
  - **When** `rf_analyze_rolling_code` is invoked,
  - **Then** it returns JSON with `is_rolling: true`, `kind: "keeloq"` or `"somfy_telis"`, `protocol`, `serial_hex`, `button`, `encrypted_hex` (or `counter`), and `replay_warning`.
- **AC-1.4.2 (Multi-Frame Heuristic)**:
  - **Given** `frames: [capture1, capture2]` (`2..8` captures) with matching preamble/serial and changing encrypted bits,
  - **When** `rf_analyze_rolling_code` is invoked,
  - **Then** it returns `is_rolling: true`, `kind: "generic_heuristic"`, `hamming_deltas: [...]`, `static_bits`, `changing_bits`, and `confidence`.
- **AC-1.4.3 (Input Validation)**:
  - **Given** empty `frames`, `> 8` frames, or a frame exceeding `65536` pulses,
  - **When** `rf_analyze_rolling_code` is invoked,
  - **Then** it returns a clean MCP tool error (`isError: true`) without panicking.

---

## 3. Go Interfaces, Types & Package Decomposition

### File Layout (`<= 350` lines/file, `<= 40` lines/function)
- `internal/subfile/decoder.go` — `DecodedKey` struct, `OOKDecoder` interface, `DecodeOOK` coordinator with K-Means fallback (`<= 140` lines).
- `internal/subfile/decoders_fixed.go` — Existing `Princeton`/`PT2262`, `CAME`, `Linear` + new `EV1527`, `Holtek`, `Nice FLO` (`<= 280` lines).
- `internal/subfile/decoders_rolling.go` — `KeeLoq` (66-bit) and `Somfy Telis` (56-bit Manchester) decoders (`<= 260` lines).
- `internal/subfile/rolling.go` — `RollingKind`, `RollingReport`, `RollingDetector` ring buffer (`cap = 6`) for live consecutive-capture comparison (`<= 220` lines).
- `internal/ui/state.go` — Add `RollingState` sub-struct embedded in `ui.App`:
  ```go
  type RollingState struct {
      RollingDetector *subfile.RollingDetector
      LastRolling     subfile.RollingReport
      rollingTxUntil  time.Time
  }
  ```
- `internal/mcpserver/tools_rolling.go` — `toolAnalyzeRollingCode` handler (`<= 150` lines).
- `internal/mcpserver/schema_ext.go` — Schema definitions for new tools (`<= 200` lines).

### Exact Domain Types (`internal/subfile`)
```go
// DecodedKey represents a decoded OOK remote frame (fixed or rolling).
type DecodedKey struct {
	Protocol     string `json:"protocol"`
	Bits         int    `json:"bits"`
	KeyHex       string `json:"key_hex"`
	TE           int    `json:"te_us"`
	Denoised     bool   `json:"denoised,omitempty"`
	IsRolling    bool   `json:"is_rolling,omitempty"`
	SerialHex    string `json:"serial_hex,omitempty"`
	EncryptedHex string `json:"encrypted_hex,omitempty"`
	Counter      uint16 `json:"counter,omitempty"`
	Button       uint8  `json:"button,omitempty"`
	TriState     string `json:"tristate,omitempty"`
}

// OOKDecoder is a Strategy interface for single-frame OOK protocol decoders.
type OOKDecoder interface {
	Name() string
	Decode(pulses []int32) (DecodedKey, bool)
}

// RollingKind classifies how a rolling-code transmitter was identified.
type RollingKind string

const (
	RollingNone    RollingKind = "none"
	RollingKeeLoq  RollingKind = "keeloq"
	RollingSomfy   RollingKind = "somfy_telis"
	RollingGeneric RollingKind = "generic_heuristic"
)

// RollingReport summarizes rolling-code analysis across 1..N consecutive captures.
type RollingReport struct {
	IsRolling     bool        `json:"is_rolling"`
	Kind          RollingKind `json:"kind"`
	Protocol      string      `json:"protocol,omitempty"`
	SerialHex     string      `json:"serial_hex,omitempty"`
	Button        uint8       `json:"button,omitempty"`
	EncryptedHex  string      `json:"encrypted_hex,omitempty"`
	Counter       uint16      `json:"counter,omitempty"`
	FramesSeen    int         `json:"frames_seen"`
	HammingDeltas []int       `json:"hamming_deltas,omitempty"`
	StaticBits    int         `json:"static_bits,omitempty"`
	ChangingBits  int         `json:"changing_bits,omitempty"`
	Confidence    float64     `json:"confidence"`
	ReplayWarning string      `json:"replay_warning,omitempty"`
}

// RollingDetector tracks recent captures to detect consecutive hopping payloads.
type RollingDetector struct { /* unexported ring of up to 6 frames */ }

func NewRollingDetector() *RollingDetector
func (d *RollingDetector) Reset()
func (d *RollingDetector) Observe(freqHz uint32, pulses []int32) RollingReport
func AnalyzeRollingCode(freqHz uint32, frames [][]int32) RollingReport
```

---

## 4. MCP Tool Schema (`rf_analyze_rolling_code`)

```json
{
  "name": "rf_analyze_rolling_code",
  "description": "Analyze 1 to 8 Sub-GHz RAW_Data captures for KeeLoq/HCS301, Somfy Telis, or generic consecutive rolling-code behavior.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "freq_hz": {
        "type": "integer",
        "description": "Carrier frequency in Hz (default 433920000)",
        "minimum": 280000000,
        "maximum": 950000000
      },
      "frames": {
        "type": "array",
        "description": "1 to 8 captured RAW_Data pulse arrays (+high, -low in microseconds)",
        "minItems": 1,
        "maxItems": 8,
        "items": {
          "type": "array",
          "items": { "type": "integer" }
        }
      }
    },
    "required": ["frames"]
  }
}
```

---

## 5. `320×170` LCD Layout & Keybindings (`DECODE` Mode)

- **Header (`y=0..17`)**: Unchanged (`RF-MAP | DECODE | <freq> | <status> | CC1101/SIM`).
- **Banner (`x=6..314, y=21..63`)**:
  - Row 1 (`y=26`): `<Protocol> (<Bits>b TE=<TE>us)` in `ColGreen` (or `ColAmber` when `IsRolling`).
  - Top-right badges (`y=24..35`): `[ROLLING]` (`ColAmber`) at `x=248..308`; `[ML-CLEAN]` (`ColMagenta`) at `x=180..244` (or `x=244..308` when not rolling).
  - Row 2 (`y=41`): Fixed code → `KeyHex` (+ `TriState` if PT2262). Rolling code → `SER:<SerialHex> HOP:<EncryptedHex> BTN:<X>` or `SER:<SerialHex> CNT:<04X>`.
  - Row 3 (`y=52`): If `IsRolling`, warning hint `"! Rolling code - replay may desync"` in `ColAmber`.
- **Keybindings (`DECODE`)**:
  - `4 / Space`: `PAUSE` / `RUN`
  - `5 / Enter`: `TX` (2-step guard if `IsRolling`)
  - `6`: `SAVE` (saves `.sub` to `~/rf-heatmap/captures/`)
  - `7`: `CLR` (clears `DecodedLog` AND resets `RollingDetector`)
  - `8 / Tab`: Next mode

---

## 6. Edge Cases & Adversarial Test Plan

| Category | Test Cases |
| --- | --- |
| **Zero / Malformed Inputs** | `nil` / empty `pulses`, all-positive or all-negative pulses, `0 µs` pulses, `math.MinInt32` / `math.MaxInt32` durations, `frames` with 0 or 9 elements. |
| **Protocol Disambiguation** | `Princeton` vs `EV1527` vs `PT2262` (invalid trit `10` disables `TriState`), `Nice FLO` (`TE=700 µs`) vs `CAME` (`TE=320 µs`), `Holtek` 28-bit bad anti-code (`!= 0101`) MUST reject. |
| **Somfy Checksum** | Valid 56-bit Manchester frame with 1-bit flip MUST fail checksum verification; inverted polarity MUST fail cleanly. |
| **KeeLoq Edge Cases** | Truncated preamble (`< 10` pulses) MUST reject; 65-bit truncated payload MUST reject; valid 66-bit with `±12%` jitter + 3 EMI glitches (`35 µs`) MUST decode via K-Means fallback. |
| **Rolling Heuristic** | 2 identical frames → `IsRolling: false`; 2 completely unrelated noise frames (0% static prefix) → `IsRolling: false`; 2 frames with 32-bit static serial + 32-bit changing hop (`HammingDelta = 14`) → `IsRolling: true`; frequency mismatch (`433.92 MHz` vs `868.35 MHz`) → resets detector, `IsRolling: false`. |
| **Fuzzing** | `FuzzDecodeOOK(f *testing.F)` and `FuzzAnalyzeRollingCode(f *testing.F)` ensuring zero panics on arbitrary byte/int32 streams. |
| **UI & Concurrency** | Table-driven `ui` tests verifying 2-step rolling TX guard, badge placement, and `CLR` resetting `RollingDetector`; `go test -race -cover ./...` >= 90%. |
