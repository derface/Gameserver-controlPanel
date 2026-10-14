#!/usr/bin/env python3
"""Erzeugt Spiel-Vorlagen aus CubeCoders' AMPTemplates (MIT-Lizenz,
https://github.com/CubeCoders/AMPTemplates).

    git clone --depth 1 https://github.com/CubeCoders/AMPTemplates /tmp/ampt
    python3 tools/gen_amp.py /tmp/ampt internal/templates/games internal/templates/games/amp

Übernommen werden Spiele, die über SteamCMD installiert werden und unter Linux
laufen (nativ oder über Wine), sofern das Panel sie noch nicht hat (gleiche
Steam-App-ID). Aus AMPs Startzeile, Ports, Einstellungen und Config-Vorlagen
wird eine Panel-Vorlage. Alle erzeugten Vorlagen sind "experimentell", bis
der Selbsttest sie bestätigt. Was übersprungen wird und warum, steht in
amp/_bericht.md.
"""
import glob
import json
import os
import re
import sys

SRC, GAMES, OUT = sys.argv[1], sys.argv[2], sys.argv[3]

# Sonderfälle: eigene Installer, mehrere Programme, Spiele ohne Server-Download
SKIP = {
    "call-of-dutybo1": "eigener Plutonium-Installer",
    "call-of-dutybo2": "eigener Plutonium-Installer",
    "call-of-dutymw3-plutonium": "eigener Plutonium-Installer",
    "call-of-dutywaw": "eigener Plutonium-Installer",
    "call-of-dutymw2": "eigener IW4x-Installer",
    "ark-sa-min": "Variante von ARK: Survival Ascended",
    "euro-truck-simulator-2": "Server braucht eine im Spiel exportierte Paketdatei",
    "american-truck-simulator": "Server braucht eine im Spiel exportierte Paketdatei",
}

# Übernahme aus AMP: Werte, die ein übernommener Server bekommt
AMP_IMPORT = {
    "icarus": {"vars": {"startup_mode": "-ResumeProspect"}},
}

# Spiele, die ohne ihre INI beim Start interaktiv nachfragen: AMP schreibt
# die Datei aus den Einstellungen, das Panel legt sie vor dem ersten Start an
INI_FROM_SETTINGS = {"archean"}

# Testergebnisse späterer Selbsttests: Vorlage bleibt drin, Status passt sich an
PASSED = set()
BROKEN = {}


def load(p):
    try:
        with open(p, encoding="utf-8-sig") as f:
            return json.load(f)
    except Exception:
        return None


def kvp(p):
    d = {}
    for line in open(p, encoding="utf-8-sig", errors="replace"):
        if "=" in line and not line.startswith("#"):
            k, v = line.rstrip("\n").split("=", 1)
            d[k.strip()] = v.strip()
    return d


def snake(name):
    s = re.sub(r"[^A-Za-z0-9]+", "_", name)
    s = re.sub(r"([a-z0-9])([A-Z])", r"\1_\2", s)
    s = re.sub(r"([A-Z]+)([A-Z][a-z])", r"\1_\2", s)
    return re.sub(r"_+", "_", s).strip("_").lower() or "wert"


def num(v):
    try:
        f = float(v)
        return int(f) if f.is_integer() else f
    except (TypeError, ValueError):
        return None


PORT_NAMES = {"game port": "Spiel", "query port": "Abfrage", "steam query port": "Abfrage", "server port": "Spiel",
              "rcon port": "RCON", "steam port": "Steam", "beacon port": "Beacon", "sourcetv port": "SourceTV",
              "client port": "Client", "web admin port": "Web-Admin", "telnet port": "Telnet", "raw udp port": "Spiel (roh)"}

# German texts (shared with the settings view): internal/templates/amp/de.json
DE = load(os.path.join(os.path.dirname(os.path.abspath(__file__)), "..", "internal", "templates", "amp", "de.json")) or {}


def de(s):
    s = str(s)
    return DE.get(s) or DE.get(s.strip()) or s


have_apps, have_ids, have_names = set(), set(), set()
for f in glob.glob(os.path.join(GAMES, "**", "*.json"), recursive=True):
    if os.sep + "amp" + os.sep in f:
        continue
    t = load(f)
    if isinstance(t, dict):
        have_ids.add(t.get("id"))
        have_names.add(re.sub(r"[^a-z0-9]", "", str(t.get("name", "")).lower()))
        if t.get("steam_app_id"):
            have_apps.add(t["steam_app_id"])


class Skip(Exception):
    pass


def steam_stage(stages, settings):
    steam = [u for u in stages if isinstance(u, dict) and u.get("UpdateSource") == "SteamCMD"
             and u.get("UpdateSourcePlatform") in ("All", "Linux", None)]
    if not steam:
        raise Skip("kein SteamCMD-Download")
    for u in steam:
        cond = u.get("UpdateSourceConditionSetting")
        if not cond:
            return u
        s = settings.get(cond)
        default = str((s or {}).get("DefaultValue", ""))
        if default.lower() == str(u.get("UpdateSourceConditionValue", "")).lower():
            return u
    return steam[0]


def shell_safe(cmd):
    """AMP starts programs without a shell; the panel runs them with bash.
    Quote words with shell syntax characters and close a quote AMP forgot."""
    out, i, n = [], 0, len(cmd)
    while i < n:
        if cmd[i] == " ":
            out.append(" ")
            i += 1
            continue
        j, q, word = i, None, ""
        while j < n and (q or cmd[j] != " "):
            c = cmd[j]
            if q:
                if c == q:
                    q = None
            elif c in "\"'":
                q = c
            word += c
            j += 1
        if q:  # unbalanced quote, e.g. Desynced's -GameSettings="{...}
            word += q
        bare = re.sub(r"\"[^\"]*\"|'[^']*'|\{\{[a-z0-9_]+\}\}", "", word)
        if re.search(r"[()<>|&;]", bare) and '"' not in word:
            word = '"%s"' % word
        out.append(word)
        i = j
    return "".join(out)


def bash_ok(line):
    import subprocess
    r = subprocess.run(["bash", "-n", "-c", line], capture_output=True, text=True)
    return r.returncode == 0, r.stderr.strip()


def build(base):
    if base in SKIP:
        raise Skip(SKIP[base])
    m = kvp(os.path.join(SRC, base + ".kvp"))
    if "Linux" not in m.get("Meta.OS", ""):
        raise Skip("läuft laut AMP nicht unter Linux")
    cfg = load(os.path.join(SRC, base + "config.json")) or []
    cfg = [x for x in cfg if isinstance(x, dict)]
    settings = {x.get("FieldName"): x for x in cfg if x.get("FieldName")}
    stages = load(os.path.join(SRC, base + "updates.json")) or []
    stages = [u for u in stages if isinstance(u, dict)]
    st = steam_stage(stages, settings)
    app = num(re.sub(r"\D", "", str(st.get("UpdateSourceData", ""))))
    if not app:
        raise Skip("keine Steam-App-ID")
    if app in have_apps:
        raise Skip("gibt es schon im Panel")
    client_app = num(re.sub(r"\D", "", str(st.get("UpdateSourceArgs", "")))) or None
    beta = str(st.get("UpdateSourceVersion") or "").strip()
    if "{{" in beta:
        beta = ""
    windows_dl = st.get("ForceDownloadPlatform") == "Windows"

    exe_l, args_l = m.get("App.ExecutableLinux", ""), m.get("App.LinuxCommandLineArgs", "")
    low = (exe_l + " " + args_l).lower()
    proton = "proton" in low
    wine = "wine" in low or proton
    if proton and not windows_dl:
        windows_dl = True
    if wine:
        windows_dl = True

    # working directory relative to the install folder (= AMP's BaseDirectory)
    root_base = m.get("App.BaseDirectory", "").strip("./").split("/")
    base_dir = root_base[-1] if root_base else ""
    wd = m.get("App.WorkingDir", "").replace("\\", "/").strip("/")
    sub = wd[len(base_dir):].strip("/") if base_dir and wd.startswith(base_dir) else ("" if wd == base_dir else wd)
    if "{{" in sub:
        raise Skip("Arbeitsordner hängt von Einstellungen ab")

    # ports
    raw_ports = load(os.path.join(SRC, base + "ports.json")) or []
    ports, refidx, twins = [], {}, {}
    for p in raw_ports if isinstance(raw_ports, list) else []:
        if not isinstance(p, dict) or not p.get("Port"):
            continue
        name = PORT_NAMES.get(str(p.get("Name", "")).lower()) or de(p.get("Name", "Port"))
        protos = ["udp", "tcp"] if str(p.get("Protocol", "")).lower() == "both" else [str(p.get("Protocol", "udp")).lower()]
        for pr in protos:
            if pr not in ("udp", "tcp"):
                continue
            if p.get("Ref") and p["Ref"] not in refidx:
                refidx[p["Ref"]] = len(ports)
            elif p.get("Ref"):
                twins.setdefault(refidx[p["Ref"]], []).append(len(ports))
            ports.append({"name": name, "port": int(p["Port"]), "proto": pr})
    if not ports:
        raise Skip("keine Ports bekannt")
    # AMP's generic port names
    for i, p in enumerate(raw_ports if isinstance(raw_ports, list) else []):
        if isinstance(p, dict) and p.get("Ref"):
            refidx.setdefault("ApplicationPort%d" % (i + 1), refidx[p["Ref"]])
    used_ports = set()

    vars_, varkeys, spaced = [], {}, set()
    max_users = num(m.get("App.MaxUsers")) or 16

    def port_ph(ref):
        i = refidx.get(ref)
        if i is None:
            return None
        used_ports.add(i)
        used_ports.update(twins.get(i, []))
        return "{{port}}" if i == 0 else "{{port_%d}}" % (i + 1)

    def var_for(field):
        """Panel variable for an AMP setting used in the command line."""
        s = settings.get(field)
        key = snake(field)
        if key in varkeys:
            return varkeys[key]
        if s is None:
            return None
        t = s.get("InputType")
        ev = s.get("EnumValues") or {}
        default = str(s.get("DefaultValue", "") if s.get("DefaultValue") is not None else "")
        if "{{" in default:
            # choices may refer to other settings (Sapiens: --new "{{WorldName}}");
            # the panel expands placeholders in template-defined choices
            if t == "enum":
                before = set(unknown)
                default = convert(default, True)
                if unknown - before:
                    unknown.intersection_update(before)
                    default = ""
            else:
                default = ""
        if re.search(r"\bAMP\b|CubeCoders", default):
            default = "Mein Server" if re.search(r"name|host|title", key) else ""
        if not default and re.fullmatch(r"(server_|session_|host)?name|hostname|server_title", key):
            default = "Mein Server"
        v = {"key": key, "label": de(s.get("DisplayName") or field)}
        desc = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", str(s.get("Description") or "")).strip()
        if desc:
            v["help"] = de(desc)
        if t == "checkbox":
            tv, fv = convert(str(ev.get("True", "true")), True), convert(str(ev.get("False", "false")), True)
            dflt = default.lower() in ("true", tv.lower()) and default != ""
            if fv == "":
                v.update({"type": "bool", "flag": tv, "default": "true" if dflt else "false"})
            else:
                if tv != tv.strip() or fv != fv.strip():
                    spaced.add(key)
                tv, fv = tv.strip(), fv.strip()
                v.update({"type": "select", "options": [tv, fv], "labels": ["An", "Aus"], "default": tv if dflt else fv})
        elif t == "enum" and isinstance(ev, dict) and ev:
            raw, nev = [], {}
            for k in (str(x) for x in ev):
                c = k
                if "{{" in k:
                    before = set(unknown)
                    c = convert(k, True)
                    if unknown - before:  # e.g. {{$ExternalIP}}: leave this choice out
                        unknown.intersection_update(before)
                        continue
                raw.append(c)
                nev[c] = ev[k]
            ev = nev
            if any(o != o.strip() for o in raw):
                spaced.add(key)
            opts, labels = [], []
            for o in raw:
                if o.strip() not in opts:
                    opts.append(o.strip())
                    labels.append(de(ev[o]) if str(ev[o]).strip() else o.strip())
            if not opts:
                v.update({"default": default})
            else:
                v.update({"type": "select", "options": opts, "default": default if default in opts else opts[0]})
                if labels != opts:
                    v["labels"] = labels
        elif t == "number" and re.fullmatch(r"-?\d*", default):
            v.update({"type": "number", "default": default})
        elif t in ("password", "Password"):
            v.update({"type": "secret", "default": default})
        elif t in ("hidden",):
            return None
        else:
            if any(c in default for c in "\"'`$\\;|&<>"):
                default = ""
            v.update({"default": default})
        vars_.append(v)
        varkeys[key] = "{{%s}}" % key
        return varkeys[key]

    unknown = set()

    def convert(s, in_file=False):
        def rep(mt):
            name = mt.group(1)
            if name.startswith("$"):
                n = name[1:]
                if n in ("FullBaseDir", "FullInstanceDir"):
                    return "/data/server/"
                if n in ("FullRootDir",):
                    return "/data/"
                if n == "ApplicationIPBinding":
                    return "0.0.0.0"
                if n == "MaxUsers":
                    return var_mx()
                if n == "RemoteAdminPassword":
                    return var_rcon()
                if n in ("PlatformArgs", "FormattedArgs"):
                    return mt.group(0)
                ph = port_ph(n)
                if ph:
                    return ph
                unknown.add(name)
                return ""
            v = var_for(name)
            if v is not None:
                return v
            s2 = settings.get(name)
            if s2 is not None:
                d = str(s2.get("DefaultValue") or "")
                return "" if "{{" in d else d
            if in_file:
                return ""
            unknown.add(name)
            return ""
        return re.sub(r"\{\{\s*(\$?[A-Za-z0-9_.]+)\s*\}\}", rep, s)

    def var_mx():
        if "max_players" not in varkeys:
            vars_.insert(0, {"key": "max_players", "label": "Maximale Spieler", "type": "number", "default": str(int(max_users))})
            varkeys["max_players"] = "{{max_players}}"
        return "{{max_players}}"

    def var_rcon():
        if "rcon_password" not in varkeys:
            vars_.append({"key": "rcon_password", "label": "RCON-Passwort", "type": "secret", "default": "",
                          "help": "Passwort für die Fernverwaltung. Der RCON-Port wird nie ins Internet freigegeben."})
            varkeys["rcon_password"] = "{{rcon_password}}"
        return "{{rcon_password}}"

    # command line
    cmd = m.get("App.CommandLineArgs", "")
    if not cmd and not args_l:
        raise Skip("keine Startzeile")
    fmt = m.get("App.CommandLineParameterFormat", "-{0}={1}")
    formatted = []
    if "{{$FormattedArgs}}" in cmd:
        for x in cfg:
            if x.get("IncludeInCommandLine") is not True or x.get("Hidden") or str(x.get("FieldName", "")).startswith("$"):
                continue
            pf = str(x.get("ParamFieldName") or "")
            if not pf or "{{" in pf or "{{%s}}" % x.get("FieldName") in cmd:
                continue
            v = var_for(x.get("FieldName"))
            if v is None:
                continue
            if x.get("InputType") == "checkbox":
                var = next(vv for vv in vars_ if "{{%s}}" % vv["key"] == v)
                if var.get("type") == "bool":
                    var["flag"] = fmt.replace("{0}", pf).replace("{1}", var["flag"])
                    formatted.append(v)
                else:
                    formatted.append(fmt.replace("{0}", pf).replace("{1}", v))
            elif x.get("SkipIfEmpty"):
                var = next(vv for vv in vars_ if "{{%s}}" % vv["key"] == v)
                if var.get("type") == "select" or var.get("flag"):
                    formatted.append(fmt.replace("{0}", pf).replace("{1}", v))
                else:
                    var["if_set"] = fmt.replace("{0}", pf).replace("{1}", "%s")
                    formatted.append(v)
            else:
                formatted.append(fmt.replace("{0}", pf).replace("{1}", v))
    platform = args_l
    exe = exe_l
    if wine:
        exe_win = m.get("App.ExecutableWin", "").replace("\\", "/")
        args_l = args_l.replace("{{$FullBaseDir}}", "").replace("{{$FullInstanceDir}}", "")
        mt = re.search(r'"?([^"\s]+\.exe)"?', args_l)
        if mt:
            target = mt.group(1)
            platform = args_l[mt.end():]
        elif exe_win.lower().endswith(".exe"):
            target = exe_win
            platform = ""
        else:
            raise Skip("Windows-Programm nicht gefunden")
        target = convert(target.replace("\\", "/"))
        if sub and target.lstrip("./").startswith(sub + "/"):
            target = target.lstrip("./")[len(sub) + 1:]
        if target.startswith(wd + "/"):
            target = target[len(wd) + 1:]
        elif target.startswith(base_dir + "/"):
            target = target[len(base_dir) + 1:]
            if sub:
                target = os.path.relpath(target, sub)
        if not target.startswith(("/", "./", "../")):
            target = "./" + target
        exe = 'wine "%s"' % target
    else:
        if not exe:
            raise Skip("kein Linux-Programm")
        if not exe.startswith("/"):
            e = exe.replace("\\", "/")
            if e.startswith(wd + "/"):
                e = e[len(wd) + 1:]
            elif e.startswith(base_dir + "/"):
                e = e[len(base_dir) + 1:]
                if sub:
                    e = os.path.relpath(e, sub)
            exe = "./" + e if not e.startswith(("./", "../")) else e
        exe = convert(exe)
        if exe in ("/usr/bin/xvfb-run",):
            raise Skip("grafischer Start ohne Wine")
    cmd = cmd.replace("{{$PlatformArgs}}", platform.strip()).replace("{{$FormattedArgs}}", " ".join(formatted))
    cmd = convert(cmd)
    if unknown:
        raise Skip("unbekannte Platzhalter: " + ", ".join(sorted(unknown)))
    for k in spaced:  # values like "+map_rotate " carried their own space
        cmd = cmd.replace("{{%s}}" % k, "{{%s}} " % k)
    cmd = re.sub(r"\s+", " ", cmd).strip()
    cmd = shell_safe(cmd)

    # environment (Wine is handled by the panel)
    env = {}
    try:
        env = json.loads(m.get("App.EnvironmentVariables") or "{}")
    except ValueError:
        pass
    envs = []
    for k, v in env.items():
        if k.upper().startswith(("WINE", "STEAM_COMPAT", "PROTON")) or k.upper() in ("HOME", "DISPLAY") or not re.match(r"^[A-Za-z_][A-Za-z0-9_]*$", k):
            continue
        if wine and k == "LD_LIBRARY_PATH":
            continue
        v = convert(str(v)).replace("%" + k + "%", "$" + k).replace("%LD_LIBRARY_PATH%", "$LD_LIBRARY_PATH")
        if "%" in v or "{{" in v:
            continue
        envs.append('%s="%s"' % (k, v.replace('"', "")))
    if client_app and not any(e.startswith("SteamAppId=") for e in envs):
        envs.append('SteamAppId="%d"' % client_app)

    # steps after the download, done before every start (idempotent)
    pre, configs = [], []
    repo_files = {os.path.basename(f).lower(): f for f in glob.glob(os.path.join(SRC, "*"))}
    meta = load(os.path.join(SRC, base + "metaconfig.json")) or []
    meta = [x for x in meta if isinstance(x, dict)]

    def rel(path):
        p = convert(str(path), True).replace("\\", "/")
        if p.startswith("/data/server/"):
            return p[len("/data/server/"):]
        if p.startswith("/data/"):
            return None
        if p.startswith("./"):
            p = p[2:]
        rb = m.get("App.RootDir", "").strip("./")
        for pre_ in (rb + "/" + base_dir + "/", base_dir + "/"):
            if pre_.strip("/") and p.startswith(pre_):
                return p[len(pre_):]
        return p

    for u in stages:
        if u is st or u.get("UpdateSource") == "SteamCMD":
            continue
        if u.get("UpdateSourcePlatform") not in ("All", "Linux", None):
            continue
        if u.get("UpdateSourceConditionSetting"):
            continue  # optional extras (mods, betas): left out
        src, optional = u.get("UpdateSource"), bool(u.get("SkipOnFailure"))
        name = str(u.get("UpdateStageName", ""))
        if src in ("Pause", "WaitForStartupComplete", "StartApplication", "ShutdownApplication"):
            continue
        args_txt = str(u.get("UpdateSourceArgs", ""))
        if src == "Executable" and ("wine" in name.lower() or "wineboot" in args_txt or "proton" in (name + args_txt).lower()):
            continue  # the panel brings its own Wine
        if src == "Executable" and re.fullmatch(r'-c "mkdir -p [^;&|]*"', args_txt.strip()):
            continue  # folders in AMP's Wine prefix; ours is set up by the panel
        if src == "CreateDirectory":
            r = rel(u.get("UpdateSourceArgs", ""))
            if r and "{{" not in r:
                pre.append('mkdir -p "%s"' % r)
            continue
        if src == "CreateFile":
            r = rel(u.get("UpdateSourceArgs", ""))
            data = str(u.get("UpdateSourceData", ""))
            if r and "\n" not in data and '"' not in data and "{{" not in data:
                pre.append('[ -f "%s" ] || { mkdir -p "$(dirname "%s")"; printf %%s "%s" > "%s"; }' % (r, r, data, r))
                continue
            if optional:
                continue
            raise Skip("eigene Datei: " + name)
        if src == "CopyFilePath":
            a, b = rel(u.get("UpdateSourceArgs", "")), rel(u.get("UpdateSourceTarget", "") or u.get("UpdateSourceData", ""))
            a = rel(u.get("UpdateSourceData", "")) if u.get("UpdateSourceTarget") else a
            if a and b and "{{" not in a + b:
                pre.append('[ -e "%s" ] || { mkdir -p "$(dirname "%s")"; cp -rn "%s" "%s" 2>/dev/null; }' % (b, b, a, b))
                continue
            if optional:
                continue
            raise Skip("Kopierschritt: " + name)
        if src == "SetExecutableFlag":
            r = rel(u.get("UpdateSourceArgs", ""))
            if r:
                pre.append('chmod +x "%s" 2>/dev/null' % r)
            continue
        if src == "FetchURL" and "/AMPTemplates/" in str(u.get("UpdateSourceData", "")) and not u.get("UnzipUpdateSource"):
            fname = os.path.basename(str(u.get("UpdateSourceData")))
            content_path = repo_files.get(fname.lower())
            target_name = str(u.get("UpdateSourceArgs", "")) or fname
            target_dir = str(u.get("UpdateSourceTarget", ""))
            final = None
            for mc in meta:
                tmpl = str(mc.get("ConfigFileTemplate", ""))
                if tmpl and os.path.basename(tmpl).lower() == target_name.lower():
                    final = str(mc.get("ConfigFile", ""))
            if final is None:
                final = rel(target_dir.rstrip("/") + "/" + target_name) if target_dir else target_name
            else:
                final = rel(final)
            if not content_path or not final or "{{" in final or target_name.startswith("AMP_") and final.endswith(target_name):
                if optional:
                    continue
                raise Skip("Config-Vorlage fehlt: " + fname)
            content = open(content_path, encoding="utf-8-sig", errors="replace").read()
            if fname.lower().endswith((".sh", ".ps1", ".bat")):
                if optional:
                    continue
                raise Skip("Skript: " + fname)
            content = convert(content, True)
            ext = final.lower().rsplit(".", 1)[-1]
            fmtname = {"json": "json", "ini": "ini", "cfg": "cvar", "properties": "properties", "txt": "properties"}.get(ext, "")
            if fmtname == "ini" and not re.search(r"(?m)^\s*\[", content):
                fmtname = "properties"
            if fmtname == "cvar" and len(re.findall(r"(?m)^\s*\w+\s*=.*;\s*$", content)) >= 3:
                fmtname = "armacfg"
            c = {"title": os.path.basename(final), "file": "server/" + final, "format": fmtname or "properties",
                 "write_default": True, "default": content, "settings": []}
            if not fmtname:
                c["auto"] = False
            if fmtname == "json":
                try:
                    json.loads(re.sub(r"\{\{[a-z0-9_]+\}\}", "0", content))
                except ValueError:
                    c["format"], c["auto"] = "properties", False
            configs.append(c)
            continue
        if optional:
            continue
        raise Skip("Zusatzschritt %s: %s" % (src, name))

    if base in INI_FROM_SETTINGS:
        for mc in meta:
            if str(mc.get("ConfigType", "")).lower() != "ini" or not mc.get("Subsections"):
                continue
            final = rel(str(mc.get("ConfigFile", "")))
            if not final or "{{" in final or any(c["file"] == "server/" + final for c in configs):
                continue
            lines = []
            for sub_ in mc["Subsections"]:
                lines.append("[%s]" % sub_.get("Heading", ""))
                for k, f in (sub_.get("SettingMappings") or {}).items():
                    if str(f).startswith("$"):
                        val = convert("{{%s}}" % f, True)
                    else:  # the file itself is the setting; no extra start value
                        d = (settings.get(f) or {}).get("DefaultValue")
                        val = "" if d is None or "{{" in str(d) else str(d)
                        if re.search(r"\bAMP\b|CubeCoders", val):
                            val = "Mein Server"
                    lines.append("%s=%s" % (k, val))
                lines.append("")
            configs.append({"title": os.path.basename(final), "file": "server/" + final, "format": "ini",
                            "write_default": True, "default": "\n".join(lines), "settings": []})
    unused = [i for i in range(len(ports)) if i not in used_ports]
    for c in configs:
        for i, _ in enumerate(ports):
            if ("{{port}}" if i == 0 else "{{port_%d}}" % (i + 1)) in c["default"]:
                used_ports.add(i)
                used_ports.update(twins.get(i, []))
    unused = [i for i in range(len(ports)) if i not in used_ports]

    start = exe + (" " + cmd if cmd else "")
    if envs:
        start = " ".join(envs) + " " + start
    if sub:
        start = 'cd "%s" && ' % sub + start
    if pre:
        start = " ; ".join(pre) + " ; " + start
    if len(start) > 4000:
        raise Skip("Startzeile zu lang")

    name = m.get("Meta.DisplayName") or m.get("App.DisplayName") or base
    if re.sub(r"[^a-z0-9]", "", name.lower()) in have_names:
        raise Skip("gibt es schon im Panel")
    tid = "amp-" + base
    if tid in have_ids:
        raise Skip("ID schon vergeben")
    t = {
        "id": tid,
        "name": name,
        "category": "Steam (Windows, Wine)" if wine else "Steam",
        "kind": "steam",
        "steam_app_id": int(app),
    }
    if beta and beta.lower() != "public":
        t["steam_beta"] = beta
    if m.get("App.SteamUpdateAnonymousLogin", "True") == "False":
        t["steam_login"] = True
    if windows_dl:
        t["platform"] = "windows"
    if wine:
        t["runtime"] = "wine"
    t["start"] = start
    t["ports"] = ports
    if unused:
        t["fixed_ports"] = True
    t["vars"] = vars_
    if base in AMP_IMPORT:
        t["amp_import"] = AMP_IMPORT[base]
    t["memory_mb"] = 6144 if wine else 4096
    t["min_memory_mb"] = 2048
    t["console"] = "stdin"
    ready = m.get("Console.AppReadyRegex", "")
    if ready and ready not in ("^$", ".*", "^.*$") and not re.search(r"\(\?<?[=!]", ready):
        t["ready"] = ready
    q = next((refidx[r] for r in refidx if "query" in r.lower()), None)
    if q is None and re.search(r"srcds|hlds", start):
        q = 0
    if q is not None:
        t["query"] = {"type": "a2s", "port": q}
    saved = sorted({re.sub(r"(/Saved)/.*$", r"\1", c["file"]) for c in configs if "/Saved/" in c["file"]})
    if saved:
        t["backup_include"] = saved
    if configs:
        t["config"] = configs
    t["status"] = "beta" if tid in PASSED else ("defekt" if tid in BROKEN else "experimentell")
    notes = "Aus den AMP-Vorlagen von CubeCoders erzeugt (MIT), noch nicht im Selbsttest geprüft."
    if wine:
        notes += " Läuft über Wine."
    if unused:
        notes += " Einige Ports stehen nur in der Spielkonfiguration: Standard-Ports, nur ein Server pro Rechner."
    if t.get("steam_login"):
        notes += " Braucht ein angemeldetes Steam-Konto (Einstellungen → Steam-Konto)."
    if tid in BROKEN:
        notes += " " + BROKEN[tid]
    t["notes"] = notes
    return t


def main():
    os.makedirs(OUT, exist_ok=True)
    for f in glob.glob(os.path.join(OUT, "amp-*.json")):
        os.remove(f)
    made, skipped = [], {}
    for k in sorted(glob.glob(os.path.join(SRC, "*.kvp"))):
        base = os.path.basename(k)[:-4]
        if not os.path.exists(os.path.join(SRC, base + "config.json")):
            continue
        try:
            t = build(base)
        except Skip as e:
            skipped[base] = str(e)
            continue
        with open(os.path.join(OUT, t["id"] + ".json"), "w", encoding="utf-8") as out:
            json.dump(t, out, ensure_ascii=False, indent=2)
            out.write("\n")
        made.append(t)
    with open(os.path.join(OUT, "_bericht.md"), "w", encoding="utf-8") as r:
        r.write("# AMP-Vorlagen\n\nErzeugt: %d · übersprungen: %d\n\n## Erzeugt\n\n" % (len(made), len(skipped)))
        for t in made:
            r.write("- %s (%s, App %d)\n" % (t["name"], t["id"], t["steam_app_id"]))
        r.write("\n## Übersprungen\n\n")
        for b, why in sorted(skipped.items()):
            if why != "gibt es schon im Panel":
                r.write("- %s: %s\n" % (b, why))
    print("erzeugt %d, übersprungen %d" % (len(made), len(skipped)), file=sys.stderr)


main()
