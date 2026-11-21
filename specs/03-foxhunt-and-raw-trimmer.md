# Spec 03: Live Foxhunt / Geiger RSSI Mode & On-Device RAW Trimmer / Repeater

**App**: `rf-heatmap` (`internal/rfml`, `internal/subfile`, `internal/library`, `internal/ui`, `internal/mcpserver`)  
**Status**: Ready for Implementation  
**Depends On**: `rfml.KalmanRSSI` (`internal/rfml/kalman.go`), `subfile.SubFile` (`internal/subfile/subfile.go`), `library.Store` (`internal/library/store.go`), `ui.App` (`internal/ui/app.go`)

---

## 1. Overview & Motivation

Field RF localization and clean signal capture require two focused utilities that currently only exist in partial form:
1. **Live Foxhunt / Geiger RSSI Mode**: `rfml.KalmanRSSI` already computes a smoothed RSSI and `CLOSER/FARTHER` trend inside a small strip on `PULSE`, but walking toward a hidden transmitter needs a **dedicated, high-contrast full-screen Foxhunt/Geiger view** locked onto a single carrier frequency with a huge RSSI bar, peak-hold marker, visual/audio Geiger tick rate proportional to signal strength, and instant `Esc` exit.
2. **On-Device RAW Trimmer & Repeater**: Raw GDO0 captures often include several seconds of leading/trailing background noise before and after the actual button press. Users need an on-device **RAW Trimmer** to isolate 1 clean frame, optionally set a **repeat count (`1x..10x`)**, and save atomically into `~/rf-heatmap/captures/` without ever touching read-only external `databases/`.

---

## 2. User Stories & Acceptance Criteria

### `US-RF-3.1`: Full-Screen Live Foxhunt / Geiger RSSI Mode
> **As a** field operator tracking down a 433/868/315/915 MHz transmitter,  
> **I want** a dedicated full-screen Foxhunt / Geiger RSSI view locked to a single frequency with a large bar meter, peak-hold marker, and Geiger tick rate proportional to RSSI,  
> **So that** I can walk toward the source eyes-free or at a glance and exit instantly with `Esc`.

#### Acceptance Criteria (Given / When / Then)
- **AC-3.1.1 (Zero-Alloc Foxhunt & Geiger Math in `internal/rfml`)**:
  - **Given** `rfml.FoxhuntTracker` wrapping `KalmanRSSI`, peak-hold (`PeakDBm`, `PeakAgeTicks`), and Geiger cadence calculation,
  - **When** `tracker.Update(rssiDBm, floorDBm)` runs on every tick,
  - **Then** it computes `GeigerHz` (`0.0 Hz` when below `floorDBm + 4 dB`, scaling monotonically up to `20.0 Hz` at `>= -35 dBm`), `GeigerIntervalMs` (`0` when silent, `50..1000 ms` when active), `PeakDBm`, and `KalmanState` with **`0 B/op, 0 allocs/op`**.
- **AC-3.1.2 (Full-Screen `320×170` Foxhunt Display)**:
  - **Given** the user enters Foxhunt mode from `HEATMAP` (on cursor frequency) or `PULSE` (on `ActiveFreq`) via `L` / softkey `FOX`,
  - **When** `drawFoxhunt(c)` renders,
  - **Then** the workspace displays:
    - Large frequency readout (`433.92 MHz`, scale 2) + live `dBm` (`-52 dBm`, scale 2) + `PK:-46dBm` (`ColAmber`).
    - Full-width (`304×26 px`) thermal-colored horizontal RSSI bar (`-110..-25 dBm`) with a crisp `2 px` `ColAmber` **peak-hold marker** and `ColCyan` squelch tick.
    - Trend pill (`^ CLOSER`, `v FARTHER`, `= STEADY`) + `ProximityPct` (`0..100%`) + visual **Geiger flash LED indicator** (`[● TICK 12.4Hz]`) pulsing in sync with `GeigerIntervalMs`.
- **AC-3.1.3 (Optional Audio / Beep Hook)**:
  - **Given** an optional injected `GeigerClicker` interface (`Click(freqHz int, durMs int)` — defaults to a no-op in `SIM`/headless or when `Settings.AudioFeedback == false`),
  - **When** the Geiger accumulator fires a tick,
  - **Then** it triggers a non-blocking click without ever stalling the `55 ms` UI render loop.
- **AC-3.1.4 (Instant `Esc` Exit & Peak Reset)**:
  - **Given** `FoxhuntViewState.Active == true`,
  - **When** the user presses `Esc` (or `8 BACK`),
  - **Then** Foxhunt mode exits immediately back to the previous tab (`HEATMAP` or `PULSE`) WITHOUT quitting the application.
  - Pressing `4 (RST PK)` resets `PeakDBm` and `KalmanRSSI`; `Z/C` nudges frequency `±50 kHz` (and resets peak/Kalman).

---

### `US-RF-3.2`: On-Device RAW Trimmer & Frame Repeater
> **As a** Sub-GHz user who recorded a noisy `.sub` RAW capture,  
> **I want** to trim leading/trailing noise pulses to isolate 1 clean frame, configure repeat count `1x..10x`, and save the result to my User Library,  
> **So that** my saved `.sub` captures are compact, clean, and reliable on replay.

#### Acceptance Criteria (Given / When / Then)
- **AC-3.2.1 (Auto-Bounds Suggestion & Manual Trim)**:
  - **Given** a `RAW_Data` pulse slice (`len >= 4`),
  - **When** `subfile.SuggestTrimBounds(raw)` is called,
  - **Then** it identifies the first and last pulse indices of the strongest/cleanest burst window (skipping leading/trailing long silence gaps `|p| > 25000 µs` and isolated `< 60 µs` EMI spikes) and returns `(startIdx, endIdx)`.
- **AC-3.2.2 (Sign-Alternation Invariant Preservation)**:
  - **Given** any `raw []int32` and valid `[start, end)` indices,
  - **When** `subfile.TrimAndRepeatRaw(raw, start, end, repeats, gapUs)` executes (`repeats ∈ [1..10]`, `gapUs` default `10000`),
  - **Then**:
    1. Every output pulse is non-zero (`!= 0`).
    2. Output strictly alternates `+high / -low` (starting on `+high` when possible; adjacent same-sign pulses at repeat boundaries are merged or separated by `-gapUs`).
    3. Total output length is capped at `maxRawPulsesToEncode` (`65536`); exceeding the cap returns `ErrRawTooLarge`.
- **AC-3.2.3 (External-DB Read-Only & Atomic Save Invariant)**:
  - **Given** the user trims a capture from `PULSE` or loads a `.sub` from `SAVED` (or even previews an external read-only pack),
  - **When** saving the trimmed result,
  - **Then** it MUST write atomically (`WriteFile(tmp)` + `os.Rename(tmp, dst)`, mode `0644`) **strictly inside `~/rf-heatmap/captures/`** with a collision-safe name (`<base>_trim.sub`, `<base>_trim_2.sub`), and MUST NEVER modify any file in `~/rf-heatmap/databases/` or `~/subg/files/`.
- **AC-3.2.4 (`320×170` Visual Trimmer UI)**:
  - **Given** the user enters the `TRIM` sub-view from `PULSE` (`CapturedRaw`) or `SAVED` (selected RAW `.sub`),
  - **When** rendered on the `320×170` LCD,
  - **Then** it draws a pulse-activity minimap (`304×54 px`) with highlighted `[start..end]` selection window, dimmed trimmed-out regions, active handle indicator (`[START]` / `[END]`), repeat count (`RPT: 3x`), resulting pulse count + duration (`ms`), and softkeys `[4:HDL | 5:AUTO | 6:TX | 7:SAVE | 8:BACK]`.

---

### `US-RF-3.3`: MCP Tool `rf_foxhunt_sample`
> **As an** AI agent tracking an RF emitter over SSH `--mcp`,  
> **I want** a lightweight `rf_foxhunt_sample` tool returning live RSSI, peak-hold, Kalman state, and Geiger tick rate,  
> **So that** I can poll or guide foxhunting sessions without running a heavy full-band sweep.

#### Acceptance Criteria (Given / When / Then)
- **AC-3.3.1 (Single/Burst Sample with Peak-Hold & Geiger)**:
  - **Given** `freq_hz` (`280000000..950000000`), optional `preset` (default `"AM650"`), optional `samples` (`1..16`, default `4`), and optional `reset_peak` (`bool`),
  - **When** `rf_foxhunt_sample` is invoked,
  - **Then** it locks `s.radioMu`, tunes and reads RSSI, updates the server's persistent `rfml.FoxhuntTracker` (auto-resetting if `freq_hz` changed or `reset_peak == true`), returns CC1101 to `IDLE`, and responds with `{ "freq_hz", "rssi_dbm", "peak_dbm", "floor_dbm", "geiger_hz", "geiger_interval_ms", "kalman": {...} }`.

---

### `US-RF-3.4`: MCP Tool `rf_trim_raw`
> **As an** AI agent cleaning captured `.sub` files over SSH `--mcp`,  
> **I want** to trim noise from a User Library `.sub` (or raw pulse array), set repeat count `1..10`, and optionally save it atomically as a new capture in `~/rf-heatmap/captures/`,  
> **So that** I can automate RAW signal cleanup safely.

#### Acceptance Criteria (Given / When / Then)
- **AC-3.4.1 (File or Inline Pulses)**:
  - **Given** either `name` (existing `.sub` in User Library or `pack` + `name` read-only source) OR inline `pulses`, plus optional `start_idx`, `end_idx`, `auto_trim: true`, `repeats` (`1..10`, default `1`), `inter_frame_gap_us` (`1000..100000`, default `10000`), and `save_as` (optional filename in `~/rf-heatmap/captures/`),
  - **When** `rf_trim_raw` is invoked,
  - **Then** it trims and repeats the pulses, enforces sign alternation, and if `save_as` is provided, rejects path traversal (`..`, `/`, `\`) and writes atomically into `~/rf-heatmap/captures/<save_as>.sub` (NEVER overwriting read-only `databases/`).
- **AC-3.4.2 (Preview Mode)**:
  - **Given** `save_as` is omitted,
  - **When** `rf_trim_raw` is invoked,
  - **Then** it writes nothing to disk and returns `original_count`, `trimmed_count`, `start_idx`, `end_idx`, `repeats`, `duration_ms`, and `pulses`.

---

## 3. Go Interfaces, Types & Package Decomposition

### File Layout (`<= 350` lines/file, `<= 40` lines/function)
- `internal/rfml/geiger.go` — `FoxhuntSample`, `FoxhuntTracker` (embeds/wraps `KalmanRSSI` + peak-hold + `GeigerTickRate`), strictly `0 allocs/op` (`<= 140` lines).
- `internal/subfile/trimmer.go` — `TrimBounds`, `TrimResult`, `SuggestTrimBounds`, `TrimAndRepeatRaw`, `NormalizeSignAlternation` (`<= 210` lines).
- `internal/library/store_save.go` — `SaveUserSubFileAtomic(name string, sf *subfile.SubFile) (string, error)` with sandbox + collision-safe naming (`<= 110` lines).
- `internal/ui/view_foxhunt.go` — `FOXHUNT` full-screen view, Geiger flash timer, optional `GeigerClicker` hook, `Esc` exit (`<= 220` lines).
- `internal/ui/view_trimmer.go` — `TRIM` sub-view, pulse minimap renderer, `Z/C` handle step, `F/X` repeat adjust (`<= 240` lines).
- `internal/mcpserver/tools_foxhunt.go` — Add `toolFoxhuntSample` alongside existing `toolFoxhuntTrack` (keep file `<= 260` lines).
- `internal/mcpserver/tools_trimmer.go` — `toolTrimRaw` handler (`<= 160` lines).

### Exact Domain Types
```go
// internal/rfml/geiger.go
type FoxhuntSample struct {
	RawDBm           int32       `json:"raw_dbm"`
	PeakDBm          int32       `json:"peak_dbm"`
	FloorDBm         int32       `json:"floor_dbm"`
	GeigerHz         float64     `json:"geiger_hz"`
	GeigerIntervalMs int         `json:"geiger_interval_ms"`
	Kalman           KalmanState `json:"kalman"`
}

type FoxhuntTracker struct {
	kalman      KalmanRSSI
	peakDBm     int32
	peakValid   bool
	lockedFreq  uint32
}

func NewFoxhuntTracker(q, r float64) *FoxhuntTracker
func (t *FoxhuntTracker) Reset(freqHz uint32)
func (t *FoxhuntTracker) ResetPeak()
func (t *FoxhuntTracker) Update(rssiDBm, floorDBm int32) FoxhuntSample
func GeigerCadence(rssiDBm, floorDBm int32) (hz float64, intervalMs int)
```

```go
// internal/subfile/trimmer.go
var (
	ErrInvalidTrimRange = errors.New("subfile: invalid trim range")
	ErrInvalidRepeats   = errors.New("subfile: repeats must be between 1 and 10")
	ErrRawTooLarge      = errors.New("subfile: trimmed RAW_Data exceeds max pulses")
)

type TrimBounds struct {
	Start int `json:"start_idx"`
	End   int `json:"end_idx"`
}

type TrimResult struct {
	OriginalCount int     `json:"original_count"`
	TrimmedCount  int     `json:"trimmed_count"`
	Bounds        TrimBounds `json:"bounds"`
	Repeats       int     `json:"repeats"`
	DurationMs    float64 `json:"duration_ms"`
	Pulses        []int32 `json:"pulses"`
}

func SuggestTrimBounds(raw []int32) TrimBounds
func TrimAndRepeatRaw(raw []int32, start, end, repeats int, interFrameGapUs int32) (TrimResult, error)
```

### UI State Sub-Structs (`internal/ui/state.go`)
```go
// GeigerClicker is a consumer-owned optional audio/tactile feedback hook.
type GeigerClicker interface {
	Click(freqHz int, durMs int)
}

type FoxhuntViewState struct {
	ShowFoxhunt    bool
	Tracker        *rfml.FoxhuntTracker
	LastFoxSample  rfml.FoxhuntSample
	GeigerFlashOn  bool
	nextGeigerTick time.Time
	Clicker        GeigerClicker // optional; nil = visual-only
	returnMode     Mode
}

type TrimmerState struct {
	ShowTrimmer bool
	SourceName  string
	SourceFreq  uint32
	SourcePreset string
	RawSource   []int32
	StartIdx    int
	EndIdx      int
	EditEnd     bool // false = moving StartIdx, true = moving EndIdx
	Repeats     int  // 1..10
	returnMode  Mode
}
```

---

## 4. MCP Tool Schemas

### `rf_foxhunt_sample`
```json
{
  "name": "rf_foxhunt_sample",
  "description": "Sample a single locked Sub-GHz frequency with peak-hold, 1D Kalman smoothing, and Geiger RSSI tick cadence.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "freq_hz": { "type": "integer", "minimum": 280000000, "maximum": 950000000 },
      "preset": { "type": "string", "description": "Optional modem preset (default AM650)" },
      "samples": { "type": "integer", "minimum": 1, "maximum": 16, "description": "Samples to average/advance filter (default 4)" },
      "reset_peak": { "type": "boolean", "description": "Reset peak-hold and Kalman state before sampling" }
    },
    "required": ["freq_hz"]
  }
}
```

### `rf_trim_raw`
```json
{
  "name": "rf_trim_raw",
  "description": "Trim leading/trailing noise from a RAW .sub capture (or inline pulses), optionally repeat 1..10x, and save atomically into User Library (~/rf-heatmap/captures/). Never modifies external databases.",
  "inputSchema": {
    "type": "object",
    "properties": {
      "pack": { "type": "string", "description": "Optional source pack_id (default: user)" },
      "name": { "type": "string", "description": "Source .sub filename (no path separators)" },
      "pulses": { "type": "array", "items": { "type": "integer" }, "description": "Optional inline RAW_Data pulses when name is omitted" },
      "freq_hz": { "type": "integer", "minimum": 280000000, "maximum": 950000000 },
      "auto_trim": { "type": "boolean", "description": "Auto-detect clean burst boundaries (default true if start_idx/end_idx omitted)" },
      "start_idx": { "type": "integer", "minimum": 0 },
      "end_idx": { "type": "integer", "minimum": 1 },
      "repeats": { "type": "integer", "minimum": 1, "maximum": 10, "description": "Frame repeat count (default 1)" },
      "inter_frame_gap_us": { "type": "integer", "minimum": 1000, "maximum": 100000, "description": "Low gap between repeats in us (default 10000)" },
      "save_as": { "type": "string", "description": "Optional output filename saved atomically to ~/rf-heatmap/captures/ (no path separators)" }
    }
  }
}
```

---

## 5. `320×170` LCD Layouts & Keybindings

### Foxhunt / Geiger Mode (`view_foxhunt.go`)
- **Top Strip (`y=20..44`)**: `433.92 MHz` (`ColCyan`, scale 2) | `-52dBm` (`ColGreen`/`ColMuted`, scale 2)
- **Peak & Trend Row (`y=48..62`)**: `PK:-46dBm` (`ColAmber`) | `^ CLOSER 74%` (`ColGreen`) | `GEIGER: 12.4Hz` (`ColMagenta` when flashing)
- **RSSI Bar (`x=8..312, y=68..96`)**: `304×28 px` bar with thermal fill + `2 px` `ColAmber` peak-hold vertical bar + `ColCyan` squelch tick + `-110 / -70 / -30` scale ticks (`y=99`)
- **Geiger Pulse Meter (`x=8..312, y=112..136`)**: Visual 12-segment Geiger LED bar that flashes bright `ColGreen`/`ColAmber` on each tick
- **Nav Bar**: `[4:RST PK | 5:MUTE | 6:BAND | 7:MOD | 8:BACK]` (`Esc` exits immediately to `returnMode`)

### RAW Trimmer Mode (`view_trimmer.go`)
- **Top Info (`y=20..34`)**: `TRIM: raw_142301.sub` (`ColCyan`) | `RPT: 3x` (`ColGreen`) | `248p / 64ms` (`ColMuted`)
- **Pulse Minimap (`x=8..312, y=38..94`)**: Vertical activity density bars across `RawSource`; `[StartIdx..EndIdx)` shaded `ColPanelAlt` with `ColCyan` start handle and `ColAmber` end handle; trimmed-out wings dimmed `ColMuted`.
- **Controls Hint (`y=100..136`)**: `Active: [START] Z/C:Move  F/X:Repeat (1..10x)  5:Auto`
- **Nav Bar**: `[4:START/END | 5:AUTO | 6:TX | 7:SAVE | 8:BACK]` (`Esc` exits immediately to `returnMode`)

---

## 6. Edge Cases & Adversarial Test Plan

| Category | Test Cases |
| --- | --- |
| **Geiger & Kalman Hot Path** | `BenchmarkFoxhuntTrackerUpdate` with `for b.Loop()` MUST assert `0 allocs/op`; RSSI `< -120 dBm` / `> 0 dBm` clamped safely; `rssi <= floor + 4` yields `0 Hz` / `0 ms`; `ResetPeak` clears `PeakDBm` without panic. |
| **RAW Trimmer Edge Cases** | `start >= end`, negative `start`, `end > len(raw)`, `repeats == 0` or `repeats == 11` → returns `errors.Is(..., ErrInvalidTrimRange/ErrInvalidRepeats)`. |
| **Sign Alternation** | Trimming mid-frame starting on a negative pulse or ending on a positive pulse + `repeats = 4` MUST still yield strict `+high / -low` alternation with zero `0 µs` values and proper inter-frame negative gap. |
| **Max Pulse Overflow** | `10000`-pulse slice × `10` repeats (`> 65536`) MUST return `ErrRawTooLarge`. |
| **Read-Only DB & Sandbox** | `rf_trim_raw` with `save_as: "../databases/evil.sub"` or `"/tmp/x.sub"` MUST reject (`ErrInvalidPath`); trimming a file from `pack: "db:fans"` with `save_as: "clean.sub"` MUST leave `db:fans` untouched and write atomically to `~/rf-heatmap/captures/clean.sub`. |
| **UI `Esc` Isolation** | Pressing `Esc` in `FOXHUNT` or `TRIM` returns `HandleKey(KeyQuit) == false` (does NOT exit the app) and restores `returnMode`. |
| **Fuzzing & Coverage** | `FuzzTrimAndRepeatRaw(f *testing.F)` verifying sign-alternation invariant on all non-error outputs; `go test -race -cover ./...` >= 90%. |
