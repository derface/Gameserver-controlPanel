#!/usr/bin/env bash
# Aktualisiert das Panel aus dem Git-Repository und installiert es neu.
#   cd /opt/gamepanel-src && bash scripts/update.sh
# Server, Spielstände und Einstellungen bleiben erhalten.
set -euo pipefail
cd "$(dirname "$0")/.."
[ "$(id -u)" -eq 0 ] || { echo "Bitte als root ausführen (sudo -i)." >&2; exit 1; }
command -v go >/dev/null || { echo "Go fehlt: apt install -y golang" >&2; exit 1; }

OLD="$(git describe --tags --always 2>/dev/null || echo '?')"
echo "▸ Hole neuesten Stand (aktuell: $OLD)"
git pull --ff-only --tags

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) echo "Nicht unterstützte Architektur: $(uname -m)" >&2; exit 1 ;;
esac

echo "▸ Baue Panel"
make dist >/dev/null
bash scripts/install.sh --binary "dist/gamepanel-linux-$ARCH"
