# RF Heatmap for CardputerZero

A fast, user-friendly **CC1101 Sub-GHz Thermal Waterfall, RSSI Oscilloscope, OOK Protocol Decoder & Flipper `.sub` Studio** with **on-device Smart Squelch & Auto-Lock ML** for the **M5Stack CardputerZero** + **Cap CC1101**.

> [!CAUTION]
> **Legal & Responsible Use Disclaimer:** This software is provided strictly for **educational, research, diagnostics, and authorized security testing** on devices, networks, and radio frequencies that you own or have explicit written permission to test. Unauthorized interception, replay, jamming, or interference with third-party devices or communications is illegal and punishable by law. Radio transmission in Sub-GHz ISM bands is subject to local telecommunications regulations (FCC, ETSI/CE, etc.) — you are solely responsible for complying with applicable frequency, power, and duty-cycle laws. Provided **"AS IS"** without warranty of any kind; use entirely at your own risk.

![HEATMAP](assets/screenshots/heatmap.png)

## Features

- **HEATMAP (Spectrum + Thermal Waterfall + EMI DIFF)**: Sweep `433 ISM`, `868 EU`, `315 US`, `915 ISM`, or `ALL ISM` (`300–928 MHz`) with live RSSI bars, amber Peak-Hold markers, per-bin ML noise floor / Smart Squelch ticks, and a scrolling Inferno thermal waterfall (`-110 dBm` to `-35 dBm`). Press `B` to capture a persistent room baseline (`"room_live"` in `~/.config/rf-heatmap/baselines/`), `D` to toggle the **EMI DIFF** overlay (red/amber highlights for `Δ ≥ +8 dB` / `Z ≥ 3.5` new emitters with a `DIFF` header pill), and `Enter` or `6 (LOCK)` to jump straight to the strongest frequency.
- **On-Device Neural ML — `PulseNet-RF` (28 Protocols + Denoiser + 32D Fingerprint) & `SpectrumNet-2D`**: Pure-Go INT8 (`CGO_ENABLED=0`, `0 allocs/op`, `.czml v1` SHA-256 verified) neural suite embedded directly in the binary (~90 KB total; optional user override in `~/.config/rf-heatmap/models/*.czml`). `PulseNet-RF` identifies 28 real-world Sub-GHz protocol families (trained on 11,471 Flipper `.sub` captures, 98.1% clean / 97.0% noisy accuracy), computes 32D SupCon transmitter fingerprints, and runs a lazy 1D-UNet asymmetric AGC denoiser (**`PN-CLEAN`** pass-3 rescue in `DECODE`). `SpectrumNet-2D` classifies live `32×36` thermal waterfall windows into 8 scenes (`Quiet`, `Narrowband_OOK`, `Periodic_Beacon`, `Wideband_EMI_SMPS`, `FHSS_Hopper`, `LoRa_Chirp`, `CW_Jammer`, `RollJam_Suspect`) with real-time `ROLLJAM!` / `CW JAM!` / `FHSS` / `LORA` / `EMI` badges on `HEATMAP`.
- **On-Device DSP — Smart Squelch (`S`) & Auto-Lock (`L`)**: Zero-allocation per-bin EWMA baseline (`internal/rfml/baseline.go`) with Welford warmup, burst-freeze guard (`z ≥ 3.0`), adaptive threshold (`μ + k·σ`, tunable with `F/X` in `PULSE`), and automatic cursor lock onto `z ≥ 4.0` bursts.
- **PULSE (RSSI Scope & RAW Capture)**: Real-time dBm meter, live Z-score badge, CC1101 **`FREQEST` frequency drift (`DF:±kHz`)** readout when hardware is attached, Smart Squelch `k` tuning or manual dBm squelch (`F/X`), fine frequency tuning (`Z/C`), `AM650`/`AM270`/`FM238`/`FM476`/`GFSK99` presets, Flipper `.sub` RAW save/replay, **`G` Foxhunt Geiger RSSI**, and **`T` RAW Trimmer & Repeater (`1x–10x`)**.
- **DECODE (Live OOK & Rolling-Code Analyzer + SENSORS)**: Auto-decodes `Princeton`, `PT2262` (tri-state), `EV1527`, `CAME`, `Linear`, `Holtek` (`HT12E`/`HT6P20B`), `Nice FLO`, `KeeLoq`/`HCS301` (66-bit), and `Somfy Telis` (56-bit Manchester RTS). Detects hopping codes across consecutive presses with an amber **`ROLLING`** badge and 2-step TX replay guard, and recovers weak/jittery captures via **1D K-Means** (`ML-CLEAN`). Press **`R`** to open the live **`SENSORS`** sub-view for **TPMS** (`Schrader` / `Toyota` via `GFSK99`) and **Weather Stations** (`Oregon Scientific`, `Acurite`, `LaCrosse`). Pure-Go **POCSAG** (`512/1200/2400 bps`, BCH(31,21)) and **Wireless M-Bus** (`wM-Bus T1/C1/S1`, `868.95 MHz`) decoders are exposed via MCP.
- **FOXHUNT Geiger Mode (`G`) & RAW Trimmer (`T`)**: Press `G` in `HEATMAP` or `PULSE` for a full-screen single-frequency Foxhunt Geiger RSSI meter with peak-hold marker and proportional click cadence (`Esc` exits back). Press `T` in `PULSE` or `SAVED` to strip leading/trailing noise from RAW captures, set `1x–10x` repeats, and save atomically into `~/rf-heatmap/captures/` (read-only external databases are never modified).
- **SAVED (User `.sub` Library)**: Browse, load, delete, purge noise, and transmit your own Flipper-compatible `.sub` captures strictly from `~/rf-heatmap/captures/` (never touches external databases).
- **PROBE (Auto-Probe, External Databases & Virtual Generators)**: Step automatically through read-only external `.sub` database packs (`~/rf-heatmap/databases/<pack>/*.sub`, `~/subg/files/`), database-free built-in key generators (**`GEN:CAME-12`** 433.92 MHz / 4096 keys, **`GEN:Linear-10`** 315.00 MHz / 1024 keys, **`GEN:CAME-868`** 868.35 MHz / 4096 keys — override freq/mod with `B`/`M`), or your `USER` library at a configurable interval (`0.05s–5.0s`). Hit `4 (STOP)` / `Space` / `Enter` the instant a target device reacts, step back up (`F/X`) using the `>` (last sent) and `*` (last 12 sent) markers, single-test (`5 TRY` / `Enter`), and save the winning `.sub` into your User Library with `6 (SAVE)`.
- **SET (Settings, TX Power, Remote DB Sync & Opt-In MCP Server)**: Configure Smart Squelch (`k`), Auto-Lock, band-calibrated **TX Power (`PATABLE`: `-30, -20, -10, 0, +5, +10 dBm`)**, trigger incremental **Sync DB Sources** (downloads only new/changed `.sub` files from GitHub repos or HTTP JSON manifests in `~/.config/rf-heatmap/sources.json` into isolated `~/rf-heatmap/databases/<id>/` folders, also runnable via `rf-heatmap --sync`), and toggle the built-in pure-Go **MCP Server** (`mcp_enabled`, disabled by default).
- **Built-in 5-Page Help (`H`)**: Interactive manual right on the device.
- **Zero Dependencies**: Pure Go (`CGO_ENABLED=0`), renders directly to `/dev/fb0` (320×170 RGB565) and reads `/dev/input/event*` without X11/Wayland or root privileges.

## Linear Home Menu & Simple / Expert Modes

`rf-heatmap` boots into a clean **Home Menu (`HOME`)** with **Simple Mode** by default (`expert_mode: false`, 5 focused actions with zero scrolling):
1. **`Read & Replay`** — 2-step linear capture & instant test replay (`433.92 / 868.35 / 315 / 915 MHz`, Rolling-code 2-step guard, `5 SAVE` saves `.sub` and opens `My Captures`).
2. **`My Captures`** — clean list of your `.sub` files (`4 SEND`, `5 TRIM`, `6 NEW`, `7 DEL`, `8 MENU`).
3. **`Spectrum Map`** — live Sub-GHz waterfall (`6 LOCK` jumps to `Read & Replay` on the peak frequency; `7 FOX` opens Foxhunt Geiger).
4. **`Sensors`** — live TPMS (433/315 MHz) & Weather Station decoder.
5. **`Settings`** — Expert Mode toggle, TX Power (`-30..+10 dBm`), opt-in MCP Server (`SSH`), DB Sync, Auto Squelch.

Press **`5 EXP-`** on `HOME` (or toggle **`Expert Mode`** in `Settings`) to unlock `+4` engineering screens (**`Pulse Scope`**, **`OOK Decoder`**, **`Auto-Probe`**, **`Foxhunt Geiger`**), `Tab` mode cycling, `[x]` batch multi-select, and full `Settings`.

## Controls

| Key | Action |
| --- | --- |
| `F` / `X` (`↑` / `↓`) | Up / Down navigation (menus, bands, squelch, lists) |
| `Z` / `C` (`←` / `→`) | Left / Right (frequency, cursor channel, help pages) |
| `Enter` | Open item / Replay / Send / Lock peak |
| `4`–`8` | Context-sensitive bottom softkeys |
| `Esc` | Step back (`Screen → HOME`); on `HOME` prompts exit confirmation (`Enter`/`Esc` = YES) |
| `Tab` | Return to `HOME` (Simple Mode) or cycle screens (Expert Mode) |
| `B` / `D` (in `Spectrum Map`) | Capture room baseline (`"room_live"`) / toggle **EMI DIFF** overlay |
| `H` / `Q` | Open 4-page interactive Help / Immediate clean exit |

## Documentation

- **Interactive Learning Portal**: [CardputerZero RF & NFC Learning Portal](https://igenexxx.github.io/cardputer-zero-learning-portal/#/home) — full English guides for [RF Heatmap](https://igenexxx.github.io/cardputer-zero-learning-portal/#/rf-guide), [Sub-GHz & CC1101 Theory](https://igenexxx.github.io/cardputer-zero-learning-portal/#/rf-domain), all 5 On-Device ML stages, and interactive browser labs.
- **Detailed App Guide**: [`docs/GUIDE.md`](docs/GUIDE.md) — in-depth device walkthrough and reference.

## Build & Package (`.deb`)

```bash
go test -race ./...
./scripts/build_deb.sh
```

This produces `dist/rf-heatmap_0.3.9-1_arm64.deb` ready for installation (`sudo dpkg -i`) or publishing via `czdev publish --deb dist/rf-heatmap_0.3.9-1_arm64.deb`.

## Built-In MCP Server (SSH Opt-In)

After enabling **MCP Server (SSH)** in the **`SET`** tab (`~/.config/rf-heatmap/config.json` -> `"mcp_enabled": true`), AI agents can invoke `rf-heatmap` over SSH stdio:

```bash
ssh <cardputer-host> /usr/share/APPLaunch/bin/rf-heatmap --mcp
```

Exposed tools (39): `rf_get_status`, `rf_sweep_band`, `rf_read_rssi`, `rf_measure_freq_offset`, `rf_set_tx_power`, `rf_wor_listen`, `rf_send_packet` (`confirm_rf: true`), `rf_recv_packet`, `rf_save_room_baseline`, `rf_list_room_baselines`, `rf_diff_room_baseline`, `rf_delete_room_baseline` (`confirm: true`), `rf_decode_ook`, `rf_analyze_rolling_code`, `rf_decode_sensor`, `rf_decode_pocsag`, `rf_decode_wmbus`, `rf_analyze_pulses`, `rf_classify_signal`, `rf_classify_spectrum` (`SpectrumNet-2D` 32×36 waterfall scene & RollJam classifier), `rf_model_info` (inspect active embedded/override `.czml` models), `rf_match_fingerprint`, `rf_foxhunt_track`, `rf_foxhunt_sample`, `rf_trim_raw`, `rf_list_saved` (supports `source`/`pack` filter), `rf_list_databases`, `rf_read_saved`, `rf_save_capture`, `rf_promote_to_user`, `rf_transmit_saved` (`confirm_rf: true`), `rf_list_sources`, `rf_add_source`, `rf_remove_source` (`confirm: true`, single or batch `ids[]`), `rf_sync_sources`, `rf_delete_pack_files` (`confirm: true`), `rf_rank_probe_pack`, `rf_delete_saved` (`confirm: true`), `rf_purge_noise` (`dry_run: true` by default; deletion requires `dry_run: false, confirm: true`).

## License

MIT — see [LICENSE](LICENSE).
