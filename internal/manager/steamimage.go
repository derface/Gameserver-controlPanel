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
const SteamImage = "gamepanel/steam-runtime:12"

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
LOGIN="anonymous"
if [ -n "${GP_STEAM_USER}" ]; then LOGIN="${GP_STEAM_USER}"; fi
steam() {
  /opt/steamcmd/steamcmd.sh "$@" +quit 2>&1 | tee /tmp/gp-steamcmd.log
  rc=${PIPESTATUS[0]}
  # "Logging in using cached credentials ... OK" is the normal case; only
  # real refusals or a password prompt mean the login has to be renewed
  if [ -n "${GP_STEAM_USER}" ] && grep -qiE "cached credentials not found|login failure|invalid password|two-factor code|steam ?guard code|account logon denied|not been authenticated|^password:" /tmp/gp-steamcmd.log; then
    echo "[panel] Steam-Anmeldung fehlt oder ist abgelaufen. Bitte im Panel unter Einstellungen → Steam-Konto neu anmelden."
    # do not exit: Docker would restart us and Steam blocks repeated logins
    exec sleep infinity
  fi
  return $rc
}
# After a failed SteamCMD run: show what Steam's own log says, and clear the
# app's download state. "state is 0x6" often means a stuck manifest or a
# half download; with them gone the next try starts clean.
steam_repair() {
  local app="$1" dir="$2"
  local code
  code=$(grep -oE "state is 0x[0-9a-fA-F]+" /tmp/gp-steamcmd.log | tail -1 | grep -oE "0x[0-9a-fA-F]+" || true)
  for f in /opt/steamcmd/logs/content_log.txt "${HOME}/Steam/logs/content_log.txt" "${HOME}/.local/share/Steam/logs/content_log.txt"; do
    if [ -f "$f" ]; then
      echo "[panel] Steam-Protokoll (content_log, letzte Meldungen):"
      grep -iE "error|fail|timeout|timed out|resolve|unauthor|denied|disk|space|write|connect" "$f" | tail -n 12 | sed 's/^/[steam] /' || true
      break
    fi
  done
  if [ -n "$code" ]; then
    echo "[panel] SteamCMD meldet ${code}: Download-Stand von App ${app} wird zurückgesetzt"
    # SteamCMD leaves StateFlags "6" in the manifest after a failed job and
    # then stops at once on every later run ("progress: 0.00 (0 / 0)").
    # Every manifest that is not "fully installed" (4) is moved aside.
    for m in "${dir}"/steamapps/appmanifest_*.acf; do
      [ -f "$m" ] || continue
      if [ "$m" = "${dir}/steamapps/appmanifest_${app}.acf" ] || ! grep -qE '"StateFlags"[[:space:]]+"4"' "$m"; then
        mv -f "$m" "$m.bak" 2>/dev/null || true
      fi
    done
    rm -rf "${dir}/steamapps/downloading/${app}" "${dir}/steamapps/downloading/state_${app}"* "${dir}/steamapps/temp/${app}" 2>/dev/null || true
    VALIDATE="validate"
  fi
}
# Install with "validate" only the first time. Updates run when the panel
# started the server (it leaves /data/.gp-update-now), not when Docker
# restarts it after a crash: otherwise every crash checks gigabytes again.
NEED=""
if [ ! -f /data/server/.gp-installed ]; then
  NEED=install
elif [ "${GP_UPDATE:-1}" = "1" ] && [ -f /data/.gp-update-now ]; then
  NEED=update
elif [ "${GP_UPDATE:-1}" = "1" ]; then
  echo "[panel] Neustart nach Absturz: ohne Update-Prüfung"
fi
rm -f /data/.gp-update-now 2>/dev/null || true
VALIDATE=""
if [ "${NEED}" = "install" ]; then VALIDATE="validate"; fi
if [ -n "${NEED}" ]; then
  echo "[panel] SteamCMD: App ${GP_APP_ID} wird installiert oder aktualisiert"
  BETA=""
  if [ -n "${GP_BETA}" ]; then BETA="-beta ${GP_BETA}"; fi
  MOD=""
  if [ -n "${GP_MOD}" ]; then MOD="+app_set_config 90 mod ${GP_MOD}"; fi
  for i in 1 2 3 4; do
    if [ -n "${GP_BASE_APP}" ]; then
      echo "[panel] SteamCMD: Basis-App ${GP_BASE_APP} nach ${GP_BASE_DIR}"
      mkdir -p "/data/server/${GP_BASE_DIR}"
      if ! steam ${PLATFORM} +force_install_dir "/data/server/${GP_BASE_DIR}" +login "${LOGIN}" +app_update "${GP_BASE_APP}" ${VALIDATE}; then
        steam_repair "${GP_BASE_APP}" "/data/server/${GP_BASE_DIR}"
        echo "[panel] SteamCMD Versuch ${i} fehlgeschlagen, neuer Versuch in 10 s"; sleep 10; continue
      fi
    fi
    if [ -n "${GP_PREPLATFORM}" ] && [ ! -f /data/server/.gp-preplatform ]; then
      echo "[panel] SteamCMD: Erstinstallation mit Plattform ${GP_PREPLATFORM}"
      steam +@sSteamCmdForcePlatformType "${GP_PREPLATFORM}" +force_install_dir /data/server +login "${LOGIN}" +app_update "${GP_APP_ID}" validate && touch /data/server/.gp-preplatform || true
    fi
    if steam ${PLATFORM} +force_install_dir /data/server +login "${LOGIN}" ${MOD} +app_update "${GP_APP_ID}" ${BETA} ${VALIDATE}; then
      if [ -z "${GP_MOD}" ] || [ -d "/data/server/${GP_MOD}" ]; then
        touch /data/server/.gp-installed
        break
      fi
      echo "[panel] SteamCMD: Mod ${GP_MOD} fehlt noch"
    else
      steam_repair "${GP_APP_ID}" /data/server
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
# Java takes its home from /etc/passwd, not from $HOME; without this,
# Project Zomboid kept world and settings inside the container.
export JAVA_TOOL_OPTIONS="-Duser.home=/data/home ${JAVA_TOOL_OPTIONS:-}"
mkdir -p ~/.steam/sdk64 ~/.steam/sdk32
cp -f /opt/steamcmd/linux64/steamclient.so ~/.steam/sdk64/ 2>/dev/null || true
cp -f /opt/steamcmd/linux32/steamclient.so ~/.steam/sdk32/ 2>/dev/null || true
cd /data/server
if [ "${GP_PLATFORM}" = "windows" ]; then
  export WINEPREFIX=/data/home/.wine WINEDEBUG=fixme-all WINEARCH=win64
  # own virtual screen without access control: xvfb-run's Xauthority does
  # not always reach Wine in the container ("no driver could be loaded")
  if [ -z "${DISPLAY}" ] && command -v Xvfb >/dev/null; then
    # after a crash Docker restarts the same container: the old lock file
    # names a PID that now belongs to another process, and Xvfb refuses
    # to start ("no driver could be loaded" in Wine)
    rm -f /tmp/.X99-lock /tmp/.X11-unix/X99
    Xvfb :99 -screen 0 1280x1024x24 -nolisten tcp -ac >/tmp/xvfb.log 2>&1 &
    export DISPLAY=:99
    for i in $(seq 1 100); do [ -S /tmp/.X11-unix/X99 ] && break; sleep 0.1; done
    if [ ! -S /tmp/.X11-unix/X99 ]; then
      echo "[panel] Der virtuelle Bildschirm (Xvfb) startet nicht:"; tail -n 5 /tmp/xvfb.log
    fi
  fi
  # A prefix whose setup was interrupted (container stopped during
  # wineboot) can look complete and still fail with "could not load
  # kernel32.dll". So the prefix is tried once per Wine version; a broken
  # one is moved aside, never deleted, because some games keep save games
  # in it.
  WV="$(wine --version 2>/dev/null)"
  # no display and no Mono/Gecko: otherwise Wine may wait forever for a
  # click in an install dialog on the invisible screen
  wine_ok() { env -u DISPLAY WINEDLLOVERRIDES="mscoree,mshtml=" timeout 180 wine cmd /c ver 2>&1 | grep -q "Microsoft Windows"; }
  if [ -f "$WINEPREFIX/system.reg" ] && [ "$(cat "$WINEPREFIX/.gp-ok" 2>/dev/null)" != "$WV" ]; then
    echo "[panel] Wine-Umgebung wird geprüft (${WV})"
    if wine_ok; then
      echo "$WV" > "$WINEPREFIX/.gp-ok"
    else
      OLD="$WINEPREFIX-defekt-$(date +%Y%m%d-%H%M%S)"
      echo "[panel] Wine-Umgebung ist beschädigt, wird neu eingerichtet (alte nach ${OLD#/data/})"
      wineserver -k 2>/dev/null || true
      mv "$WINEPREFIX" "$OLD"
    fi
  fi
  if [ ! -f "$WINEPREFIX/system.reg" ]; then
    echo "[panel] Wine wird eingerichtet (einmalig, 1 bis 3 Minuten)"
    env -u DISPLAY WINEDLLOVERRIDES="mscoree,mshtml=" timeout 600 wineboot --init 2>&1 | grep -iv 'fixme\|^$' | tail -n 15
    timeout 120 wineserver -w 2>/dev/null || wineserver -k 2>/dev/null || true
    if wine_ok; then
      echo "$WV" > "$WINEPREFIX/.gp-ok"
      echo "[panel] Wine ist eingerichtet (${WV})"
    else
      echo "[panel] Wine konnte nicht eingerichtet werden (Meldungen oben, ${WV})"
    fi
  fi
  # The checks above ran without a screen. Their wineserver lingers for a
  # few seconds and would hand the game a desktop without display driver
  # ("Application tried to create a window, but no driver could be loaded").
  wineserver -k 2>/dev/null || true
  timeout 20 wineserver -w 2>/dev/null || true
fi
# Steam Workshop: the panel keeps the list in /data/.gp-workshop
# ("app <id>" and one item per line). Downloads run at installs, updates and
# whenever the list changed; the game-specific hook links the mods in.
if [ -f /data/.gp-workshop ]; then
  WS_APP=$(sed -n 's/^app \([0-9]*\).*/\1/p' /data/.gp-workshop | head -n 1)
  WS_IDS=$(grep -E '^[0-9]+$' /data/.gp-workshop | tr '\n' ' ')
  if [ -n "${WS_APP}" ] && [ -n "${WS_IDS}" ] && { [ -n "${NEED}" ] || [ ! -f /data/.gp-workshop-status ] || [ /data/.gp-workshop -nt /data/.gp-workshop-status ]; }; then
    echo "[panel] SteamCMD: Workshop-Inhalte für App ${WS_APP} werden geladen: ${WS_IDS}"
    ARGS=""
    for id in ${WS_IDS}; do ARGS="${ARGS} +workshop_download_item ${WS_APP} ${id}"; done
    steam ${PLATFORM} +force_install_dir /data/server +login "${LOGIN}" ${ARGS} || true
    RETRY=""
    for id in ${WS_IDS}; do [ -d "/data/server/steamapps/workshop/content/${WS_APP}/${id}" ] || RETRY="${RETRY} +workshop_download_item ${WS_APP} ${id}"; done
    if [ -n "${RETRY}" ] && [ "${LOGIN}" = "anonymous" ] && [ -n "${GP_WS_USER}" ]; then
      echo "[panel] Workshop: zweiter Versuch mit dem Steam-Konto des Panels"
      mkdir -p /tmp/wsacct && ln -sfn /gp-steam-acct /tmp/wsacct/Steam
      HOME=/tmp/wsacct /opt/steamcmd/steamcmd.sh ${PLATFORM} +force_install_dir /data/server +login "${GP_WS_USER}" ${RETRY} +quit 2>&1 | tee -a /tmp/gp-steamcmd.log || true
      if grep -qiE "cached credentials not found|login failure|invalid password|two-factor code|steam ?guard code|account logon denied|not been authenticated" /tmp/gp-steamcmd.log; then WS_LOGINFAIL=1; fi
    fi
    : > /data/.gp-workshop-status.tmp
    for id in ${WS_IDS}; do
      if [ -d "/data/server/steamapps/workshop/content/${WS_APP}/${id}" ]; then
        echo "${id} ok" >> /data/.gp-workshop-status.tmp
      else
        why=$(grep -iE "${id}" /tmp/gp-steamcmd.log | grep -iE "error|fail|denied|timeout" | tail -n 1 | tr -d '\r' | cut -c1-200)
        if [ -n "${WS_LOGINFAIL}" ]; then why="Steam-Anmeldung des Panels fehlt oder ist abgelaufen (Einstellungen → Steam-Konto). ${why}"; fi
        echo "${id} fehler ${why:-nicht geladen}" >> /data/.gp-workshop-status.tmp
        echo "[panel] Workshop ${id}: ${why:-nicht geladen}"
      fi
    done
    mv -f /data/.gp-workshop-status.tmp /data/.gp-workshop-status
  fi
  export GP_WS_APP="${WS_APP}" GP_WS_IDS="${WS_IDS}" GP_WS_DIR="/data/server/steamapps/workshop/content/${WS_APP}"
  if [ -f /data/.gp-workshop-after.sh ]; then
    (cd /data/server && bash /data/.gp-workshop-after.sh) || echo "[panel] Einbindung der Workshop-Mods meldet einen Fehler"
  fi
fi
export GP_WS_MODS="$(cat /data/.gp-ws-mods 2>/dev/null)"
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
      libgl1 libglx-mesa0 libgl1-mesa-dri libegl1 libvulkan1 mesa-vulkan-drivers libgnutls30 \
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
	// the layer text is part of the name: a changed layer is built anew
	layer := ""
	if tpl.Runtime == "wine" {
		layer = wineLayer
	}
	h := sha256.Sum256([]byte(tpl.Runtime + "|" + strings.Join(pk, ",") + "|" + layer))
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
