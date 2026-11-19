#!/usr/bin/env bash
set -euo pipefail

PKG="rf-heatmap"
VER="0.1.0"
REV="1"
ARCH="arm64"
DEB_NAME="${PKG}_${VER}-${REV}_${ARCH}.deb"

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

echo "[1/4] Running tests..."
go test ./...

echo "[2/4] Cross-compiling ${ARCH} binary..."
mkdir -p dist
CGO_ENABLED=0 GOOS=linux GOARCH=arm64 go build -ldflags="-s -w" -o "dist/${PKG}" ./cmd/rf-heatmap

echo "[3/4] Staging Debian package..."
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT

mkdir -p "$STAGE/DEBIAN"
mkdir -p "$STAGE/usr/share/APPLaunch/bin"
mkdir -p "$STAGE/usr/share/APPLaunch/applications"
mkdir -p "$STAGE/usr/share/APPLaunch/share/images"

install -m 0755 "dist/${PKG}" "$STAGE/usr/share/APPLaunch/bin/${PKG}"
install -m 0644 "assets/images/rf-heatmap-icon.png" "$STAGE/usr/share/APPLaunch/share/images/rf-heatmap-icon.png"

cat > "$STAGE/usr/share/APPLaunch/applications/${PKG}.desktop" <<EOF
[Desktop Entry]
Type=Application
Name=RF Heatmap
Comment=CC1101 Sub-GHz thermal waterfall heatmap, RSSI scope & Flipper .sub studio
Exec=/usr/share/APPLaunch/bin/${PKG}
Terminal=false
Sysplause=true
Icon=share/images/rf-heatmap-icon.png
Categories=Utility;
StartupNotify=false
EOF

SIZE_KB="$(du -sk "$STAGE/usr" | awk '{print $1}')"
cat > "$STAGE/DEBIAN/control" <<EOF
Package: ${PKG}
Version: ${VER}-${REV}
Section: utils
Priority: optional
Architecture: ${ARCH}
Installed-Size: ${SIZE_KB}
Maintainer: Zhenya <zhenya@cardputer.cc>
Description: CC1101 Sub-GHz thermal waterfall heatmap and Flipper .sub studio
 RF Heatmap turns CardputerZero with Cap CC1101 into a pocket Sub-GHz
 spectrum analyzer with thermal waterfall, RSSI squelch scope, OOK decoder,
 and Flipper-compatible .sub library.
EOF

echo "[4/4] Building dist/${DEB_NAME}..."
dpkg-deb --root-owner-group --build "$STAGE" "dist/${DEB_NAME}"
ls -lh "dist/${DEB_NAME}"
