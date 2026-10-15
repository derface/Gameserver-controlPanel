#!/usr/bin/env python3
"""Builds web/static/i18n/en.json from tools/i18n/en/*.json.

Each source file maps German → English. Keys from Go code may contain
format verbs (%s, %d, %[2]s …); those become regex patterns, everything
else is an exact entry. patterns.json holds hand-written patterns as
[[regex, replacement], …] with {0}, {1} … for the captured groups.

  python3 tools/i18n/build.py          build
  python3 tools/i18n/build.py --check  only report problems
"""
import glob, json, os, re, sys

ROOT = os.path.dirname(os.path.dirname(os.path.dirname(os.path.abspath(__file__))))
SRC = os.path.join(ROOT, 'tools', 'i18n', 'en')
OUT = os.path.join(ROOT, 'web', 'static', 'i18n', 'en.json')

VERB = re.compile(r'%(\[(\d+)\])?[-+# 0]*\d*(?:\.\d+)?[sdvqwfgxXct]')
norm = lambda s: re.sub(r'\s+', ' ', s).strip()
# escapes that JavaScript accepts in unicode mode
esc = lambda s: re.sub(r'([\\^$.*+?()\[\]{}|/])', r'\\\1', s)


def to_pattern(key, val):
    key = re.sub(r'%%(?=[sdv])', '%', key)
    val = re.sub(r'%%(?=[sdv])', '%', val)
    parts, pos, n = [], 0, 0
    for m in VERB.finditer(key):
        parts.append(esc(key[pos:m.start()].replace('%%', '%')))
        parts.append('(.+?)')
        pos, n = m.end(), n + 1
    parts.append(esc(key[pos:].replace('%%', '%')))
    rx = norm(''.join(parts))
    seq = iter(range(100))

    def rep(m):
        return '{%d}' % (int(m.group(2)) - 1 if m.group(2) else next(seq))
    to = VERB.sub(rep, val).replace('%%', '%')
    return rx, norm(to), n


def main():
    check = '--check' in sys.argv
    exact, patterns, problems, inline, html = {}, [], [], [], {}
    for f in sorted(glob.glob(os.path.join(SRC, '*.json'))):
        name = os.path.basename(f)
        data = json.load(open(f, encoding='utf-8'))
        if name == 'html.json':
            html = {norm(k): v for k, v in data.items()}
            continue
        if name == 'inline.json':
            for rx, to in data:
                re.compile(rx.replace('\\p{L}', 'a'))
            inline = data
            continue
        if name == 'patterns.json':
            for rx, to in data:
                re.compile(rx)
                patterns.append((len(re.sub(r'\([^)]*\)[?*+]?|\\.|[\[\]^$]', '', rx)), rx, to))
            continue
        go = name.startswith('go')
        for k, v in data.items():
            if not v:
                continue
            if go and VERB.search(k):
                rx, to, n = to_pattern(k, v)
                used = {int(i) for i in re.findall(r'\{(\d+)\}', to)}
                if used - set(range(n)):
                    problems.append(f'{name}: {k!r}: placeholder out of range')
                lit = len(re.sub(r'\(\.\+\?\)|\\', '', rx))
                if lit < 4:
                    continue  # nothing to recognise it by
                patterns.append((lit, rx, to))
                continue
            nk = norm(k)
            if not nk:
                continue
            if nk in exact and exact[nk] != norm(v):
                problems.append(f'{name}: {nk!r} twice: {exact[nk]!r} / {norm(v)!r}')
            exact[nk] = norm(v)
    patterns.sort(key=lambda p: -p[0])
    # patterns with little fixed text ("(.+?), (.+)") only run when nothing
    # else changed the text, so they do not split known phrases apart
    seen, plist, generic = set(), [], []
    for lit, rx, to in patterns:
        if rx not in seen:
            seen.add(rx)
            (generic if lit < 6 else plist).append([rx, to])
    for p in problems:
        print('!', p)
    print(f'{len(exact)} texts, {len(plist)} patterns, {len(generic)} generic')
    if check:
        return
    os.makedirs(os.path.dirname(OUT), exist_ok=True)
    with open(OUT, 'w', encoding='utf-8') as fh:
        json.dump({'exact': exact, 'patterns': plist, 'generic': generic, 'inline': inline, 'html': html}, fh, ensure_ascii=False, separators=(',', ':'), sort_keys=True)
    print('written', os.path.relpath(OUT, ROOT), os.path.getsize(OUT) // 1024, 'KB')


main()
