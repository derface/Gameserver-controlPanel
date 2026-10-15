# Übersetzung (Englisch)

- `en/web*.json`, `en/go*.json`: Texte aus der Web-Oberfläche und dem Go-Code, Deutsch → Englisch. Go-Texte dürfen Formatangaben enthalten (`%s`, `%d`, …); daraus werden Muster. Im Englischen geht `%[2]s` für eine andere Reihenfolge.
- `en/patterns.json`: Muster von Hand, `[regex, ersatz]`, `{0}`, `{1}` … für die Gruppen (werden selbst wieder übersetzt).
- `en/inline.json`: kleine Stücke mit Zahlen mitten im Text („3 Std.“), `$1` für Gruppen.
- `en/html.json`: Absätze mit Auszeichnung (`<b>`, Links) als Ganzes.
- Spielvorlagen: `internal/templates/en.json` (vom Server angewendet).

`python3 tools/i18n/build.py` baut daraus `web/static/i18n/en.json`; `--check` meldet nur Probleme.
