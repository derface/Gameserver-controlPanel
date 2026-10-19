# Contributing / Mitmachen

Thanks for helping! Deutsch weiter unten.

## English

- **Bugs and game requests:** open an [issue](../../issues/new/choose). For bugs, the panel version (bottom left), the game and the last lines of the console or of `journalctl -u gamepanel -n 100` help a lot. Please remove passwords and IP addresses first.
- **Code:** Go 1.24 standard library only, plain JavaScript without a build step. Run `make test` (`go vet` and `go test`) before a pull request. Keep changes focused; one feature or fix per pull request.
- **Texts:** the panel is written in German and translated on display. New German UI texts need an English entry in `tools/i18n/en/*.json`, then `python3 tools/i18n/build.py`.
- **Game templates:** live in `internal/templates/games/*.json`. Run the self-test (Self-test page) for the game and mention the result.

By contributing you agree that your contribution is licensed under the AGPL-3.0.

## Deutsch

- **Fehler und Spielwünsche:** als [Issue](../../issues/new/choose) melden. Bei Fehlern helfen die Panel-Version (unten links), das Spiel und die letzten Zeilen der Konsole bzw. von `journalctl -u gamepanel -n 100`. Passwörter und IP-Adressen vorher entfernen.
- **Code:** Go 1.24 nur mit Standardbibliothek, JavaScript ohne Build-Schritt. Vor einem Pull Request `make test` ausführen. Bitte ein Thema pro Pull Request.
- **Texte:** Das Panel ist auf Deutsch geschrieben und wird beim Anzeigen übersetzt. Neue Texte brauchen einen englischen Eintrag in `tools/i18n/en/*.json`, danach `python3 tools/i18n/build.py`.
- **Spielvorlagen:** liegen in `internal/templates/games/*.json`. Bitte den Selbsttest für das Spiel laufen lassen und das Ergebnis nennen.

Mit einem Beitrag bist du einverstanden, dass er unter der AGPL-3.0 steht.
