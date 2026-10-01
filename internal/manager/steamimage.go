package manager

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"regexp"
	"sort"
	"strings"

	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// The steam runtime image is built locally on first use from this tiny
// build context. It contains SteamCMD and the libraries most Linux
// dedicated servers need. Every Steam game shares this one image; the game
// itself is downloaded into the server's data folder on first start.

// Bump the tag whenever the Dockerfile changes: containers built from an
// older tag are recreated automatically on their next start.
const SteamImage = "gamepanel/steam-runtime:4"

const steamDockerfile = `FROM debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive LANG=en_US.UTF-8
RUN dpkg --add-architecture i386 && apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl tini procps locales xdg-user-dirs \
      lib32gcc-s1 lib32stdc++6 libc6:i386 libstdc++6:i386 libcurl4 \
      libatomic1 libpulse0 libpulse-mainloop-glib0 libsdl2-2.0-0 \
 && sed -i 's/^# *en_US.UTF-8/en_US.UTF-8/' /etc/locale.gen && locale-gen \
 && rm -rf /var/lib/apt/lists/*
RUN useradd -m -u 1000 -s /bin/bash steam && mkdir -p /opt/steamcmd /data && chown steam:steam /opt/steamcmd /data
USER steam
RUN curl -fsSL https://steamcdn-a.akamaihd.net/client/installer/steamcmd_linux.tar.gz | tar xz -C /opt/steamcmd \
 && (/opt/steamcmd/steamcmd.sh +quit || true)
COPY gp-run /usr/local/bin/gp-run
WORKDIR /data
ENTRYPOINT ["/usr/bin/tini", "-g", "--", "/usr/local/bin/gp-run"]
`

const steamRunScript = `#!/bin/bash
# Installs/updates the game with SteamCMD, then starts it.
set -e
mkdir -p /data/server /data/logs
PLATFORM=""
if [ "${GP_PLATFORM}" = "windows" ]; then PLATFORM="+@sSteamCmdForcePlatformType windows"; fi
if [ "${GP_UPDATE:-1}" = "1" ] || [ ! -f /data/server/.gp-installed ]; then
  echo "[panel] SteamCMD: App ${GP_APP_ID} wird installiert oder aktualisiert"
  BETA=""
  if [ -n "${GP_BETA}" ]; then BETA="-beta ${GP_BETA}"; fi
  for i in 1 2 3; do
    if /opt/steamcmd/steamcmd.sh ${PLATFORM} +force_install_dir /data/server +login anonymous +app_update "${GP_APP_ID}" ${BETA} validate +quit; then
      touch /data/server/.gp-installed
      break
    fi
    echo "[panel] SteamCMD Versuch ${i} fehlgeschlagen, neuer Versuch in 10 s"
    sleep 10
  done
  if [ ! -f /data/server/.gp-installed ]; then
    echo "[panel] Installation fehlgeschlagen"
    exit 1
  fi
fi
# Spiele speichern oft im Home-Verzeichnis (Zomboid, 7 Days to Die,
# Satisfactory). /data/home liegt im Serverordner und bleibt erhalten.
mkdir -p /data/home
export HOME=/data/home
mkdir -p ~/.steam/sdk64 ~/.steam/sdk32
cp -f /opt/steamcmd/linux64/steamclient.so ~/.steam/sdk64/ 2>/dev/null || true
cp -f /opt/steamcmd/linux32/steamclient.so ~/.steam/sdk32/ 2>/dev/null || true
cd /data/server
if [ "${GP_PLATFORM}" = "windows" ]; then
  export WINEPREFIX=/data/home/.wine WINEDEBUG=-all WINEARCH=win64
  if [ ! -f "$WINEPREFIX/system.reg" ]; then
    echo "[panel] Wine wird eingerichtet (einmalig)"
    wineboot --init >/dev/null 2>&1 || true
  fi
fi
echo "[panel] Start: ${GP_START}"
if [ -n "${GP_LOGFILE}" ]; then
  # Das Spiel schreibt in eine Datei statt in die Konsole: mitlesen.
  mkdir -p "$(dirname "${GP_LOGFILE}")"; : > "${GP_LOGFILE}"
  bash -c "${GP_START}" &
  GAME=$!
  trap 'kill -INT $GAME 2>/dev/null; wait $GAME' INT TERM
  tail -n +1 -F "${GP_LOGFILE}" --pid=$GAME 2>/dev/null &
  wait $GAME
  exit $?
fi
exec bash -c "${GP_START}"
`

// Extra layers on top of the base image, built on demand per template.
// Wine comes from WineHQ because Debian's version is too old for current
// Unreal/Unity Windows servers.
const wineLayer = `RUN mkdir -pm755 /etc/apt/keyrings \
 && curl -fsSL https://dl.winehq.org/wine-builds/winehq.key -o /etc/apt/keyrings/winehq-archive.key \
 && curl -fsSL https://dl.winehq.org/wine-builds/debian/dists/bookworm/winehq-bookworm.sources -o /etc/apt/sources.list.d/winehq-bookworm.sources \
 && apt-get update && apt-get install -y --install-recommends winehq-stable \
 && apt-get install -y --no-install-recommends xvfb xauth cabextract winbind \
 && rm -rf /var/lib/apt/lists/*
`

var pkgName = regexp.MustCompile(`^[a-z0-9][a-z0-9.+-]*(:i386|:amd64)?$`)

// SteamImageFor returns the image a Steam template runs in.
func SteamImageFor(tpl templates.Template) string {
	if tpl.Runtime == "" && len(tpl.Packages) == 0 {
		return SteamImage
	}
	pk := append([]string(nil), tpl.Packages...)
	sort.Strings(pk)
	h := sha256.Sum256([]byte(tpl.Runtime + "|" + strings.Join(pk, ",")))
	return SteamImage + "-" + hex.EncodeToString(h[:])[:10]
}

func layerDockerfile(tpl templates.Template) (string, error) {
	var b strings.Builder
	b.WriteString("FROM " + SteamImage + "\nUSER root\n")
	if tpl.Runtime == "wine" {
		b.WriteString(wineLayer)
	} else if tpl.Runtime != "" {
		return "", fmt.Errorf("unbekannte Laufzeit %q", tpl.Runtime)
	}
	if len(tpl.Packages) > 0 {
		for _, p := range tpl.Packages {
			if !pkgName.MatchString(p) {
				return "", fmt.Errorf("ungültiger Paketname %q", p)
			}
		}
		b.WriteString("RUN apt-get update && apt-get install -y --no-install-recommends " + strings.Join(tpl.Packages, " ") + " && rm -rf /var/lib/apt/lists/*\n")
	}
	b.WriteString("USER steam\n")
	return b.String(), nil
}
