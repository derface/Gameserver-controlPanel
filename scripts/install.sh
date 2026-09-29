#!/usr/bin/env bash
# [Panel] – Installer für Linux
#
#   curl -fsSL https://<deine-domain>/install.sh | sudo bash
#   sudo ./install.sh --binary ./gamepanel-linux-amd64      # lokale Datei installieren
#   sudo ./install.sh --uninstall [--purge]
#
# Richtet Docker ein (falls nötig), installiert das Panel nach /opt/gamepanel
# und startet es als systemd-Dienst.
set -euo pipefail

REPO="${GP_REPO:-derface/Gameserver-controlPanel}"
PORT="${GP_PORT:-8420}"
INSTALL_DIR=/opt/gamepanel
DATA_DIR=/var/lib/gamepanel
UNIT=/etc/systemd/system/gamepanel.service
BINARY=""
UNINSTALL=0
PURGE=0

c_ok=$'\e[32m'; c_warn=$'\e[33m'; c_err=$'\e[31m'; c_dim=$'\e[2m'; c_off=$'\e[0m'
say()  { echo "${c_ok}▸${c_off} $*"; }
warn() { echo "${c_warn}!${c_off} $*"; }
die()  { echo "${c_err}✗ $*${c_off}" >&2; exit 1; }

while [ $# -gt 0 ]; do
  case "$1" in
    --binary) BINARY="$2"; shift 2 ;;
    --port) PORT="$2"; shift 2 ;;
    --uninstall) UNINSTALL=1; shift ;;
    --purge) PURGE=1; shift ;;
    -h|--help) sed -n '2,10p' "$0"; exit 0 ;;
    *) die "Unbekannte Option: $1" ;;
  esac
done

[ "$(id -u)" -eq 0 ] || die "Bitte mit sudo bzw. als root ausführen."
command -v systemctl >/dev/null || die "systemd wird benötigt."

if [ "$UNINSTALL" -eq 1 ]; then
  say "Panel wird entfernt"
  systemctl disable --now gamepanel 2>/dev/null || true
  rm -f "$UNIT" && systemctl daemon-reload
  rm -rf "$INSTALL_DIR"
  if [ "$PURGE" -eq 1 ]; then
    warn "Lösche alle Gameserver-Container und $DATA_DIR"
    docker ps -aq --filter label=gamepanel.id | xargs -r docker rm -f
    rm -rf "$DATA_DIR"
  else
    echo "  Daten bleiben in $DATA_DIR, Gameserver-Container bleiben bestehen."
  fi
  say "Fertig."
  exit 0
fi

case "$(uname -m)" in
  x86_64|amd64) ARCH=amd64 ;;
  aarch64|arm64) ARCH=arm64 ;;
  *) die "Nicht unterstützte Architektur: $(uname -m)" ;;
esac

if   command -v apt-get >/dev/null; then PM=apt
elif command -v dnf     >/dev/null; then PM=dnf
elif command -v yum     >/dev/null; then PM=yum
elif command -v pacman  >/dev/null; then PM=pacman
elif command -v zypper  >/dev/null; then PM=zypper
else PM=unknown; fi

pkg_install() {
  case "$PM" in
    apt) DEBIAN_FRONTEND=noninteractive apt-get update -qq && DEBIAN_FRONTEND=noninteractive apt-get install -y -qq "$@" ;;
    dnf) dnf install -y -q "$@" ;;
    yum) yum install -y -q "$@" ;;
    pacman) pacman -Sy --noconfirm --needed "$@" ;;
    zypper) zypper --non-interactive install "$@" ;;
    *) die "Bitte installiere selbst: $*" ;;
  esac
}

say "System: $(. /etc/os-release 2>/dev/null && echo "$PRETTY_NAME" || uname -s), $ARCH"
command -v curl >/dev/null || pkg_install curl ca-certificates

# ---------- Docker ----------
if ! command -v docker >/dev/null; then
  say "Docker wird installiert"
  if [ "$PM" = pacman ]; then pkg_install docker
  else curl -fsSL https://get.docker.com | sh; fi
fi
systemctl enable --now docker >/dev/null 2>&1 || true
docker info >/dev/null 2>&1 || die "Docker läuft nicht. Prüfen mit: systemctl status docker"
say "Docker $(docker version --format '{{.Server.Version}}' 2>/dev/null) läuft"

# ---------- Panel ----------
mkdir -p "$INSTALL_DIR" "$DATA_DIR/templates"
chmod 750 "$DATA_DIR"
TMP="$(mktemp)"
if [ -n "$BINARY" ]; then
  [ -f "$BINARY" ] || die "Datei nicht gefunden: $BINARY"
  cp "$BINARY" "$TMP"
else
  URL="https://github.com/$REPO/releases/latest/download/gamepanel-linux-$ARCH"
  say "Lade Panel: $URL"
  AUTH=()
  [ -n "${GP_TOKEN:-}" ] && AUTH=(-H "Authorization: Bearer $GP_TOKEN")
  curl -fL --progress-bar "${AUTH[@]}" -o "$TMP" "$URL" || die "Download fehlgeschlagen. Bei einem privaten Repo: --binary <datei> verwenden."
fi
install -m 755 "$TMP" "$INSTALL_DIR/gamepanel"; rm -f "$TMP"
VERSION="$("$INSTALL_DIR/gamepanel" -version 2>/dev/null || echo '?')"

cat > "$UNIT" <<EOF
[Unit]
Description=[Panel] Gameserver Control Panel
Documentation=https://github.com/$REPO
After=network-online.target docker.service
Wants=network-online.target
Requires=docker.service

[Service]
ExecStart=$INSTALL_DIR/gamepanel -listen :$PORT -data $DATA_DIR
Restart=on-failure
RestartSec=3
LimitNOFILE=65536

[Install]
WantedBy=multi-user.target
EOF
systemctl daemon-reload
systemctl enable gamepanel >/dev/null 2>&1
systemctl restart gamepanel

# ---------- Firewall ----------
if command -v ufw >/dev/null && ufw status 2>/dev/null | grep -q "Status: active"; then
  ufw allow "$PORT"/tcp >/dev/null && say "ufw: Port $PORT/tcp freigegeben"
elif command -v firewall-cmd >/dev/null && firewall-cmd --state >/dev/null 2>&1; then
  firewall-cmd --permanent --add-port="$PORT"/tcp >/dev/null && firewall-cmd --reload >/dev/null && say "firewalld: Port $PORT/tcp freigegeben"
fi

sleep 1
systemctl is-active --quiet gamepanel || die "Das Panel startet nicht. Details: journalctl -u gamepanel -n 50"
IP="$(hostname -I 2>/dev/null | awk '{print $1}')"
echo
say "Panel $VERSION läuft."
echo "   Öffne im Browser:  http://${IP:-<server-ip>}:$PORT"
echo "   Beim ersten Aufruf legst du dein Admin-Konto an."
echo "${c_dim}   Logs: journalctl -u gamepanel -f   ·   Daten: $DATA_DIR${c_off}"
