# RF Heatmap for CardputerZero

A fast, user-friendly **CC1101 Sub-GHz Thermal Waterfall, RSSI Oscilloscope, OOK Protocol Decoder & Flipper `.sub` Studio** with **on-device Smart Squelch & Auto-Lock ML** for the **M5Stack CardputerZero** + **Cap CC1101**.

> [!CAUTION]
> **Legal & Responsible Use Disclaimer:** This software is provided strictly for **educational, research, diagnostics, and authorized security testing** on devices, networks, and radio frequencies that you own or have explicit written permission to test. Unauthorized interception, replay, jamming, or interference with third-party devices or communications is illegal and punishable by law. Radio transmission in Sub-GHz ISM bands is subject to local telecommunications regulations (FCC, ETSI/CE, etc.) — you are solely responsible for complying with applicable frequency, power, and duty-cycle laws. Provided **"AS IS"** without warranty of any kind; use entirely at your own risk.

![HEATMAP](assets/screenshots/heatmap.png)

## Features

- **HEATMAP (Spectrum + Thermal Waterfall)**: Sweep `433 ISM`, `868 EU`, `315 US`, `915 ISM`, or `ALL ISM` (`300–928 MHz`) with live RSSI bars, amber Peak-Hold markers, per-bin ML noise floor / Smart Squelch ticks, and a scrolling Inferno thermal waterfall (`-110 dBm` to `-35 dBm`). Press `Enter` or `6 (LOCK)` to jump straight to the strongest frequency.
- **On-Device ML — Smart Squelch (`S`) & Auto-Lock (`L`)**: Zero-allocation per-bin EWMA baseline (`internal/rfml/baseline.go`) with Welford warmup, burst-freeze guard (`z ≥ 3.0`), adaptive threshold (`μ + k·σ`, tunable with `F/X` in `PULSE`), and automatic cursor lock onto `z ≥ 4.0` bursts.
- **PULSE (RSSI Scope & RAW Capture)**: Real-time dBm meter, live Z-score badge, Smart Squelch `k` tuning or manual dBm squelch (`F/X`), fine frequency tuning (`Z/C`), `AM650`/`AM270`/`FM238`/`FM476`/`GFSK99` presets, Flipper `.sub` RAW save/replay, **`G` Foxhunt Geiger RSSI**, and **`T` RAW Trimmer & Repeater (`1x–10x`)**.
- **DECODE (Live OOK & Rolling-Code Analyzer + SENSORS)**: Auto-decodes `Princeton`, `PT2262` (tri-state), `EV1527`, `CAME`, `Linear`, `Holtek` (`HT12E`/`HT6P20B`), `Nice FLO`, `KeeLoq`/`HCS301` (66-bit), and `Somfy Telis` (56-bit Manchester RTS). Detects hopping codes across consecutive presses with an amber **`ROLLING`** badge and 2-step TX replay guard, and recovers weak/jittery captures via **1D K-Means** (`ML-CLEAN`). Press **`R`** to open the live **`SENSORS`** sub-view for **TPMS** (`Schrader` / `Toyota` via `GFSK99`) and **Weather Stations** (`Oregon Scientific`, `Acurite`, `LaCrosse`).
- **FOXHUNT Geiger Mode (`G`) & RAW Trimmer (`T`)**: Press `G` in `HEATMAP` or `PULSE` for a full-screen single-frequency Foxhunt Geiger RSSI meter with peak-hold marker and proportional click cadence (`Esc` exits back). Press `T` in `PULSE` or `SAVED` to strip leading/trailing noise from RAW captures, set `1x–10x` repeats, and save atomically into `~/rf-heatmap/captures/` (read-only external databases are never modified).
- **SAVED (User `.sub` Library)**: Browse, load, delete, purge noise, and transmit your own Flipper-compatible `.sub` captures strictly from `~/rf-heatmap/captures/` (never touches external databases).
- **PROBE (Auto-Probe, External Databases & Virtual Generators)**: Step automatically through read-only external `.sub` database packs (`~/rf-heatmap/databases/<pack>/*.sub`, `~/subg/files/`), database-free built-in key generators (**`GEN:CAME-12`** 433.92 MHz / 4096 keys, **`GEN:Linear-10`** 315.00 MHz / 1024 keys, **`GEN:CAME-868`** 868.35 MHz / 4096 keys — override freq/mod with `B`/`M`), or your `USER` library at a configurable interval (`0.05s–5.0s`). Hit `4 (STOP)` / `Space` / `Enter` the instant a target device reacts, step back up (`F/X`) using the `>` (last sent) and `*` (last 12 sent) markers, single-test (`5 TRY` / `Enter`), and save the winning `.sub` into your User Library with `6 (SAVE)`.
- **SET (Settings, Remote DB Sync & Opt-In MCP Server)**: Configure Smart Squelch (`k`), Auto-Lock, trigger incremental **Sync DB Sources** (downloads only new/changed `.sub` files from GitHub repos or HTTP JSON manifests in `~/.config/rf-heatmap/sources.json` into isolated `~/rf-heatmap/databases/<id>/` folders, also runnable via `rf-heatmap --sync`), and toggle the built-in pure-Go **MCP Server** (`mcp_enabled`, disabled by default).
- **Built-in 5-Page Help (`H`)**: Interactive manual right on the device.
- **Zero Dependencies**: Pure Go (`CGO_ENABLED=0`), renders directly to `/dev/fb0` (320×170 RGB565) and reads `/dev/input/event*` without X11/Wayland or root privileges.

## Controls

| Key | Action |
| --- | --- |
| `4`–`8` | Bottom navigation bar actions |
| `Tab` / `8` | Switch mode (`HEATMAP → PULSE → DECODE → SAVED → PROBE → SET`) |
| `F` / `X` (`↑` / `↓`) | Up / Down (band, Smart Squelch `k` / manual squelch in `PULSE`, list selection) |
| `Z` / `C` (`←` / `→`) | Left / Right (cursor channel, `±50 kHz` frequency step) |
| `B` / `M` | Cycle ISM band / modulation preset (`AM650`/`AM270`/`FM238`/`FM476`/`GFSK99`) |
| `S` / `L` | Toggle **Auto Squelch (`ASQ`)** / **Auto-Lock (`LOCK`)** |
| `R` / `G` / `T` | `R` in `DECODE`: **SENSORS** (TPMS/Weather) · `G` in `HEATMAP`/`PULSE`: **FOXHUNT** Geiger · `T` in `PULSE`/`SAVED`: **RAW Trimmer** |
| `Enter` | Lock peak frequency / transmit |
| `H` | Open 4-page interactive Help |
| `Q` / `Esc` | Quit safely (restores `GPIO 14` & puts CC1101 in `IDLE`) |

## Documentation

- **Interactive Learning Portal**: [CardputerZero RF & NFC Learning Portal](https://igenexxx.github.io/cardputer-zero-learning-portal/#/home) — full English guides for [RF Heatmap](https://igenexxx.github.io/cardputer-zero-learning-portal/#/rf-guide), [Sub-GHz & CC1101 Theory](https://igenexxx.github.io/cardputer-zero-learning-portal/#/rf-domain), all 5 On-Device ML stages, and interactive browser labs.
- **Detailed App Guide**: [`docs/GUIDE.md`](docs/GUIDE.md) — in-depth device walkthrough and reference.

## Build & Package (`.deb`)

```bash
go test -race ./...
./scripts/build_deb.sh
```

This produces `dist/rf-heatmap_0.1.0-1_arm64.deb` ready for installation (`sudo dpkg -i`) or publishing via `czdev publish --deb dist/rf-heatmap_0.1.0-1_arm64.deb`.

## Built-In MCP Server (SSH Opt-In)

After enabling **MCP Server (SSH)** in the **`SET`** tab (`~/.config/rf-heatmap/config.json` -> `"mcp_enabled": true`), AI agents can invoke `rf-heatmap` over SSH stdio:

```bash
ssh <cardputer-host> /usr/share/APPLaunch/bin/rf-heatmap --mcp
```

Exposed tools: `rf_get_status`, `rf_sweep_band`, `rf_read_rssi`, `rf_decode_ook`, `rf_analyze_rolling_code`, `rf_decode_sensor`, `rf_analyze_pulses`, `rf_classify_signal`, `rf_match_fingerprint`, `rf_foxhunt_track`, `rf_foxhunt_sample`, `rf_trim_raw`, `rf_list_saved` (supports `source`/`pack` filter), `rf_list_databases`, `rf_read_saved`, `rf_save_capture`, `rf_promote_to_user`, `rf_transmit_saved` (`confirm_rf: true`), `rf_list_sources`, `rf_add_source`, `rf_remove_source` (`confirm: true`, single or batch `ids[]`), `rf_sync_sources`, `rf_delete_pack_files` (`confirm: true`), `rf_delete_saved` (`confirm: true`), `rf_purge_noise` (`dry_run: true` by default; deletion requires `dry_run: false, confirm: true`).

## License

MIT — see [LICENSE](LICENSE).
