package manager

// The steam runtime image is built locally on first use from this tiny
// build context. It contains SteamCMD and the libraries most Linux
// dedicated servers need. Every Steam game shares this one image; the game
// itself is downloaded into the server's data folder on first start.

const SteamImage = "gamepanel/steam-runtime:1"

const steamDockerfile = `FROM debian:bookworm-slim
ENV DEBIAN_FRONTEND=noninteractive LANG=en_US.UTF-8
RUN dpkg --add-architecture i386 && apt-get update && apt-get install -y --no-install-recommends \
      ca-certificates curl tini procps locales xdg-user-dirs \
      lib32gcc-s1 lib32stdc++6 libc6:i386 libstdc++6:i386 libcurl4 \
      libatomic1 libpulse0 libsdl2-2.0-0 \
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
mkdir -p /data/server
if [ "${GP_UPDATE:-1}" = "1" ] || [ ! -f /data/server/.gp-installed ]; then
  echo "[panel] SteamCMD: App ${GP_APP_ID} wird installiert oder aktualisiert"
  BETA=""
  if [ -n "${GP_BETA}" ]; then BETA="-beta ${GP_BETA}"; fi
  for i in 1 2 3; do
    if /opt/steamcmd/steamcmd.sh +force_install_dir /data/server +login anonymous +app_update "${GP_APP_ID}" ${BETA} validate +quit; then
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
mkdir -p ~/.steam/sdk64 ~/.steam/sdk32
cp -f /opt/steamcmd/linux64/steamclient.so ~/.steam/sdk64/ 2>/dev/null || true
cp -f /opt/steamcmd/linux32/steamclient.so ~/.steam/sdk32/ 2>/dev/null || true
cd /data/server
echo "[panel] Start: ${GP_START}"
exec bash -c "${GP_START}"
`
