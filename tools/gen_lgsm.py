#!/usr/bin/env python3
"""Erzeugt Spiel-Vorlagen aus den Daten von LinuxGSM (MIT-Lizenz,
https://github.com/GameServerManagers/LinuxGSM).

    git clone --depth 1 https://github.com/GameServerManagers/LinuxGSM /tmp/lgsm
    python3 tools/gen_lgsm.py /tmp/lgsm internal/templates/games/community

Übernommen werden nur Steam-Spiele, deren Ports sich eindeutig bestimmen
lassen. Alle erzeugten Vorlagen haben den Status "beta" (ungetestet).
"""
import csv, json, os, re, sys

LGSM, OUT = sys.argv[1], sys.argv[2]
L = os.path.join(LGSM, "lgsm")

# Diese Spiele haben handgepflegte Vorlagen im Panel
SKIP = {"vh", "pw", "cs2", "sf", "pz", "sdtd", "rust", "gmod", "tf2", "mc", "mcb", "pmc", "vpmc", "wmc", "mcv",
        "scpsl", "scpslsm",   # braucht mono-complete
        "vints",              # braucht .NET 10 aus Microsoft-Quellen
        "dst",                # mehrere Shards, eigene Clusterdateien nötig
        "ts3",
        "armar",              # braucht eine eigene JSON-Konfiguration
        # Moddateien kommen nicht über Steam (LinuxGSM lädt sie von eigenen Servern)
        "ahl", "bb", "ns", "ts", "vs", "sfc", "zmr",
        # braucht eine grafische Startoberfläche und eigene Konfigurationsdateien
        "wurm"}

# Im Selbsttest abgestürzt, Ursache noch offen: bleiben drin, aber "defekt"
# (im Panel ausgeblendet, der Selbsttest prüft sie weiter).
BROKEN = {
    "dab": "Selbsttest 01.10.2026: Segmentation fault beim Start",
    "ios": "Selbsttest 01.10.2026: Segmentation fault beim Start",
    "zps": "Selbsttest 01.10.2026: Segmentation fault beim Start",
    "dys": "Selbsttest 01.10.2026: Segmentation fault beim Start",
    "nd": "Selbsttest 01.10.2026: gameinfo.txt nicht gefunden",
    "ti": "Selbsttest 01.10.2026: stürzt beim Laden der Plugins ab",
}

# Im Selbsttest vom 01.10.2026 bestanden (Download, Start, Spielerabfrage).
# Nur diese bekommen "beta", alle anderen "experimentell" bis zum nächsten Test.
PASSED = set("ark bd bmdm bb2 cc cs css dods dodr em fof hl2dm hldms hldm hw jbep3 l4d nmrih ohd pvkii sm squad44 sbots sven tf".split())
# zweiter Lauf 02.10.2026, nach den Korrekturen
PASSED |= set("cmw ck cscz csgo dod dmc l4d2 mh ns2 opfor pvr ricochet tf2c tfc tu".split())
# dritter Lauf 08.10.2026
PASSED |= set("kf2 rw squad unt".split())

# Nacharbeiten, die LinuxGSM in seinen fix_*.sh macht, als Befehle vor dem Start
# (laufen im Ordner des Programms). Platzhalter: SC32/SC64 = steamclient.so.
SC32, SC64 = "/opt/steamcmd/linux32/steamclient.so", "/opt/steamcmd/linux64/steamclient.so"
FIXES = {
    "csgo": ["[ -f bin/libgcc_s.so.1 ] && mv bin/libgcc_s.so.1 bin/libgcc_s.so.1.bak", "echo -n 730 > steam_appid.txt"],
    "ins": ["sed -i 's/\\r$//' srcds_run", "mkdir -p bin && cp -f %s bin/" % SC32, "export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/data/server:/data/server/bin"],
    "cmw": ["mkdir -p lib && cp -f %s lib/" % SC32, "echo 219640 > steam_appid.txt"],
    "pvr": ["mkdir -p /data/server/linux64", "cp -f /usr/lib/x86_64-linux-gnu/libc++.so.1 /data/server/linux64/libc++.so", "cp -f %s ." % SC64,
            "export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/data/server:/data/server/linux64"],
    "tu": ["cp -f %s ." % SC64],
    "unt": ["cp -f %s /data/server/" % SC64, "export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/data/server:/data/server/Unturned_Headless_Data/Plugins/x86_64"],
    "sm": ["export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/data/server:/data/server/linux64"],
    "hw": ["export LD_LIBRARY_PATH=$LD_LIBRARY_PATH:/data/server:/data/server/Hurtworld_Data/Plugins/x86_64"],
    "nmrih": ["for f in vphysics studiorender soundemittersystem shaderapiempty scenefilecache replay materialsystem; do [ -e bin/$f.so ] || ln -s ${f}_srv.so bin/$f.so; done"],
}
FIXES["btl"] = ["mv -f Battalion/Plugins/Sentry/Binaries/Linux/crashpad_handler Battalion/Plugins/Sentry/Binaries/Linux/crashpad_handler.off 2>/dev/null"]
EXTRA_PKGS = {"eco": ["libicu72"], "ck": ["xauth"], "mh": ["libcurl3-gnutls"], "pvr": ["libc++1", "libc++abi1"], "btl": [], "tf2c": ["libcurl3-gnutls:i386", "libcurl3-gnutls"]}
# eigene Bereit-Meldungen (sonst bei Unreal-Servern mit -log die Standardzeile)
READY = {"ns2": r"Finished loading 'maps/", "unt": r"Loading level: 100%", "av": r"Server startup complete", "nec": r"Started server using port"}
# Unreal-Server ohne Spielerabfrage bekommen -log, damit die Bereit-Meldung sichtbar wird
ADD_LOG = {"hcu", "squad"}

USER_VARS = {
    "servername": ("Servername", "text", None),
    "serverpassword": ("Server-Passwort", "secret", None),
    "adminpassword": ("Admin-Passwort", "secret", None),
    "rconpassword": ("RCON-Passwort", "secret", None),
    "maxplayers": ("Maximale Spieler", "number", None),
    "defaultmap": ("Start-Map", "slug", None),
    "gslt": ("Game Server Login Token", "secret", "Für öffentliche Source-Server: steamcommunity.com/dev/managegameservers"),
    "worldname": ("Weltname", "slug", None),
    "gamemode": ("Spielmodus", "slug", None),
    "tickrate": ("Tickrate", "number", None),
    "seed": ("Seed", "number", None),
    "worldsize": ("Weltgröße", "number", None),
}

FIXED = {
    "serverfiles": "/data/server", "HOME": "/data/home", "selfname": "panel", "ip": "0.0.0.0",
    "publicip": "", "logdir": "/data/logs", "gamelogdir": "/data/logs", "lgsmlogdir": "/data/logs",
    "consolelogdir": "/data/logs", "rootdir": "/data/server", "lgsmdir": "/data/lgsm",
    "datadir": "/data/lgsm", "tmpdir": "/tmp", "steamcmddir": "/opt/steamcmd",
}

ENGINE_MEM = {"unreal4": 6144, "unreal5": 8192, "unity3d": 4096, "source": 1024, "goldsrc": 512, "idtech2": 512, "idtech3": 512, "idtech3_ql": 1024,
              "quake": 512, "unreal": 512, "unreal2": 1024, "unreal3": 2048, "spark": 2048}

dep_rows = {}
with open(os.path.join(L, "data", "debian-12.csv")) as f:
    for row in csv.reader(f):
        if row:
            dep_rows[row[0]] = [p for p in row[1:] if p]

# bekannte Paket-Umbenennungen für ein schlankes Image
PKG_MAP = {"libcurl4-gnutls-dev:i386": "libcurl3-gnutls:i386", "libc6-dev": None, "libpulse-dev": "libpulse0",
           "libicu-dev": "libicu72", "rng-tools5": None, "openjdk-17-jre": "openjdk-17-jre-headless",
           "libstdc++5:i386": None, "mono-complete": None, "dotnet-runtime-10.0": None, "libmariadb-dev": "libmariadb3",
           "libncursesw5": "libncursesw5", "dos2unix": None, "telnet": None, "expect": None, "libxml2-utils": None}
BASE_PKGS = {"lib32gcc-s1", "lib32stdc++6", "libatomic1", "libpulse0", "libsdl2-2.0-0", "libcurl4"}

def parse_cfg(path):
    vals = {}
    for line in open(path, encoding="utf-8", errors="replace"):
        m = re.match(r'^([A-Za-z_][A-Za-z0-9_]*)="(.*)"\s*(#.*)?$', line.strip())
        if m:
            vals[m.group(1)] = m.group(2).replace('\\"', '"')
    return vals

def shell_fn(src, name):
    m = re.search(r'^' + re.escape(name) + r'\(\) \{\n(.*?)^\}', src, re.S | re.M)
    return m.group(1) if m else None

msgs = open(os.path.join(L, "modules", "info_messages.sh")).read()
games = open(os.path.join(L, "modules", "info_game.sh")).read()
sel = shell_fn(msgs, "fn_info_messages_select_engine") or ""

def ports_fn(short, engine):
    pairs = re.findall(r'\[ "\$\{(shortname|engine)\}" == "([^"]+)" \]; then\s+(fn_info_messages_[a-z0-9_]+)', sel)
    for kind, val, fn in pairs:
        if kind == "shortname" and val == short:
            return fn
    for kind, val, fn in pairs:
        if kind == "engine" and val == engine:
            return fn
    return None

def expand(s, env, depth=0):
    """bash-ähnliche ${...}-Ersetzung mit :+ und :-"""
    if depth > 8:
        return s
    def rep(m):
        inner = m.group(1)
        mm = re.match(r'^([A-Za-z_][A-Za-z0-9_]*)(:?[+-])(.*)$', inner, re.S)
        if mm:
            name, op, alt = mm.groups()
            v = expand(env.get(name, ""), env, depth + 1)
            if op.endswith("+"):
                return expand(alt, env, depth + 1) if v else ""
            return v if v else expand(alt.strip('"'), env, depth + 1)
        return expand(env.get(inner, ""), env, depth + 1)
    prev = None
    while prev != s:
        prev = s
        s = re.sub(r'\$\{([^{}]*)\}', rep, s)
    return s

def arith(expr, env):
    expr = expand(expr, env)
    expr = re.sub(r'\b([a-z]+)\b', lambda m: str(env.get(m.group(1), "0")), expr)
    if re.fullmatch(r'[0-9+\-* ()]+', expr):
        return str(eval(expr))
    return None

def derived_ports(short, env):
    body = shell_fn(games, "fn_info_game_" + short) or ""
    out = {}
    for line in body.splitlines():
        line = line.strip()
        m = re.match(r'^([a-z]+port)="\$\(\((.*)\)\)"$', line)
        if m:
            v = arith(m.group(2), {**env, **out})
            if v:
                out[m.group(1)] = v
            continue
        m = re.match(r'^([a-z]+port)="\$\{([a-z]+port)\}"$', line)
        if m and ({**env, **out}).get(m.group(2)):
            out[m.group(1)] = ({**env, **out})[m.group(2)]
            continue
        m = re.match(r'^([a-z]+port)="\$\{\1:-"?([0-9]+)"?\}"$', line)
        if m and not env.get(m.group(1)) and m.group(2) != "0":
            out[m.group(1)] = m.group(2)
    return out

def B(key, label):
    return {"key": key, "label": label, "type": "bool", "true": "1", "false": "0"}

def polish_arma3(t):
    # Arma 3 ships no server.cfg: create a minimal one, then use it
    init = ("[ -f server.cfg ] || printf 'hostname = \\\"Mein Arma-3-Server\\\";\\npassword = \\\"\\\";\\npasswordAdmin = \\\"\\\";\\n"
            "maxPlayers = 32;\\npersistent = 1;\\nverifySignatures = 2;\\nBattlEye = 1;\\n' > server.cfg; ")
    t["start"] = init + t["start"].replace("-mod= ", "-mod=\"$(echo '{{mods}}' | tr , ';')\" ").replace(" -autoinit", " -config=server.cfg -autoinit")
    t["vars"].append({"key": "mods", "label": "Mod-Ordner", "default": "", "help": "Ordner im Serververzeichnis, mit Komma getrennt, z. B. @CBA_A3,@ace. Mods lädst du über Dateien hoch."})
    t["config"] = [{"title": "Arma 3 (server.cfg)", "file": "server/server.cfg", "format": "armacfg", "settings": [
        {"key": "hostname", "label": "Servername"},
        {"key": "password", "label": "Passwort", "type": "secret"},
        {"key": "passwordAdmin", "label": "Admin-Passwort", "type": "secret"},
        {"key": "maxPlayers", "label": "Maximale Spieler", "type": "number", "min": 1, "max": 256},
        B("persistent", "Mission läuft ohne Spieler weiter"),
        {"key": "verifySignatures", "label": "Mod-Signaturen prüfen", "type": "select", "options": ["2", "0"], "labels": ["Ja (empfohlen)", "Nein"]},
        B("BattlEye", "BattlEye-Anticheat"), B("kickDuplicate", "Doppelte Spieler-IDs kicken"), B("disableVoN", "Sprachchat aus"),
    ]}]
    t["broadcast"] = ""

def polish_dayz(t):
    t["start"] = t["start"].replace(" -limitFPS", " -config=serverDZ.cfg -limitFPS")
    t["config"] = [{"title": "DayZ (serverDZ.cfg)", "file": "server/serverDZ.cfg", "format": "armacfg", "settings": [
        {"key": "hostname", "label": "Servername"},
        {"key": "password", "label": "Passwort", "type": "secret"},
        {"key": "passwordAdmin", "label": "Admin-Passwort", "type": "secret"},
        {"key": "maxPlayers", "label": "Maximale Spieler", "type": "number", "min": 1, "max": 127},
        {"key": "serverTimeAcceleration", "label": "Zeitraffer Tag (Faktor)", "type": "number", "min": 0, "max": 64},
        {"key": "serverNightTimeAcceleration", "label": "Zeitraffer Nacht (Faktor)", "type": "number", "min": 0.1, "max": 64},
        B("disable3rdPerson", "Nur Ego-Perspektive"), B("disableCrosshair", "Fadenkreuz aus"),
        B("BattlEye", "BattlEye-Anticheat"), B("enableWhitelist", "Nur Whitelist"),
    ]}]

POLISH = {"arma3": polish_arma3, "dayz": polish_dayz}

# Standard-Ports von Spielen, die ihre Ports nur in der eigenen Konfiguration
# führen (Quelle: Herstellerdokumentation / Standard-Konfigurationen).
MANUAL_PORTS = {
    "kf2": [("Spiel", "port", 7777, "udp"), ("Abfrage", "queryport", 27015, "udp"), ("Web-Admin", "httpport", 8080, "tcp"), ("Steam", "steamport", 20560, "udp")],
    "eco": [("Spiel", "port", 3000, "udp"), ("Web", "httpport", 3001, "tcp")],
    "av": [("Spiel", "port", 27000, "udp"), ("Spiel (TCP)", "porttcp", 27000, "tcp"), ("Abfrage", "queryport", 27003, "udp"), ("Steam", "steamport", 27020, "udp"), ("Steamworks", "steamworksport", 27021, "udp")],
    "nec": [("Spiel", "port", 14159, "udp")],
    "ct": [("Spiel", "port", 6587, "udp")],
    "rw": [("Spiel", "port", 4255, "udp"), ("Spiel (TCP)", "porttcp", 4255, "tcp"), ("Abfrage", "queryport", 4254, "udp")],
    "bt": [("Spiel", "port", 27015, "udp"), ("Abfrage", "queryport", 27016, "udp")],
    "st": [("Spiel", "port", 27016, "udp"), ("Abfrage", "queryport", 27015, "udp")],
    "sb": [("Spiel", "port", 21025, "tcp")],
}

made, skipped = [], []
os.makedirs(OUT, exist_ok=True)
for old in os.listdir(OUT):
    if old.startswith("lgsm-") and old.endswith(".json"):
        os.remove(os.path.join(OUT, old))
names = {}
with open(os.path.join(L, "data", "serverlist.csv")) as f:
    for row in list(csv.reader(f))[1:]:
        names[row[0]] = (row[1], row[2])

for short, (gsname, gamename) in sorted(names.items()):
    cfgp = os.path.join(L, "config-default", "config-lgsm", gsname, "_default.cfg")
    if short in SKIP or not os.path.exists(cfgp):
        continue
    cfg = parse_cfg(cfgp)
    appid = cfg.get("appid", "")
    if not appid.isdigit():
        skipped.append((short, "kein Steam"))
        continue
    needs_login = "steamuser" in cfg
    windows = cfg.get("steamcmdforcewindows") == "yes"
    if windows:
        skipped.append((short, "nur Windows"))
        continue
    env = dict(cfg)
    env.update(FIXED)
    engine = cfg.get("engine", "")
    env.update(derived_ports(short, env))

    fn = ports_fn(short, engine)
    body = shell_fn(msgs, fn) if fn else None
    if not body:
        skipped.append((short, "keine Port-Info"))
        continue
    plist = []
    for name, var, proto in re.findall(r'fn_port "([^"]+)" ([a-z]+) (tcp|udp)', body):
        v = env.get(var, "")
        if not v.isdigit() or v == "0":
            continue
        plist.append((name, var, int(v), proto))
    if short in MANUAL_PORTS:
        # the game keeps its ports in a config file it writes on first start:
        # use the defaults and allow one instance per machine
        plist = list(MANUAL_PORTS[short])
        for name, var, num, proto in plist:
            env[var] = str(num)
    if not plist:
        skipped.append((short, "Ports stehen nur in der Spielkonfiguration"))
        continue

    # Ports deduplizieren, Variablen auf Platzhalter abbilden
    ports, seen, var_index = [], {}, {}
    for name, var, num, proto in plist:
        key = (num, proto)
        if key not in seen:
            seen[key] = len(ports)
            ports.append({"name": name, "port": num, "proto": proto})
        var_index.setdefault(var, seen[key])
    # Port-Variablen ohne eigenen Eintrag (z. B. queryport == port) mitnehmen
    for var in re.findall(r'\$\{([a-z]+port)\}', cfg.get("startparameters", "")):
        if var not in var_index:
            v = env.get(var, "")
            for (num, proto), idx in seen.items():
                if str(num) == v:
                    var_index[var] = idx
                    break
            else:
                if v.isdigit() and v != "0":
                    seen[(int(v), "udp")] = len(ports)
                    var_index[var] = len(ports)
                    ports.append({"name": var.replace("port", "").capitalize() or "Port", "port": int(v), "proto": "udp"})

    if "port" not in var_index:
        skipped.append((short, "Spielport steht nur in der Spielkonfiguration"))
        continue
    tvars, placeholders = [], {}
    for i, (var, idx) in enumerate(var_index.items()):
        placeholders[var] = "{{port}}" if idx == 0 else "{{port_%d}}" % (idx + 1)
    params = cfg.get("startparameters", "")
    used = set(re.findall(r'\$\{([A-Za-z_][A-Za-z0-9_]*)', params))
    penv = dict(env)
    for v in used:
        if v in placeholders:
            penv[v] = placeholders[v]
        elif v in USER_VARS:
            label, typ, help_ = USER_VARS[v]
            default = expand(cfg.get(v, ""), env)
            if default in ("LinuxGSM", ""):
                default = {"servername": "Mein Server"}.get(v, default)
            entry = {"key": v, "label": label, "default": default, "type": typ}
            if help_:
                entry["help"] = help_
            if typ == "secret":
                entry["default"] = ""
            tvars.append(entry)
            penv[v] = "{{%s}}" % v
    start_args = expand(params, penv)
    # Verweise auf Konfigurationsdateien, die LinuxGSM selbst anlegen würde, entfernen
    toks, keep = start_args.split(), []
    for t in toks:
        if re.search(r'/data/(server|lgsm)/\S*panel[.\w-]*\.(cfg|ini|xml|json|txt)', t):
            if keep and re.fullmatch(r'[-+][A-Za-z_]+', keep[-1]):
                keep.pop()  # zugehörigen Schalter (-config) ebenfalls entfernen
            continue
        keep.append(t)
    start_args = " ".join(keep)
    start_args = re.sub(r'\s+', ' ', start_args).strip()
    if re.search(r'\$\{|\$\(|`', start_args):
        skipped.append((short, "Startparameter nicht auflösbar"))
        continue
    execdir = expand(cfg.get("systemdir" if short == "av" else "executabledir", "${serverfiles}"), env)
    if not execdir.startswith("/data/server"):
        skipped.append((short, "unbekanntes Verzeichnis"))
        continue
    rel = execdir[len("/data/server"):].lstrip("/") or "."
    exe = cfg.get("executable", "")
    if not exe:
        skipped.append((short, "kein Programm"))
        continue
    pre = expand(cfg.get("preexecutable", ""), {**env, "javaram": "2048"})
    if short in ADD_LOG and not re.search(r'(^|\s)-log(\s|$)', start_args):
        start_args += " -log"
    if re.search(r'(srcds_run|hlds_run|srcds\.sh|srcds_run\.sh)$', exe) and "-norestart" not in start_args:
        start_args += " -norestart"  # Absturz soll Docker sehen, nicht srcds_run selbst neu starten
    fixes = "; ".join(FIXES.get(short, []))
    start = (f"cd {rel} && " if rel != "." else "") + (fixes + "; " if fixes else "") + f"{pre} {exe} {start_args}".strip()
    # leere Platzhalter (z. B. +sv_setsteamaccount ohne Token) stören die meisten Server nicht

    pkgs = list(EXTRA_PKGS.get(short, []))
    for p in dep_rows.get(short, []):
        p = PKG_MAP.get(p, p)
        if p and p not in BASE_PKGS:
            pkgs.append(p)

    mem = ENGINE_MEM.get(engine, 4096)
    tpl = {
        "id": "lgsm-" + short,
        "name": gamename,
        "category": "Steam (Community)",
        "kind": "steam",
        "steam_app_id": int(appid),
    }
    if cfg.get("branch"):
        tpl["steam_beta"] = cfg["branch"]
    if needs_login:
        tpl["steam_login"] = True
    if cfg.get("appidmod") and appid == "90" and cfg["appidmod"] != "cstrike":
        tpl["steam_mod"] = cfg["appidmod"]
    if cfg.get("baseappid"):
        sd = expand(cfg.get("supportdir", ""), env)
        if not sd.startswith("/data/server/"):
            skipped.append((short, "Basis-App ohne Ordner"))
            continue
        tpl["steam_base_app"] = int(cfg["baseappid"])
        tpl["steam_base_dir"] = sd[len("/data/server/"):]
    if short == "l4d2":
        tpl["steam_preplatform"] = "windows"  # Linux-Depot lässt sich erst nach einem Windows-Durchlauf laden
    tpl["start"] = start
    tpl["ports"] = ports
    tpl["vars"] = tvars
    tpl["memory_mb"] = mem
    tpl["min_memory_mb"] = min(512, mem)
    tpl["console"] = "stdin"
    if cfg.get("querytype") == "protocol-valve" and "queryport" in var_index:
        tpl["query"] = {"type": "a2s", "port": var_index["queryport"]}
    elif cfg.get("querytype") == "protocol-valve" and engine in ("source", "goldsrc"):
        tpl["query"] = {"type": "a2s", "port": var_index.get("port", 0)}
    if short in READY:
        tpl["ready"] = READY[short]
    elif engine in ("unreal4", "unreal5") and re.search(r'(^|\s)-log(\s|$)', start):
        tpl["ready"] = "Bringing up level for play took"
    if engine in ("source", "goldsrc"):
        tpl["broadcast"] = "say {{msg}}"
        gm = re.search(r'-game\s+"?(?:/data/server/)?([A-Za-z0-9_\-]+)"?', start)
        cfgname = "panel.cfg" if "+servercfgfile panel.cfg" in start else "server.cfg"
        if gm:
            sub = gm.group(1) + ("/" if engine == "goldsrc" else "/cfg/")  # GoldSrc: config in the mod folder
            tpl["config"] = [{
                "title": "Server (%s%s)" % (sub, cfgname),
                "file": "server/%s%s" % (sub, cfgname),
                "format": "cvar",
                "settings": [
                    {"key": "hostname", "label": "Servername"},
                    {"key": "sv_password", "label": "Passwort", "type": "secret"},
                    {"key": "rcon_password", "label": "RCON-Passwort", "type": "secret"},
                    {"key": "mp_timelimit", "label": "Zeitlimit pro Map (Minuten)", "type": "number", "min": 0, "max": 180},
                    {"key": "mp_friendlyfire", "label": "Friendly Fire", "type": "bool", "true": "1", "false": "0"},
                    {"key": "sv_alltalk", "label": "Sprachchat für alle", "type": "bool", "true": "1", "false": "0"},
                ],
            }]
    if short == "ark":
        tpl["config"] = [{
            "title": "ARK (GameUserSettings.ini)",
            "file": "server/ShooterGame/Saved/Config/LinuxServer/GameUserSettings.ini",
            "format": "ini",
            "note": "Die Datei entsteht beim ersten Start. ARK schreibt sie beim Beenden neu, deshalb stoppt das Panel den Server vor dem Speichern.",
            "settings": [
                {"key": "SessionSettings/SessionName", "label": "Servername"},
                {"key": "ServerSettings/ServerPassword", "label": "Passwort", "type": "secret"},
                {"key": "ServerSettings/ServerAdminPassword", "label": "Admin-Passwort", "type": "secret"},
                {"key": "/Script/Engine.GameSession/MaxPlayers", "label": "Maximale Spieler", "type": "number", "min": 1, "max": 200},
                {"key": "ServerSettings/ServerPVE", "label": "PvE (kein PvP)", "type": "bool", "true": "True", "false": "False"},
                {"key": "ServerSettings/DifficultyOffset", "label": "Schwierigkeit (0 bis 1)", "type": "number", "min": 0, "max": 1},
                {"key": "ServerSettings/XPMultiplier", "label": "Erfahrung (Faktor)", "type": "number", "min": 0.1, "max": 100},
                {"key": "ServerSettings/TamingSpeedMultiplier", "label": "Zähmen (Faktor)", "type": "number", "min": 0.1, "max": 100},
                {"key": "ServerSettings/HarvestAmountMultiplier", "label": "Ernte (Faktor)", "type": "number", "min": 0.1, "max": 100},
                {"key": "ServerSettings/ShowMapPlayerLocation", "label": "Eigene Position auf der Karte", "type": "bool", "true": "True", "false": "False"},
            ],
        }]
    if pkgs:
        tpl["packages"] = sorted(set(pkgs))
    tpl["status"] = "beta" if short in PASSED else "experimentell"
    tpl["notes"] = "Automatisch aus LinuxGSM übernommen." + (" Selbsttest bestanden." if short in PASSED else "")
    if needs_login:
        tpl["notes"] += " Braucht ein Steam-Konto (Einstellungen → Steam-Konto); bei manchen Spielen muss das Konto das Spiel besitzen."
    if short in BROKEN:
        tpl["status"] = "defekt"
        tpl["notes"] += " " + BROKEN[short] + "."
    if short in MANUAL_PORTS:
        tpl["fixed_ports"] = True
        tpl["notes"] += " Nutzt die Standard-Ports des Spiels, daher nur ein Server dieses Spiels pro Rechner."
    if short in POLISH:
        POLISH[short](tpl)
    with open(os.path.join(OUT, tpl["id"] + ".json"), "w") as f:
        json.dump(tpl, f, ensure_ascii=False, indent=2)
        f.write("\n")
    made.append(short)

print(f"{len(made)} Vorlagen erzeugt")
for s, why in skipped:
    print(f"  übersprungen {s}: {why}")
