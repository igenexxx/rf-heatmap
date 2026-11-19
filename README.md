# RF Heatmap for CardputerZero

A fast, user-friendly **CC1101 Sub-GHz Thermal Waterfall, RSSI Oscilloscope, OOK Protocol Decoder & Flipper `.sub` Studio** for the **M5Stack CardputerZero** + **Cap CC1101**.

![HEATMAP](assets/screenshots/heatmap.png)

## Features

- **HEATMAP (Spectrum + Thermal Waterfall)**: Sweep `433 ISM`, `868 EU`, `315 US`, `915 ISM`, or `ALL ISM` (`300–928 MHz`) with live RSSI bars, amber Peak-Hold markers, and a scrolling Inferno thermal waterfall (`-110 dBm` to `-35 dBm`). Press `Enter` or `6 (LOCK)` to jump straight to the strongest frequency.
- **PULSE (RSSI Scope & RAW Capture)**: Real-time dBm meter, adjustable Squelch threshold (`F/X`), fine frequency tuning (`Z/C`), `AM650`/`AM270`/`FM238`/`FM476` presets, and Flipper `.sub` RAW save/replay.
- **DECODE (Live OOK Analyzer)**: Auto-decodes `Princeton`, `CAME`, and `Linear` OOK frames, showing protocol, bit length, hex key, and base pulse width `TE (µs)`.
- **SAVED (`.sub` Library)**: Browse, load, delete, and transmit Flipper-compatible `.sub` files from `~/rf-heatmap/captures/` and `~/subg/files/`.
- **Built-in 4-Page Help (`H`)**: Interactive manual right on the device.
- **Zero Dependencies**: Pure Go (`CGO_ENABLED=0`), renders directly to `/dev/fb0` (320×170 RGB565) and reads `/dev/input/event*` without X11/Wayland or root privileges.

## Controls

| Key | Action |
| --- | --- |
| `4`–`8` | Bottom navigation bar actions |
| `Tab` / `8` | Switch mode (`HEATMAP → PULSE → DECODE → SAVED`) |
| `F` / `X` (`↑` / `↓`) | Up / Down (band, squelch, list selection) |
| `Z` / `C` (`←` / `→`) | Left / Right (cursor channel, frequency step) |
| `B` / `M` | Cycle ISM band / modulation preset |
| `Enter` | Lock peak frequency / transmit |
| `H` | Open 4-page interactive Help |
| `Q` / `Esc` | Quit safely (restores `GPIO 14` & puts CC1101 in `IDLE`) |

## Build & Package (`.deb`)

```bash
go test -race ./...
./scripts/build_deb.sh
```

This produces `dist/rf-heatmap_0.1.0-1_arm64.deb` ready for installation (`sudo dpkg -i`) or publishing via `czdev publish --deb dist/rf-heatmap_0.1.0-1_arm64.deb`.

## License

MIT — see [LICENSE](LICENSE).
