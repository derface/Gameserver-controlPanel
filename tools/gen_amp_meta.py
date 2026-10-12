#!/usr/bin/env python3
"""Builds internal/templates/amp/meta.json from CubeCoders' AMPTemplates (MIT).

For every panel template the matching AMP template is found (by the Steam app
id of the dedicated server, else by name). From it we take:
  - settings: key, name, description, type, choices, range, unit, tab
  - files:    config files with a format the panel can edit

Usage:
  git clone --depth 1 https://github.com/CubeCoders/AMPTemplates /tmp/ampt
  python3 tools/gen_amp_meta.py /tmp/ampt internal/templates/games internal/templates/amp/meta.json
"""
import glob
import json
import os
import re
import sys


def load(p):
    try:
        with open(p, encoding="utf-8-sig") as f:
            return json.load(f)
    except Exception:
        return None


def kvp(p):
    out = {}
    try:
        for line in open(p, encoding="utf-8-sig", errors="replace"):
            if "=" in line:
                k, v = line.rstrip("\n").split("=", 1)
                out[k.strip()] = v.strip()
    except OSError:
        pass
    return out


def norm(s):
    return re.sub(r"[^a-z0-9]", "", (s or "").lower())


def amp_index(src):
    by_app, by_name = {}, {}
    for k in sorted(glob.glob(os.path.join(src, "*.kvp"))):
        base = os.path.basename(k)[:-4]
        if not os.path.exists(os.path.join(src, base + "config.json")):
            continue
        meta = kvp(k)
        apps = set()
        ups = load(os.path.join(src, base + "updates.json")) or []
        for u in ups if isinstance(ups, list) else []:
            if isinstance(u, dict) and u.get("UpdateSource") == "SteamCMD":
                for tok in str(u.get("UpdateSourceArgs", "")).split():
                    if tok.isdigit():
                        apps.add(int(tok))
        wd = meta.get("App.WorkingDir", "")
        if wd.isdigit():
            apps.add(int(wd))
        for a in apps:  # several AMP templates per app (e.g. palworld, palworld-modded): keep the plainest
            if a not in by_app or len(base) < len(by_app[a]):
                by_app[a] = base
        for n in (meta.get("Meta.DisplayName"), meta.get("App.DisplayName"), base):
            if n and (norm(n) not in by_name or len(base) < len(by_name[norm(n)])):
                by_name[norm(n)] = base
    return by_app, by_name


INPUT = {"number": "number", "checkbox": "bool", "enum": "select", "text": "text", "Textarea": "text",
         "password": "secret", "Password": "secret", "list": "list", "url": "text"}


def num(v):
    try:
        f = float(v)
        return int(f) if f.is_integer() else f
    except (TypeError, ValueError):
        return None


def settings(src, base):
    raw = load(os.path.join(src, base + "config.json")) or []
    out = []
    for x in raw if isinstance(raw, list) else []:
        if not isinstance(x, dict):
            continue
        key = str(x.get("ParamFieldName") or "").strip()
        if not key or "{{" in key or " " in key:
            continue
        if key.startswith("$."):
            key = key[2:]
        field = str(x.get("FieldName") or "")
        hide = field.startswith("$") or x.get("Hidden") is True or x.get("InputType") == "hidden"
        t = INPUT.get(x.get("InputType"))
        e = {"key": key}
        if hide or not t:
            e["hide"] = True  # ports, IP binding and other things the panel manages
            out.append(e)
            continue
        e["label"] = str(x.get("DisplayName") or key).strip()
        desc = re.sub(r"\[([^\]]+)\]\([^)]+\)", r"\1", str(x.get("Description") or "")).strip()
        if desc:
            e["help"] = desc
        e["type"] = t
        ev = x.get("EnumValues") or {}
        if t == "bool" and isinstance(ev, dict):
            if "True" in ev and str(ev["True"]) not in ("true",):
                e["true"] = str(ev["True"])
            if "False" in ev and str(ev["False"]) not in ("false",):
                e["false"] = str(ev["False"])
        if t == "select":
            opts = [(str(k), str(v)) for k, v in (ev.items() if isinstance(ev, dict) else []) if "{{" not in str(k)]
            if not opts:
                e["type"] = "text"
            else:
                e["options"] = [k for k, _ in opts]
                e["labels"] = [v for _, v in opts]
        if t == "number":
            for a, b in (("MinValue", "min"), ("MaxValue", "max")):
                n = num(x.get(a))
                if n is not None:
                    e[b] = n
        if x.get("Suffix"):
            e["unit"] = str(x["Suffix"])
        sub = str(x.get("Subcategory") or x.get("Category") or "").split(":")[0].strip()
        if sub:
            e["group"] = sub
        if x.get("IncludeInCommandLine") is True:
            e["cmd"] = True
        out.append(e)
    return out


def files(src, base):  # noqa: C901
    meta = load(os.path.join(src, base + "metaconfig.json")) or []
    out = []
    for c in meta if isinstance(meta, list) else []:
        if not isinstance(c, dict):
            continue
        path = str(c.get("ConfigFile") or "")
        if not path or "{{" in path or ".." in path or path.startswith("/"):
            continue
        typ, fmt = c.get("ConfigType"), None
        cf = str(c.get("ConfigFormat") or "")
        low = path.lower()
        if typ == "json" or (typ == "template" and low.endswith(".json")):
            fmt = "json"
        elif typ == "ini" or (typ == "template" and low.endswith(".ini")):
            fmt = "ini"
        elif typ == "kvp" and cf.replace(" ", "") == "{0}={1}":
            fmt = "properties"
        elif typ == "kvp" and cf.startswith("{0} ") and "=" not in cf:
            fmt = "cvar"
        elif typ == "template" and low.endswith(".cfg"):
            fmt = "cvar"
            tpl = os.path.join(src, str(c.get("ConfigFileTemplate") or ""))
            body = open(tpl, errors="replace").read() if os.path.isfile(tpl) else ""
            if re.search(r"(?m)^\s*\[[^\]]+\]\s*$", body):
                fmt = "ini"
            elif len(re.findall(r"(?m)^\s*\w+\s*=.*;\s*(//.*)?$", body)) >= 3:
                fmt = "armacfg"
        if fmt == "cvar" and re.search(r"(?i)arma|dayz", base) and re.search(r"(?i)server\w*\.cfg$", path):
            fmt = "armacfg"
        if fmt and low.endswith(".cfg") and fmt == "cvar" and re.search(r"(?i)bepinex|plugins", path):
            fmt = "ini"  # BepInEx plugin configs are ini files
        if fmt and not any(f["file"] == path for f in out):
            out.append({"file": path, "format": fmt})
    return out


def main(src, games, dst):
    by_app, by_name = amp_index(src)
    result = {}
    for f in sorted(glob.glob(os.path.join(games, "**", "*.json"), recursive=True)):
        t = load(f)
        if not isinstance(t, dict) or "id" not in t:
            continue
        base = by_name.get(norm(t.get("name"))) or by_app.get(t.get("steam_app_id"))
        if not base:
            continue
        st, fl = settings(src, base), files(src, base)
        if not st:
            continue
        result[t["id"]] = {"source": base, "files": fl, "settings": st}
    os.makedirs(os.path.dirname(dst), exist_ok=True)
    with open(dst, "w", encoding="utf-8") as out:
        json.dump(result, out, ensure_ascii=False, indent=0, sort_keys=True)
        out.write("\n")
    shown = sum(1 for v in result.values() for s in v["settings"] if not s.get("hide"))
    print(f"{len(result)} Vorlagen zugeordnet, {shown} Einstellungen", file=sys.stderr)


if __name__ == "__main__":
    main(*sys.argv[1:4])
