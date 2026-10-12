#!/usr/bin/env python3
"""Packt AMP-Instanzen (CubeCoders AMP) für das Gameserver Control Panel.

Mitgenommen werden nur Spielstände, Spiel-Configs und die AMP-Einstellungen
der Instanz, nicht die Spieldateien (die lädt das Panel frisch über Steam).
Laufende Instanzen werden übersprungen, damit kein halb geschriebener
Spielstand mitkommt.

Aufruf (als root auf dem AMP-Rechner), den genauen Befehl zeigt das Panel:
    curl -fsSk <panel>/import/amp.py | python3 - --panel <panel> --token <token>
Ohne --panel/--token werden die Archive nur nach /root gelegt.
"""
import argparse
import glob
import io
import json
import os
import re
import ssl
import subprocess
import sys
import tarfile
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

ROOT = "/home/amp/.ampdata/instances"
SKIP_DIRS = {".trash", "Logs", "logs", "Crashes", "crashes", "steamapps", "Backups", ".cache",
             "__pycache__", "CrashReportClient", "Webcache", "webcache"}
SKIP_FILE = re.compile(r"\.(log|dmp|mdmp|tmp)$|^core(\.\d+)?$", re.I)
SAVE_DIRS = {"Zomboid", "savegame", "savegames", "saves", "Saves", "SaveGames", "worlds", "world", "Worlds"}


def say(msg):
    print(msg, flush=True)


def kvp(path):
    out = {}
    with open(path, encoding="utf-8-sig", errors="replace") as f:
        for line in f:
            if "=" in line and not line.startswith("#"):
                k, v = line.rstrip("\n").split("=", 1)
                out[k.strip()] = v.strip()
    return out


def instance_list():
    """Name -> {friendly, running} from ampinstmgr, or {} if it cannot be asked."""
    try:
        txt = subprocess.run(["su", "-l", "amp", "-c", "ampinstmgr -l"], capture_output=True, text=True,
                             timeout=60).stdout
    except Exception:
        return {}
    res, cur = {}, None
    for line in txt.splitlines():
        parts = re.split(r"\s*[│|]\s*", line.strip(), maxsplit=1)
        if len(parts) != 2:
            continue
        k, v = parts[0].strip(), parts[1].strip()
        if k == "Instance Name":
            cur = res.setdefault(v, {"friendly": v, "running": False, "module": ""})
        elif cur is not None and k == "Friendly Name":
            cur["friendly"] = v
        elif cur is not None and k == "Running":
            cur["running"] = v.lower() == "yes"
        elif cur is not None and k == "Module":
            cur["module"] = v
    return res


def skipped(rel):
    parts = rel.split("/")
    return any(p in SKIP_DIRS for p in parts[:-1]) or SKIP_FILE.search(parts[-1]) is not None


def collect(inst, m):
    base = os.path.normpath(os.path.join(inst, m.get("App.BaseDirectory", "").strip() or "."))
    picks = set()
    # 1. the game's config files AMP knows about
    try:
        with open(os.path.join(inst, "metaconfig.json"), encoding="utf-8-sig") as f:
            meta = json.load(f)
    except Exception:
        meta = []
    for c in meta if isinstance(meta, list) else []:
        cf = str((c or {}).get("ConfigFile", "")).replace("\\", "/")
        if not cf:
            continue
        pat = re.sub(r"\{\{[^}]*\}\}", "*", cf)
        for p in glob.glob(os.path.join(base, pat)):
            if os.path.isfile(p):
                picks.add(os.path.relpath(p, base))
    # 2. save folders: Unreal "Saved" (not Engine/Saved) and well-known names
    for dirpath, dirnames, _ in os.walk(base):
        rel = os.path.relpath(dirpath, base)
        depth = 0 if rel == "." else rel.count(os.sep) + 1
        keep = []
        for d in dirnames:
            if d in SKIP_DIRS or d.startswith(".") and d != ".config":
                continue
            r = d if rel == "." else os.path.join(rel, d)
            if d == "Saved" and os.path.basename(dirpath) != "Engine":
                picks.add(r)
            elif d in SAVE_DIRS and depth <= 1:
                picks.add(r)
            elif depth < 3:
                keep.append(d)
        dirnames[:] = keep
    # 3. folders the instance settings point at, e.g. saveDirectory=./savegame
    try:
        settings = json.loads(m.get("App.AppSettings", "{}"))
    except ValueError:
        settings = {}
    for k, v in settings.items():
        if isinstance(v, str) and v.startswith("./") and "log" not in k.lower():
            p = os.path.normpath(os.path.join(base, v))
            if p.startswith(base + os.sep) and os.path.isdir(p):
                picks.add(os.path.relpath(p, base))
    # drop entries inside other picks
    out = []
    for p in sorted(picks, key=len):
        if not any(p.startswith(q + os.sep) for q in out):
            out.append(p)
    return base, out


def pack(name, inst, info, out=None):
    m = kvp(os.path.join(inst, "GenericModule.kvp"))
    base, picks = collect(inst, m)
    if not picks:
        say("  keine Spielstände oder Configs gefunden")
    fd, path = tempfile.mkstemp(prefix="amp-%s-" % name, suffix=".tar.gz",
                                dir=out or ("/root" if os.access("/root", os.W_OK) else None))
    os.close(fd)
    files, size = 0, 0
    with tarfile.open(path, "w:gz", compresslevel=6) as tar:
        tar.add(os.path.join(inst, "GenericModule.kvp"), "amp/GenericModule.kvp")
        if os.path.isfile(os.path.join(inst, "metaconfig.json")):
            tar.add(os.path.join(inst, "metaconfig.json"), "amp/metaconfig.json")
        for rel in picks:
            full = os.path.join(base, rel)
            if os.path.isfile(full):
                if not skipped(rel):
                    tar.add(full, "data/" + rel.replace(os.sep, "/"))
                    files, size = files + 1, size + os.path.getsize(full)
                continue
            for dirpath, dirnames, filenames in os.walk(full):
                dirnames[:] = [d for d in dirnames if d not in SKIP_DIRS]
                for fn in filenames:
                    fp = os.path.join(dirpath, fn)
                    r = os.path.relpath(fp, base).replace(os.sep, "/")
                    if skipped(r) or not os.path.isfile(fp) or os.path.islink(fp):
                        continue
                    tar.add(fp, "data/" + r)
                    files, size = files + 1, size + os.path.getsize(fp)
        meta = {"instance": name, "friendly": info.get("friendly", name), "game": m.get("Meta.DisplayName", ""),
                "files": files, "bytes": size, "picked": picks, "exported": int(time.time())}
        data = json.dumps(meta, ensure_ascii=False, indent=1).encode()
        ti = tarfile.TarInfo("amp/instance.json")
        ti.size, ti.mtime = len(data), int(time.time())
        tar.addfile(ti, io.BytesIO(data))
    say("  %d Dateien, %.1f MB: %s" % (files, size / 1e6, ", ".join(picks) or "-"))
    return path


def upload(panel, token, name, path):
    url = panel.rstrip("/") + "/import/amp?" + urllib.parse.urlencode({"token": token, "instance": name})
    size = os.path.getsize(path)
    ctx = ssl.create_default_context()
    ctx.check_hostname, ctx.verify_mode = False, ssl.CERT_NONE  # panels in the LAN often use a self-signed certificate
    with open(path, "rb") as f:
        req = urllib.request.Request(url, data=f, method="POST",
                                     headers={"Content-Type": "application/gzip", "Content-Length": str(size)})
        try:
            with urllib.request.urlopen(req, context=ctx, timeout=3600) as r:
                return json.loads(r.read().decode() or "{}")
        except urllib.error.HTTPError as e:
            try:
                msg = json.loads(e.read().decode()).get("error")
            except Exception:
                msg = str(e)
            raise RuntimeError(msg)


def main():
    ap = argparse.ArgumentParser(description=__doc__, formatter_class=argparse.RawDescriptionHelpFormatter)
    ap.add_argument("instances", nargs="*", help="nur diese Instanzen (Name wie in ampinstmgr -l)")
    ap.add_argument("--panel", help="Adresse des Panels, z. B. https://192.168.178.54:8420")
    ap.add_argument("--token", help="Einmal-Schlüssel aus dem Panel")
    ap.add_argument("--root", default=ROOT, help="Ordner der AMP-Instanzen (Standard: %(default)s)")
    ap.add_argument("--keep", action="store_true", help="Archive nach dem Hochladen behalten")
    ap.add_argument("--out", help="Ordner für die Archive (Standard: /root)")
    args = ap.parse_args()
    if os.geteuid() != 0:
        say("Bitte als root ausführen (su -), sonst fehlen Leserechte.")
        return 1
    if not os.path.isdir(args.root):
        say("AMP-Instanzen nicht gefunden unter %s (anderer Ordner: --root)." % args.root)
        return 1
    known = instance_list()
    names = args.instances or sorted(d for d in os.listdir(args.root)
                                     if os.path.isfile(os.path.join(args.root, d, "GenericModule.kvp")))
    if not names:
        say("Keine Spiele-Instanzen gefunden.")
        return 1
    ok, skip = 0, []
    for name in names:
        inst = os.path.join(args.root, name)
        info = known.get(name, {})
        say("== %s (%s)" % (info.get("friendly", name), name))
        if not os.path.isfile(os.path.join(inst, "GenericModule.kvp")):
            say("  keine Spiele-Instanz, übersprungen")
            continue
        if info.get("running"):
            say("  läuft noch, übersprungen. Erst stoppen: su -l amp -c \"ampinstmgr stop %s\"" % name)
            skip.append(name)
            continue
        try:
            path = pack(name, inst, info, args.out)
            if args.panel and args.token:
                res = upload(args.panel, args.token, name, path)
                say("  ans Panel geschickt: %s" % (res.get("template_name") or res.get("problem") or "ok"))
                if not args.keep:
                    os.remove(path)
            else:
                say("  gespeichert: %s" % path)
            ok += 1
        except Exception as e:  # keep going with the other instances
            say("  Fehler: %s" % e)
    say("")
    say("Fertig: %d übertragen%s." % (ok, (", übersprungen (laufen noch): " + ", ".join(skip)) if skip else ""))
    if ok and args.panel:
        say("Weiter im Panel: Server anlegen → Aus AMP übernehmen.")
    return 0


if __name__ == "__main__":
    sys.exit(main())
