# Spec 04: CC1101 Frequency Drift Meter (`FREQEST`/`LQI`), TX Power Control (`PATABLE`), and Wake-On-Radio (`WOR`)

**App**: `rf-heatmap` (`internal/cc1101`, `internal/config`, `internal/ui`, `internal/mcpserver`)  
**Status**: Ready for Implementation  
**Depends On**: `internal/cc1101` (`cc1101.go`, `driver.go`, `fsk.go`), `internal/config`, `internal/ui`, `internal/mcpserver`

---

## 1. Overview & Motivation

The TI CC1101 transceiver exposes hardware telemetry, RF power-amplifier calibration, and autonomous duty-cycled reception features that are not yet surfaced in `rf-heatmap`:

1. **Frequency Offset & Link Quality Meter (`FREQEST 0x32` + `LQI 0x33`)**: Measures carrier frequency drift of a received transmitter in Hz (two's complement) and reads demodulator Link Quality Indicator (`0..127`) plus `CRC_OK` bit.
2. **Band-Calibrated TX Power Control (`PATABLE 0x3E`)**: Allows users and AI agents to step TX output power across `{-30, -20, -10, 0, +5, +10} dBm` using TI DN013 band-specific `PATABLE` register values (`315 / 433 / 868 / 915 MHz`) persisted in `config.json`.
3. **Wake-On-Radio (`WOR`: `WOREVT1/0`, `MCSM2`, `WORCTRL`, `SWOR`)**: Configures autonomous low-power periodic RX polling (`10 ms – 60000 ms`) so CardputerZero can monitor quiet channels with minimal current draw.

---

## 2. User Stories & Acceptance Criteria

### `US-RF-4.1`: Frequency Drift (`FREQEST`) & Link Quality (`LQI`) Telemetry
> **As a** Sub-GHz RF engineer diagnosing crystal drift on keyfobs or sensors,  
> **I want** `rf-heatmap` to read CC1101 `FREQEST (0x32)` and `LQI (0x33)` and display frequency offset in Hz/kHz,  
> **So that** I can spot off-frequency transmitters without external SDR equipment.

#### Acceptance Criteria (Given / When / Then)
- **AC-4.1.1 (Signed Two's Complement Math)**:
  - **Given** `FREQEST (0x32)` returns raw byte `raw`,
  - **When** `FreqOffsetHz(raw, crystalHz)` computes $\Delta f = \text{round}(\text{int8}(raw) \cdot f_{\text{xosc}} / 2^{14})$,
  - **Then** for $f_{\text{xosc}} = 26\text{ MHz}$: `0x00 -> 0 Hz`, `0x01 -> +1587 Hz`, `0xFF -> -1587 Hz`, `0x7F -> +201538 Hz`, `0x80 -> -203125 Hz`.
- **AC-4.1.2 (LQI & CRC_OK Extraction)**:
  - **Given** `LQI (0x33)` returns raw byte `raw`,
  - **When** `Radio.ReadLQI()` executes,
  - **Then** it returns `lqi = raw & 0x7F` (`0..127`) and `crcOK = (raw & 0x80) != 0`.
- **AC-4.1.3 (UI Surface)**:
  - **Given** the user is on `PULSE` or `DECODE` and a live frequency offset reading is available,
  - **When** the frame renders on the `320×170` LCD,
  - **Then** a compact `DF:+1.6kHz` (or `DF:-3.2kHz`) drift badge is shown without overlapping existing telemetry.

---

### `US-RF-4.2`: Band-Calibrated TX Power Control (`PATABLE`)
> **As a** CardputerZero user testing close-range devices or complying with local ERP limits,  
> **I want** to select TX output power (`-30, -20, -10, 0, +5, +10 dBm`) in `SET` and via MCP,  
> **So that** I can attenuate transmissions safely on `315 / 433 / 868 / 915 MHz` bands.

#### Acceptance Criteria (Given / When / Then)
- **AC-4.2.1 (Datasheet PATABLE Lookup)**:
  - **Given** carrier frequency `freqHz` (`280–950 MHz`) and desired power `dbm`,
  - **When** `SetTXPowerDBm(freqHz, dbm)` is called,
  - **Then** `dbm` is clamped to the nearest supported step in `{-30, -20, -10, 0, +5, +10}`, mapped via the TI DN013 band table (`<374` = 315 MHz, `374..650.5` = 433 MHz, `650.5..890` = 868 MHz, `>=890` = 915 MHz), and written to `PATABLE (0x3E)` respecting ASK/OOK (`[0x00, pa]`) vs 2-FSK/GFSK (`[pa, pa]`).
- **AC-4.2.2 (Settings Persistence & Safe Default)**:
  - **Given** `config.Settings` adds `TXPowerDBm int` (`json:"tx_power_dbm"`),
  - **When** `DefaultSettings()` or `Normalize()` runs on missing or out-of-range JSON values,
  - **Then** missing/zero-struct defaults safely to `+10 dBm` (preserving existing behavior for legacy `config.json` files where `tx_power_dbm` is absent, while explicitly supporting `0 dBm`), and invalid integers clamp to the nearest allowed step.
- **AC-4.2.3 (SET UI Control)**:
  - **Given** the user opens `SET`,
  - **When** navigating to the `TX Power` row and pressing `Enter`/`Space`/`Z`/`C`,
  - **Then** power cycles through `-30, -20, -10, 0, +5, +10 dBm`, updates the radio (if attached), persists `config.json`, and displays a toast `TX Power: +5 dBm`.

---

### `US-RF-4.3`: Wake-On-Radio (`WOR`) Duty-Cycled Polling
> **As an** operator monitoring sparse Sub-GHz beacons on battery power,  
> **I want** to configure CC1101 Wake-On-Radio (`WOREVT1/0`, `MCSM2`, `WORCTRL`) and strobe `SWOR (0x38)`,  
> **So that** the radio sleeps between periodic wakeups and reports any RSSI wake trigger.

#### Acceptance Criteria (Given / When / Then)
- **AC-4.3.1 (WOR Timing Math)**:
  - **Given** `intervalMs` in `[10..60000]` ms and $f_{\text{xosc}} = 26\text{ MHz}$,
  - **When** `WORRegisters(intervalMs, crystalHz)` computes `EVENT0` and `WOR_RES`,
  - **Then** $t_{\text{event0}} = \frac{750}{f_{\text{xosc}}} \cdot \text{EVENT0} \cdot 2^{5 \cdot \text{WOR\_RES}}$ matches `intervalMs` within `±2%` (`WOR_RES = 0` for `<= 1890 ms`, `WOR_RES = 1` for `1891..60000 ms`).
- **AC-4.3.2 (Register & Strobe Sequence)**:
  - **Given** `ConfigureWOR(intervalMs)` followed by `EnterWOR()`,
  - **When** executed over SPI,
  - **Then** the driver strobes `SIDLE (0x36)`, writes `MCSM2 (0x16)`, `WOREVT1 (0x1E)`, `WOREVT0 (0x1F)`, `WORCTRL (0x20)` (`EVENT1=7`, `RC_CAL=1`, `WOR_RES`), strobes `SWORRST (0x3C)`, and `EnterWOR()` strobes `SWOR (0x38)`.
- **AC-4.3.3 (Out-of-Range Rejection)**:
  - **Given** `intervalMs < 10` or `intervalMs > 60000`,
  - **When** `ConfigureWOR` is called,
  - **Then** it returns a descriptive error without touching SPI registers.

---

## 3. Register Reference & Exact Go Interfaces

### TI DN013 `PATABLE` Lookup Table
| Band | Frequency Split | `-30 dBm` | `-20 dBm` | `-10 dBm` | `0 dBm` | `+5 dBm` | `+10 dBm` |
| --- | --- | --- | --- | --- | --- | --- | --- |
| **315 MHz** | `< 374.0 MHz` | `0x12` | `0x0D` | `0x1C` | `0x51` | `0x85` | `0xC2` |
| **433 MHz** | `374.0 – 650.5 MHz` | `0x12` | `0x0E` | `0x1D` | `0x60` | `0x84` | `0xC0` |
| **868 MHz** | `650.5 – 890.0 MHz` | `0x03` | `0x0F` | `0x27` | `0x50` | `0x81` | `0xC2` |
| **915 MHz** | `>= 890.0 MHz` | `0x03` | `0x0E` | `0x27` | `0x8E` | `0x8D` | `0xC0` |

### Exact Go API (`internal/cc1101/telemetry.go`)
```go
package cc1101

// AllowedTXPowerDBm lists the canonical supported TX power levels in dBm.
var AllowedTXPowerDBm = [6]int{-30, -20, -10, 0, 5, 10}

func ClampTXPowerDBm(dbm int) int
func PATableByte(freqHz uint32, dbm int) (uint8, int, error)
func FreqOffsetHz(raw uint8, crystalHz uint32) int32
func DecodeLQI(raw uint8) (lqi uint8, crcOK bool)
func WORRegisters(intervalMs uint16, crystalHz uint32) (evt1, evt0, worCtrl uint8, err error)

func (r *Radio) ReadFreqOffsetHz() (int32, error)
func (r *Radio) ReadLQI() (uint8, bool, error)
func (r *Radio) SetTXPowerDBm(freqHz uint32, dbm int) error
func (r *Radio) ConfigureWOR(intervalMs uint16) error
func (r *Radio) EnterWOR() error
```

---

## 4. MCP Tools

1. **`rf_measure_freq_offset`**:
   - Inputs: `freq_hz` (`280000000..950000000`, required), `preset` (optional, default `AM650`), `samples` (`1..16`, default `4`).
   - Output: `{ freq_hz, preset, offset_hz, offset_ppm, rssi_dbm, lqi, crc_ok, source }`.
2. **`rf_set_tx_power`**:
   - Inputs: `dbm` (required, clamped to `{-30,-20,-10,0,5,10}`), `freq_hz` (optional, default `433920000`), `persist` (optional bool, default `true`).
   - Output: `{ requested_dbm, applied_dbm, freq_hz, patable_hex, persisted }`.
3. **`rf_wor_listen`**:
   - Inputs: `freq_hz` (`280000000..950000000`, required), `interval_ms` (`10..60000`, default `500`), `threshold_dbm` (optional, default `-75`), `duration_ms` (`50..5000`, default `500`).
   - Output: `{ freq_hz, interval_ms, woke, peak_rssi_dbm, offset_hz, lqi, source }`. Always returns radio to `IDLE`.

---

## 5. Edge Cases & Test Plan

| Category | Test Cases |
| --- | --- |
| **FREQEST Math** | `0x00`, `0x01`, `0x7F`, `0x80`, `0xFF`, `crystalHz == 0` fallback to `DefaultCrystalHz`. |
| **LQI Decoding** | `0x00` (`lqi=0, crc=false`), `0x80` (`lqi=0, crc=true`), `0xFF` (`lqi=127, crc=true`), `0x55` (`lqi=85, crc=false`). |
| **PATABLE Matrix** | Every band (`315/433/868/915 MHz`) × all 6 power steps; out-of-range freq error; clamping (`-99 -> -30`, `3 -> 5`, `99 -> 10`); OOK vs FSK PATABLE layout. |
| **WOR Timing** | Boundary intervals (`10 ms`, `1890 ms`, `1891 ms`, `60000 ms`), rejection of `0 ms`, `9 ms`, `60001 ms`; SPI sequence verifies `SIDLE -> regs -> SWORRST -> SWOR`. |
| **Config & UI & MCP** | `config.Normalize()` preserves explicit `0 dBm`, defaults missing field to `10 dBm`, clamps invalid values; `SET` TX Power row cycles cleanly; all 3 MCP tools pass in hardware mock and SIM modes with `-race`. |
