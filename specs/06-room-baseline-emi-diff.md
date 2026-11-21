# Spec 06: CC1101 Room Baseline & EMI Spectrum Diff

**App**: `rf-heatmap` (`internal/rfml`, `internal/ui`, `internal/mcpserver`)  
**Status**: Ready for Implementation  
**Depends On**: `internal/rfml/baseline.go`, `internal/ui/view_heatmap.go`, `internal/ui/state.go`, `internal/mcpserver`

---

## 1. Overview & Motivation

Urban and lab RF environments contain persistent ambient emitters (power supplies, LED drivers, routers, smart meters). When hunting a newly introduced RF/EMI source (rogue transmitter, noisy buck converter, newly powered IoT device), raw RSSI bars alone cannot separate persistent room background from new emissions.

**Room Baseline & EMI Spectrum Diff** adds:
1. **Persistent Room Baselines (`RoomBaseline`)**: Capture multi-sweep per-bin Welford mean, standard deviation, and peak RSSI snapshots into sandboxed `~/.config/rf-heatmap/baselines/<name>.json` (`0600`, atomic write).
2. **Zero-Allocation EMI Diff Engine (`ComputeDiff`)**: Compare live sweeps against an active `RoomBaseline` into caller-owned `[]DiffBin` with `0 B/op, 0 allocs/op`, classifying each bin as `normal`, `elevated`, or `new_emitter`.
3. **HEATMAP UI Overlay (`B` / `D`)**: Press `B` in `HEATMAP` to capture the `"room_live"` baseline; press `D` to toggle **EMI DIFF** mode with red/amber anomaly highlighting and a `DIFF` header pill.
4. **MCP Automation (4 Tools)**: `rf_save_room_baseline`, `rf_list_room_baselines`, `rf_diff_room_baseline`, and `rf_delete_room_baseline` (`confirm: true`).

---

## 2. User Stories & Acceptance Criteria

### `US-RF-6.1`: Capture & Persist Room Baselines
> **As an** RF engineer characterizing an ambient room environment,  
> **I want** to capture multi-sweep per-bin statistics (mean, std, peak dBm) and persist them as named JSON files in `~/.config/rf-heatmap/baselines/`,  
> **So that** I can compare future sweeps against known quiet-room baselines across sessions.

#### Acceptance Criteria (Given / When / Then)
- **AC-6.1.1 (Welford Per-Bin Statistics)**:
  - **Given** `frames [][]float32` containing `M >= 1` sweep frames of `N >= 1` bins each,
  - **When** `CaptureBaselineFromFrames(name, band, startHz, stepHz, frames)` runs,
  - **Then** it computes exact Welford sample mean (`MeanDBm[i]`), sample standard deviation (`StdDBm[i]`, `0` when `M == 1`), and maximum (`PeakDBm[i]`) for each bin `0..N-1`, setting `Samples = M` and `CreatedAt = time.Now().UTC()`.
- **AC-6.1.2 (Validation & Sandboxed Persistence)**:
  - **Given** a baseline name containing `..`, `/`, `\`, null bytes, leading `.`, empty string, or unsupported characters,
  - **When** `CaptureBaselineFromFrames`, `SaveRoomBaselineAtomic`, `LoadRoomBaseline`, or `DeleteRoomBaseline` is called,
  - **Then** it rejects the operation with a wrapped `ErrInvalidBaselineName` error without touching the filesystem.
- **AC-6.1.3 (Atomic `0600` Write)**:
  - **Given** a valid `RoomBaseline` and baselines directory,
  - **When** `SaveRoomBaselineAtomic(dir, base)` executes,
  - **Then** it creates `dir` (`0700`), writes a temporary file (`0600`) in `dir`, closes it, and atomically renames it to `<dir>/<name>.json`.

---

### `US-RF-6.2`: Zero-Allocation EMI Spectrum Diff & HEATMAP Overlay
> **As a** CardputerZero user locating a newly active EMI or Sub-GHz emitter,  
> **I want** a live `EMI DIFF` overlay on `HEATMAP` that highlights bins rising significantly above the captured room baseline,  
> **So that** new emitters stand out immediately in red/amber on the `320×170` LCD.

#### Acceptance Criteria (Given / When / Then)
- **AC-6.2.1 (Zero-Allocation Hot Path)**:
  - **Given** a valid `RoomBaseline` of `N` bins, a live slice `liveDBm` of `N` bins, and a caller-owned `dst` with `cap(dst) >= N`,
  - **When** `ComputeDiff(base, liveDBm, dst)` runs,
  - **Then** it writes `N` `DiffBin` entries into `dst[:N]` with **`0 B/op, 0 allocs/op`** (verified via `BenchmarkComputeDiff` using `for b.Loop()`).
- **AC-6.2.2 (Classification Thresholds)**:
  - **Given** `delta = live - base.MeanDBm[i]` and `z = delta / max(base.StdDBm[i], 2.0)`,
  - **When** `ComputeDiff` classifies bin `i`,
  - **Then**:
    - `delta >= +8.0 dB` **or** `z >= 3.5` -> `DiffNewEmitter` (`"new_emitter"`, rendered in **Red**).
    - Else `delta >= +4.0 dB` **or** `z >= 2.0` -> `DiffElevated` (`"elevated"`, rendered in **Amber**).
    - Else -> `DiffNormal` (`"normal"`, rendered normally).
- **AC-6.2.3 (HEATMAP `B` and `D` Controls)**:
  - **Given** the user is in `HEATMAP`,
  - **When** pressing `B`,
  - **Then** the app captures recent sweep frames as `"room_live"`, saves atomically, sets `ActiveBaseline`, and shows a confirmation toast.
  - **When** pressing `D`,
  - **Then** `DiffMode` toggles ON/OFF (loading `"room_live"` if not yet in memory; warning if none exists). When `DiffMode` is ON, a red `DIFF` header pill appears and anomalous bins highlight in red/amber.

---

### `US-RF-6.3`: MCP Automation for Room Baselines & EMI Diff
> **As an** AI agent connected via `rf-heatmap --mcp` over SSH,  
> **I want** typed MCP tools to save, list, diff, and delete room baselines,  
> **So that** I can run automated EMI spectrum differential diagnostics headlessly.

#### Acceptance Criteria (Given / When / Then)
- **AC-6.3.1 (4 MCP Tools, 37 Total)**:
  - **Given** MCP server is enabled (`mcp_enabled: true`),
  - **When** `tools/list` is called,
  - **Then** 37 tools are returned including `rf_save_room_baseline`, `rf_list_room_baselines`, `rf_diff_room_baseline`, and `rf_delete_room_baseline`.
- **AC-6.3.2 (Destructive Delete Guard)**:
  - **Given** `rf_delete_room_baseline` is called with `confirm: false` or omitted,
  - **When** dispatched by the MCP server,
  - **Then** it returns an error result and preserves the baseline file on disk.

---

## 3. Exact Go Types & Contracts (`internal/rfml/room_baseline.go`)

```go
package rfml

import "time"

type DiffClass string

const (
	DiffNormal     DiffClass = "normal"
	DiffElevated   DiffClass = "elevated"
	DiffNewEmitter DiffClass = "new_emitter"

	MinRoomStdDBm         float32 = 2.0
	DiffElevatedDeltaDB   float32 = 4.0
	DiffElevatedZScore    float32 = 2.0
	DiffNewEmitterDeltaDB float32 = 8.0
	DiffNewEmitterZScore  float32 = 3.5
)

type RoomBaseline struct {
	Name      string    `json:"name"`
	Band      string    `json:"band"`
	StartHz   uint32    `json:"start_hz"`
	StepHz    uint32    `json:"step_hz"`
	Samples   int       `json:"samples"`
	MeanDBm   []float32 `json:"mean_dbm"`
	StdDBm    []float32 `json:"std_dbm"`
	PeakDBm   []float32 `json:"peak_dbm"`
	CreatedAt time.Time `json:"created_at"`
}

type DiffBin struct {
	Bin     int       `json:"bin"`
	FreqHz  uint32    `json:"freq_hz"`
	LiveDBm float32   `json:"live_dbm"`
	BaseDBm float32   `json:"base_dbm"`
	DeltaDB float32   `json:"delta_db"`
	ZScore  float32   `json:"z_score"`
	Class   DiffClass `json:"class"`
}

func DefaultBaselinesDir() string
func ValidateBaselineName(name string) error
func CaptureBaselineFromFrames(name, band string, startHz, stepHz uint32, frames [][]float32) (*RoomBaseline, error)
func ComputeDiff(base *RoomBaseline, liveDBm []float32, dst []DiffBin) []DiffBin
func SaveRoomBaselineAtomic(dir string, base *RoomBaseline) (string, error)
func LoadRoomBaseline(dir, name string) (*RoomBaseline, error)
func ListRoomBaselines(dir string) ([]RoomBaseline, error)
func DeleteRoomBaseline(dir, name string) error
```

---

## 4. UI & MCP Integration

### UI State (`internal/ui/state.go`)
- Add `DiffMode bool`, `ActiveBaseline *rfml.RoomBaseline`, and `DiffBins []rfml.DiffBin` to `HeatmapState` (10 fields total, `<= 12` cap).
- Add `BaselinesDir string` to `SettingsState` (9 fields total, defaulting to `rfml.DefaultBaselinesDir()`).
- In `HEATMAP` (`internal/ui/view_heatmap.go`):
  - `B` captures the current sweep history into `"room_live"` and persists it atomically.
  - `D` toggles `DiffMode` (recomputing `DiffBins` via `rfml.ComputeDiff` on every sweep tick with 0 allocations).
  - `5 (BAND)` and `F` (`↑`) continue to cycle ISM bands.
  - Header (`internal/ui/chrome.go`) shows a red `DIFF` status pill when `DiffMode` is active.

### MCP Tools (`internal/mcpserver/tools_baseline.go`, `schema_unlock.go`, `server.go`)
1. **`rf_save_room_baseline`**: Inputs `name` (required), `band` (`0..4`, default `0`), `sweeps` (`1..30`, default `8`), optional `frames` (`[][]float32`). Captures and saves baseline atomically.
2. **`rf_list_room_baselines`**: Lists all saved baselines sorted by name.
3. **`rf_diff_room_baseline`**: Inputs `name` (required), optional `live_dbm` (`[]float32`), optional `only_anomalies` (`bool`). Sweeps live if `live_dbm` is omitted and returns per-bin diff & summary counts.
4. **`rf_delete_room_baseline`**: Inputs `name` (required), `confirm` (`true` required). Deletes sandboxed `<name>.json`.

---

## 5. Adversarial Test Plan

| Category | Test Cases |
| --- | --- |
| **Welford Math** | Single frame (`StdDBm == 0`), constant frames, multi-frame known mean/std/peak, mismatched frame lengths, empty frames, `NaN`/`Inf` rejection. |
| **ComputeDiff** | `nil` baseline, empty `liveDBm`, shorter/longer `liveDBm`, exact threshold boundaries (`+4.0 dB`, `+8.0 dB`, `Z=2.0`, `Z=3.5`), flat `StdDBm=0` floored to `2.0 dBm`, `BenchmarkComputeDiff` asserting `0 allocs/op`. |
| **Path Sandbox & Atomic IO** | Reject `""`, `"."`, `".."`, `"../evil"`, `"a/b"`, `"a\\b"`, `".hidden"`, `>64 chars`, `0600` file permissions check, round-trip Save/Load/List/Delete, missing dir returns empty list. |
| **UI (`HEATMAP`)** | `B` captures `"room_live"`, `D` toggles `DiffMode` (and warns when no baseline exists), `Step()` updates `DiffBins`, `Render()` draws red/amber bars and `DIFF` pill without panics. |
| **MCP (`-race`)** | `tools/list` verifies 37 tools; full lifecycle save -> list -> diff (both inline `live_dbm` and live sweep) -> delete (`confirm: false` rejected, `confirm: true` succeeds); path traversal rejected on all tools. |
