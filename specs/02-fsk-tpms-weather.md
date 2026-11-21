# Spec 02: CC1101 2-FSK/GFSK Profiles & TPMS / Weather Station Decoders

**App**: `rf-heatmap` (`internal/cc1101`, `internal/sensors`, `internal/ui`, `internal/mcpserver`)  
**Status**: Ready for Implementation  
**Depends On**: `internal/cc1101` (`cc1101.go`, `driver.go`), `internal/ui` (`state.go`, `app.go`), `internal/mcpserver`

---

## 1. Overview & Motivation

Many high-value Sub-GHz IoT devices—automotive **TPMS (Tire Pressure Monitoring System)** sensors (`433.92 MHz` EU / `315 MHz` US) and **433 MHz Weather Stations** (`Oregon Scientific`, `Acurite`, `LaCrosse`)—transmit using **2-FSK / GFSK** (typically `9.6–19.2 kbps`, `±19–47.6 kHz` deviation) or Manchester/PWM OOK.

Currently `cc1101.go` defines `PresetAM650`, `PresetAM270`, `PresetFM238`, and `PresetFM476`, but lacks a GFSK profile (`MDMCFG2[6:4] = 001`) and dedicated sensor-band register tuning, and `rf-heatmap` has no telemetry decoders or sensor UI view.

This specification adds:
1. **Clean CC1101 2-FSK & GFSK Register Profiles** (`PresetGFSK999` / `PresetGFSKTPMS`) with safe `PATABLE`, `FSCTRL1`, and `AGCCTRL` switching so toggling between ASK/OOK and 2-FSK/GFSK never breaks spectrum sweeps or OOK RX.
2. **Pure-Go Sensor Decoders (`internal/sensors`)**:
   - **TPMS**: Schrader EG53MA4 / Toyota PMV / Generic Manchester-FSK (`sensor_id`, `pressure_kpa`, `pressure_psi`, `temp_c`, `battery_ok`, `flags`).
   - **Weather Stations**: `Oregon Scientific` (v2.1/v3 Manchester), `Acurite` (5n1 / 606TX), and `LaCrosse` (TX141-Bv2) (`sensor_id`, `channel`, `temp_c`, `humidity_pct`, `battery_ok`).
3. **Dedicated `SENSORS` Sub-View / Mode** on the `320×170` LCD with live telemetry cards, unit toggle (`kPa/PSI`, `°C/°F`), and realistic **`SIM` synthetic TPMS & Weather frames**.
4. **MCP Tool** `rf_decode_sensor` for programmatic sensor decoding over SSH stdio.

---

## 2. User Stories & Acceptance Criteria

### `US-RF-2.1`: CC1101 2-FSK / GFSK Register Profiles & Safe Modem Switching
> **As a** Sub-GHz operator switching between OOK remotes, 2-FSK/GFSK TPMS sensors, and spectrum sweeps,  
> **I want** CC1101 register profiles for `ASK/OOK`, `2-FSK`, and `GFSK` to configure `MDMCFG4..2`, `DEVIATN`, `FSCTRL1`, `FOCCFG`, `AGCCTRL2..0`, `FREND1..0`, and `PATABLE` cleanly,  
> **So that** switching into FSK/GFSK and back to OOK/Sweep never leaves stale registers or degrades RSSI accuracy.

#### Acceptance Criteria (Given / When / Then)
- **AC-2.1.1 (New GFSK Profile SSOT)**:
  - **Given** `internal/cc1101/cc1101.go` defines `PresetSpec` as the SSOT,
  - **When** `PresetGFSK999` (`Name: "GFSK99"`, `SubFileName: "FuriHalSubGhzPresetGFSK9_99KbAsync"`, `MDMCFG2 = 0x10` GFSK async, `DEVIATN = 0x34` ~19.04 kHz, `DRATE ~ 9.997 kbps`, `RX BW ~ 101 kHz`) is added,
  - **Then** `GetPresetSpec`, `PresetCount`, and `PresetFromSubName` expose it consistently alongside `AM650`, `AM270`, `FM238`, and `FM476`.
- **AC-2.1.2 (FSK/GFSK PATABLE & AGC Correctness)**:
  - **Given** `Radio.ApplyPreset(p)` is called for any 2-FSK (`MDMCFG2 & 0x70 == 0x00`) or GFSK (`MDMCFG2 & 0x70 == 0x10`) preset,
  - **When** registers are written over SPI,
  - **Then** the radio MUST strobe `IDLE` (`0x36`) first, set `PATABLE[0] = txPowerMax` (`0xC0`) for both 2-FSK and GFSK (currently `driver.go:122` only checks `== 0x00` and would leave `PATABLE[0] = 0x00` for GFSK!), and write per-preset `FSCTRL1`, `AGCCTRL2`, `AGCCTRL1`, `AGCCTRL0`.
- **AC-2.1.3 (Zero Sweep Regression)**:
  - **Given** the user switches to `GFSK99` or `FM476` in `SENSORS` and then returns to `HEATMAP` or `PULSE` (`AM650`),
  - **When** `ApplyPreset(PresetAM650)` is called,
  - **Then** all OOK registers (`MDMCFG4..2`, `FSCTRL1`, `AGCCTRL2..0`, `FREND1..0`, `PATABLE = [0x00, 0xC0, ...]`) are fully restored and `StartReceive` + `ReadRSSIDBm` produce identical sweep behavior.

---

### `US-RF-2.2`: Pure-Go TPMS & Weather Station Decoders (`internal/sensors`)
> **As a** CardputerZero user monitoring local 433.92 / 315 MHz sensors,  
> **I want** pure-Go decoders for TPMS tire sensors and Oregon Scientific / Acurite / LaCrosse weather stations,  
> **So that** I can inspect tire pressure, temperature, humidity, and battery status without external tools.

#### Acceptance Criteria (Given / When / Then)
- **AC-2.2.1 (TPMS Decoder — 433.92 & 315 MHz)**:
  - **Given** a Manchester-encoded FSK/OOK pulse train or demodulated bit payload matching:
    - **Schrader** (`37–64` bits, preamble `0xF5` / Manchester sync, 24–28 bit ID, pressure in `2.5 kPa` steps, temp `°C - 50`, flags), OR
    - **Toyota / Generic TPMS** (`64–72` bits with valid CRC-8 polynomial `0x07` or `0x13`),
  - **When** `sensors.Decode(pulses, freqHz, preset)` is called,
  - **Then** it verifies checksum/CRC and returns `Reading{Kind: KindTPMS, Protocol, SensorID, PressureKPa, PressurePSI, TempC, BatteryOK, Flags}`.
- **AC-2.2.2 (Oregon Scientific Weather Decoder — v2.1 / v3)**:
  - **Given** an OOK Manchester pulse train (`TE ~ 488 µs`, `>= 16` preamble `'1'` bits, sync nibble `0xA`, sensor type ID e.g. `THGN132N` / `THGR122N`, channel `1..3`, BCD temperature + sign, BCD humidity `0..99%`, low-battery flag, and valid 8-bit nibble sum checksum),
  - **When** `sensors.Decode(pulses, freqHz, preset)` is called,
  - **Then** it verifies the checksum and returns `Reading{Kind: KindWeather, Protocol: "Oregon", SensorID, Channel, TempC, HumidityPct, BatteryOK}`.
- **AC-2.2.3 (Acurite & LaCrosse Weather Decoders)**:
  - **Given** an `Acurite 606TX / 5n1` OOK PWM frame (`32–56` bits, `TE ~ 200/400/500 µs`, valid parity/checksum) OR a `LaCrosse TX141` OOK PWM frame (`40` bits, 4 sync pulses `~830 µs`, `208/417 µs` bit timings, valid CRC/nibble check),
  - **When** `sensors.Decode(pulses, freqHz, preset)` is called,
  - **Then** it verifies integrity and returns a structured `Reading` with `Kind: KindWeather`, `TempC`, `HumidityPct` (when present), `Channel`, and `BatteryOK`.
- **AC-2.2.4 (Strict Checksum Rejection)**:
  - **Given** any TPMS or Weather frame with a 1-bit corruption in payload or checksum,
  - **When** `sensors.Decode` runs,
  - **Then** it MUST reject the frame (`ok == false`) — never emit garbage pressure/temperature values.

---

### `US-RF-2.3`: `SENSORS` Sub-View & `SIM` Synthetic Telemetry
> **As a** CardputerZero user,  
> **I want** a dedicated `SENSORS` sub-view accessible from `DECODE` (or tab cycle) that displays live decoded TPMS & Weather telemetry cards and works in `SIM` mode with realistic synthetic frames,  
> **So that** I can monitor sensors in the field and demo/test the UI without physical sensors present.

#### Acceptance Criteria (Given / When / Then)
- **AC-2.3.1 (Entry & Exit)**:
  - **Given** the user is on `DECODE` mode,
  - **When** pressing `R` (or softkey toggle `SENS` / `KeySensors`),
  - **Then** `App` enters the `SENSORS` sub-view (`SensorState.Active = true`), automatically tunes to the active sensor profile (`433.92 MHz GFSK99` for TPMS or `433.92 MHz AM650` for Weather, selectable with `B`/`M`), and `Esc` returns cleanly to `DECODE` while restoring the previous preset.
- **AC-2.3.2 (Telemetry Cards & Units)**:
  - **Given** decoded `sensors.Reading` entries exist (deduplicated by `Protocol + SensorID`, keeping most recent timestamp + `SeenCount` + `LastRSSIDBm`),
  - **When** rendered on the `320×170` LCD,
  - **Then** up to 4 live sensor rows display:
    - TPMS: `TPMS <ID>  234kPa(33.9)  26.5C  BAT:OK`
    - Weather: `OREG CH1   22.4C  48%RH  BAT:OK`
  - Pressing `5 (UNIT)` toggles Metric (`kPa / °C`) ↔ Imperial (`PSI / °F`).
- **AC-2.3.3 (`SIM` Mode Synthetic Frames)**:
  - **Given** `App.SimMode == true` (`Radio == nil`) and `SENSORS` sub-view is active,
  - **When** `Step()` advances,
  - **Then** `sensors.SimFrame(tick)` generates genuine encoded pulse trains (alternating TPMS Schrader 433.92 MHz, Oregon Scientific THGN132N, and LaCrosse TX141) that pass through the **real** `sensors.Decode()` pipeline (exercising the real decoders end-to-end, not mocking structs).

---

### `US-RF-2.4`: MCP Tool `rf_decode_sensor`
> **As an** AI agent connected via `--mcp`,  
> **I want** to decode TPMS and Weather station pulse trains or hex bitstreams using `rf_decode_sensor`,  
> **So that** I can extract structured tire pressure and environmental telemetry programmatically.

#### Acceptance Criteria (Given / When / Then)
- **AC-2.4.1 (Decode Pulses)**:
  - **Given** `pulses` (signed `µs` array) and optional `freq_hz` (default `433920000`) and `preset` (default `"GFSK99"`),
  - **When** `rf_decode_sensor` is called,
  - **Then** it returns `{ "decoded": true, "reading": { ... } }` on match, or `{ "decoded": false }` on non-sensor noise.
- **AC-2.4.2 (Hardware Live Sniff Option)**:
  - **Given** `pulses` is omitted and `sim_sample: true` is passed (for testing/demo),
  - **When** `rf_decode_sensor` is called,
  - **Then** it runs a synthetic sensor frame through the real decoder and returns the decoded `Reading` with `"source": "sim"`.

---

## 3. Go Interfaces, Types & Package Decomposition

### File Layout (`<= 350` lines/file, `<= 40` lines/function)
- `internal/cc1101/cc1101.go` — Add `PresetGFSK999` + per-preset `FSCTRL1`, `AGCCTRL2`, `AGCCTRL1`, `AGCCTRL0` fields in `PresetSpec` (`<= 210` lines).
- `internal/cc1101/driver.go` — Fix `ApplyPreset` PATABLE check to cover both 2-FSK (`0x00`) and GFSK (`0x10`) and write preset `FSCTRL1`/`AGCCTRL*` (`<= 240` lines).
- `internal/sensors/types.go` — `SensorKind`, `Reading`, `Decoder` interface, `Decode(pulses, freqHz, preset)` registry coordinator (`<= 120` lines).
- `internal/sensors/tpms.go` — Schrader & Toyota/Generic TPMS Manchester/FSK decoders + CRC-8 (`<= 260` lines).
- `internal/sensors/weather.go` — Oregon Scientific v2.1/v3, Acurite, and LaCrosse TX141 decoders (`<= 300` lines).
- `internal/sensors/sim.go` — Synthetic pulse-train generators for TPMS & Weather (`<= 160` lines).
- `internal/ui/view_sensors.go` — `SENSORS` sub-view key handler, tick step, and `320×170` renderer (`<= 220` lines).
- `internal/mcpserver/tools_sensors.go` — `toolDecodeSensor` handler (`<= 120` lines).

### Exact Domain Types (`internal/sensors`)
```go
package sensors

import "time"

type SensorKind string

const (
	KindTPMS    SensorKind = "tpms"
	KindWeather SensorKind = "weather"
)

// Reading is a verified, immutable telemetry frame from a Sub-GHz sensor.
type Reading struct {
	Kind        SensorKind `json:"kind"`
	Protocol    string     `json:"protocol"`
	SensorID    string     `json:"sensor_id"`
	FreqHz      uint32     `json:"freq_hz"`
	Channel     int        `json:"channel,omitempty"`
	PressureKPa float64    `json:"pressure_kpa,omitempty"`
	PressurePSI float64    `json:"pressure_psi,omitempty"`
	TempC       float64    `json:"temp_c"`
	HasHumidity bool       `json:"has_humidity,omitempty"`
	HumidityPct int        `json:"humidity_pct,omitempty"`
	BatteryOK   bool       `json:"battery_ok"`
	Flags       uint8      `json:"flags,omitempty"`
	RSSIDBm     int32      `json:"rssi_dbm,omitempty"`
	SeenAt      time.Time  `json:"seen_at,omitempty"`
	SeenCount   int        `json:"seen_count,omitempty"`
}

// Decoder is a Strategy interface implemented by each sensor protocol.
type Decoder interface {
	Name() string
	Decode(pulses []int32, freqHz uint32) (Reading, bool)
}

// Decode attempts all registered TPMS and Weather decoders against pulses.
func Decode(pulses []int32, freqHz uint32, preset string) (Reading, bool)
```

### UI State Sub-Struct (`internal/ui/state.go`)
```go
type SensorState struct {
	ShowSensors   bool
	SensorProfile int // 0=TPMS 433 GFSK, 1=TPMS 315 GFSK, 2=Weather 433 OOK
	ImperialUnits bool
	SensorEntries []sensors.Reading
	SensorIdx     int
	prevPreset    cc1101.Preset
	prevFreqHz    uint32
}
```

---

## 4. MCP Tool Schema (`rf_decode_sensor`)

```json
{
  "name": "rf_decode_sensor",
  "description": "Decode Sub-GHz TPMS (Schrader/Toyota) or Weather Station (Oregon Scientific, Acurite, LaCrosse) telemetry from signed microsecond pulse timings.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "pulses": {
        "type": "array",
        "description": "Signed pulse durations in microseconds (+high, -low)",
        "items": { "type": "integer" }
      },
      "freq_hz": {
        "type": "integer",
        "description": "Carrier frequency in Hz (default 433920000)",
        "minimum": 280000000,
        "maximum": 950000000
      },
      "preset": {
        "type": "string",
        "description": "Modem preset (AM650, AM270, FM238, FM476, GFSK99)"
      },
      "sim_sample": {
        "type": "boolean",
        "description": "If true and pulses omitted, decode a deterministic synthetic sensor frame"
      }
    }
  }
}
```

---

## 5. `320×170` LCD Layout & Keybindings (`SENSORS` Sub-View)

- **Header (`y=0..17`)**: Mode pill displays `SENSOR` (`ColCyan`); active freq (`433.92M` / `315.00M`) + active modem (`GFSK99` / `AM650`).
- **Profile Strip (`x=6..314, y=20..33`)**: `Profile: TPMS 433 (GFSK99)  [B:PROF  5:UNIT]`
- **Sensor Table (`x=6..314, y=36..140`)**: 4 rows × `25 px` height, each showing protocol badge (`TPMS` in `ColCyan` or `WTHR` in `ColGreen`), `SensorID`, pressure/humidity, temperature, battery badge (`OK` `ColGreen` / `LOW` `ColRed`), and age (`2s`).
- **Bottom Nav Bar (`y=143..169`)**: `[4:PAUSE | 5:UNIT | 6:PROF | 7:CLR | 8:BACK]`
- **Keybindings**:
  - `F/X` (`Up/Down`): Scroll sensor list
  - `B` / `6`: Cycle profile (`TPMS 433 GFSK99` → `TPMS 315 GFSK99` → `Weather 433 AM650`)
  - `5`: Toggle `Metric (kPa/°C)` ↔ `Imperial (PSI/°F)`
  - `7`: Clear sensor list
  - `Esc` / `8`: Exit `SENSORS` sub-view back to `DECODE`, restoring `prevPreset` and `prevFreqHz`

---

## 6. Edge Cases & Adversarial Test Plan

| Category | Test Cases |
| --- | --- |
| **CC1101 Register Parity** | Mock `cc1101.Transport` verifying `ApplyPreset(PresetGFSK999)` writes `MDMCFG2=0x10`, `PATABLE[0]=0xC0`, then `ApplyPreset(PresetAM650)` restores `MDMCFG2=0x30`, `PATABLE=[0x00,0xC0,...]` with zero stale registers. |
| **TPMS Validation** | Valid Schrader & Toyota frames at `433.92 MHz` and `315 MHz`; negative temperature (`-20.0 °C`), high pressure (`450 kPa`), low-battery flag; 1-bit CRC error MUST reject; `0 kPa` flat tire valid. |
| **Weather Validation** | Valid Oregon v2.1/v3 (`-15.5 °C`, `0%` and `99%` RH), Acurite, LaCrosse; bad nibble sum / bad parity MUST reject; invalid BCD digit (`0xA..0xF`) MUST reject. |
| **Round-Trip SIM Pipeline** | Every `sensors.SimFrame(i)` (`i = 0..15`) MUST decode cleanly through `sensors.Decode` with expected `Kind`, `Protocol`, and plausible physical ranges. |
| **Boundary & Fuzzing** | Empty/nil pulses, truncated Manchester stream, `> 65536` pulses, `FuzzDecodeSensor(f *testing.F)` ensuring zero panics. |
| **UI & MCP** | `SENSORS` entry/exit restores `Preset` and `ActiveFreq`; unit conversion math (`1 kPa = 0.1450377 PSI`, `°F = °C*9/5 + 32`); `go test -race -cover ./...` >= 90%. |
