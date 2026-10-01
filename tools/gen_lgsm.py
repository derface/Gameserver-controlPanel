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
        "armar"}              # braucht eine eigene JSON-Konfiguration

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

made, skipped = [], []
os.makedirs(OUT, exist_ok=True)
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
    if "steamuser" in cfg:
        skipped.append((short, "braucht Steam-Login"))
        continue
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
    start = (f"cd {rel} && " if rel != "." else "") + f"{pre} {exe} {start_args}".strip()
    # leere Platzhalter (z. B. +sv_setsteamaccount ohne Token) stören die meisten Server nicht

    pkgs = []
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
    if pkgs:
        tpl["packages"] = sorted(set(pkgs))
    tpl["status"] = "beta"
    tpl["notes"] = "Automatisch aus LinuxGSM übernommen und noch nicht im Panel getestet."
    with open(os.path.join(OUT, tpl["id"] + ".json"), "w") as f:
        json.dump(tpl, f, ensure_ascii=False, indent=2)
        f.write("\n")
    made.append(short)

print(f"{len(made)} Vorlagen erzeugt")
for s, why in skipped:
    print(f"  übersprungen {s}: {why}")
