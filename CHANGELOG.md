# Changelog

Alle Versionen von Bytewise Panel, neueste zuerst. Ausführlich beschrieben sind die Funktionen im [Handbuch](docs/handbuch.md).

## v1.4.0 – 2026-10-20

- **Twitch & YouTube**: Twitch-Chat wird anonym mitgelesen (IRC), YouTube-Live-Chat über die Data API (höchstens alle 10 s). Aktionen für Chat-Befehle, Kanalpunkte (mit Texteingabe), Bits, Super Chats, Abos und Raids führen Konsolenbefehle aus und/oder schreiben allen im Spiel; Platzhalter {{user}} {{streamer}} {{amount}} {{text}} (Zuschauertext streng gefiltert), Abklingzeiten global und je Zuschauer, Vorlagen für Minecraft, Live-Ansicht und Test-Knopf. / Twitch & YouTube viewer actions.

## v1.3.0 – 2026-10-20

- **Sprachsteuerung**: Mikrofon-Knopf (oder Alt+V) im Panel und in der Handy-App. Versteht Starten, Stoppen, Neustart, Update, Backup, „Wer spielt?“, Status und „Öffne …“ auf Deutsch und Englisch, findet den Server am Namen oder Spiel, fragt bei Stopp/Neustart/Update nach und liest die Antwort vor; Unbekanntes geht an den KI-Assistenten. / Voice control.

## v1.2.0 – 2026-10-20

- **Smart Home**: Philips Hue (lokale Bridge, Koppeln per Knopfdruck) und Home Assistant (Lampen, Alexa- und Lautsprecher-Ansagen, Handy-Benachrichtigungen, beliebige Dienste) reagieren per Regel auf Ereignisse: Spieler kommt oder geht, Server online oder gestoppt, Absturz, Fehler, Update, Backup, Event, Wunsch. Lampen kehren nach Blinken/Pulsieren in den vorherigen Zustand zurück. / Smart home: Hue and Home Assistant rules.

## v1.1.0 – 2026-10-20

- **Handy-Widget** (Android, KWGT): kompakte Übersicht aller Server unter `/api/widget` mit Status, Spielern, CPU und RAM sowie fertigen Textzeilen; Einrichtung mit Vorschau und Formeln zum Kopieren unter Einstellungen → API & Webhooks. Schlüssel im Link nur mit Leserechten. / Phone widget for Android (KWGT).
- **Als App installierbar** (Web-App-Manifest, Service Worker) mit **Push-Nachrichten** je Gerät und Auswahl der Ereignisse (Abstürze, Fehler, Updates, Spieler kommen, Status …) und der Zahl der Server mit Problemen auf dem App-Symbol. Verschlüsselung nach RFC 8291, VAPID nach RFC 8292, ohne Fremdbibliothek. / Installable app with push notifications and app badge.

## v1.0.0 – 2026-10-19

Erste öffentliche Version. / First public release.

**English**
- Game servers in Docker: about 20 hand-tuned templates (Minecraft Java/Bedrock, Valheim, Palworld, Enshrouded, Rust, 7 Days to Die, Project Zomboid, Satisfactory, Factorio, Terraria …), 130 more from LinuxGSM and AMPTemplates, any SteamCMD game, self-test for templates.
- Settings of every config file as forms with search and history, mods (Modrinth, Steam Workshop, BepInEx and more) with update notices and a mod pack for players.
- Live console with macros and log-flood folding, backups with schedules and external targets, scheduled tasks (only when empty, only on updates, at the quietest hour), crash and hang watchdog.
- AI assistant (Anthropic, OpenAI or local Ollama): plans servers, changes settings, timed events, diagnoses problems.
- Players: online list, kick/ban/whitelist, play time statistics, origin map, seasons. Public status page with wish list and calendar, Discord bot, invite links.
- Multiple machines from one panel, moving servers between them. API keys and signed webhooks.
- Roles, two-factor login, audit log, HTTPS (Let's Encrypt, own certificate, reverse proxy), UPnP port forwarding with reachability check, setup wizard, built-in help, English and German.
- Fit and finish: calm Docker notice, tables and tabs on phones, stopped-console view, default values in settings.

**Deutsch**
- Gameserver in Docker: rund 20 sorgfältig abgestimmte Vorlagen, 130 weitere aus LinuxGSM und AMPTemplates, beliebige SteamCMD-Spiele, Selbsttest der Vorlagen.
- Einstellungen aller Config-Dateien als Formulare mit Suche und Verlauf, Mods (Modrinth, Steam Workshop, BepInEx u. a.) mit Update-Hinweisen und Mod-Paket für Mitspieler.
- Live-Konsole mit Makros und Log-Flut-Zusammenfassung, Backups mit Plan und externem Ziel, geplante Aufgaben (nur wenn leer, nur bei Update, zur ruhigsten Zeit), Absturz- und Hänger-Wächter.
- KI-Assistent (Anthropic, OpenAI oder lokal Ollama): plant Server, ändert Einstellungen, Events auf Zeit, Diagnose bei Problemen.
- Spieler: Online-Liste, Kick/Bann/Whitelist, Spielzeit-Statistik, Herkunftskarte, Saisons. Öffentliche Statusseite mit Wunschliste und Kalender, Discord-Bot, Einladungslinks.
- Mehrere Rechner in einem Panel, Server umziehen. API-Schlüssel und signierte Webhooks.
- Rollen, Zwei-Faktor-Anmeldung, Protokoll, HTTPS (Let's Encrypt, eigenes Zertifikat, Reverse Proxy), UPnP-Portfreigabe mit Erreichbarkeitsprüfung, Einrichtungsassistent, Hilfe im Panel, Deutsch und Englisch.
- Feinschliff: ruhiger Docker-Hinweis, Tabellen und Reiter am Handy, Ansicht der gestoppten Konsole, Standardwerte in den Einstellungen.

## v0.41.0 – 2026-10-19

- Feinschliff der Oberfläche

## v0.40.0 – 2026-10-19

- Log-Flut-Komprimierer und Log-Grenze für Container

## v0.39.0 – 2026-10-19

- API-Schlüssel, Webhooks, Aufgaben zur ruhigsten Zeit, Servername aus dem Spiel

## v0.38.0 – 2026-10-19

- Einrichtungsassistent und Hilfe im Panel

## v0.37.0 – 2026-10-19

- Konsolen-Makros und Wunschliste

## v0.36.0 – 2026-10-15

- Server auf einen anderen Rechner umziehen

## v0.35.3 – 2026-10-15

- CPU-Anzeige: Anteil am Rechner statt Prozent je Kern
- install.sh: Hinweis aufs Admin-Konto nur bei neuer Installation

## v0.35.2 – 2026-10-15

- SteamCMD 0x6: hängende App-Manifeste beiseitelegen

## v0.35.1 – 2026-10-15

- SteamCMD-Fehler 0x6: Download-Stand zurücksetzen und Steams Protokoll zeigen

## v0.35.0 – 2026-10-15

- Statusseite, Discord-Bot und Übersicht über alle Rechner

## v0.34.0 – 2026-10-15

- Mehrere Rechner

## v0.33.0 – 2026-10-15

- Englische Oberfläche

## v0.32.0 – 2026-10-15

- Mod-Paket, Mod-Updates, Discord-Whitelist, Spielerliste, Saisons

## v0.31.0 – 2026-10-15

- Leistungsverlauf, Speicherplatz, Wartungsmodus, Einladungslink, Kalender-Abo, Online-Verlauf

## v0.30.2 – 2026-10-15

- Herkunftskarte erkennt Steam-Networking-Verbindungen

## v0.30.1 – 2026-10-15

- Beitreten: richtige Adresse je Spiel, Heimnetz-Adresse

## v0.30.0 – 2026-10-14

- Herkunftskarte: woher Spieler verbinden

## v0.29.0 – 2026-10-14

- Events auf Zeit, Statusseite, Discord-Bot, Spieler-Statistik

## v0.28.0 – 2026-10-14

- Open Source (AGPL-3.0) ohne Lizenzsystem; Benachrichtigungen (Discord, ntfy); Spielerverwaltung; Panel-Update per Knopf

## v0.27.2 – 2026-10-14

- KI-Änderungen: reply als Vorschlag, Bereiche einhalten

## v0.27.1 – 2026-10-14

- KI-Änderungen: Schlüssel ohne Präfix, Werte unter vars statt Datei, verschachtelte Objekte, Zahlen als Zahl, Gründe in der Meldung

## v0.27.0 – 2026-10-14

- KI-Assistent ändert vorhandene Server (Gesprächsverlauf, Einstellungsliste, Vorschau Bisher → Neu)

## v0.26.0 – 2026-10-14

- Geplante Aufgaben mit Bedingungen: nur wenn leer, nur bei Update, vorher Backup; Vorschläge; Versionsprüfung

## v0.25.0 – 2026-10-14

- Selbsttest-Auswertung 14.10.: Steam-Konto-Fehlalarm, Wine-Bildschirm, Startzeilen

## v0.24.4 – 2026-10-14

- Logo in Orange, passend zur Akzentfarbe

## v0.24.3 – 2026-10-14

- Logo von Bytewise Panel: Anmeldeseite, Seitenleiste, Favicon

## v0.24.2 – 2026-10-14

- Name: Bytewise Panel (sichtbar), technisch weiter gamepanel

## v0.24.1 – 2026-10-13

- Reverse Proxy: echte Besucher-IP und HTTPS-Erkennung hinter Traefik

## v0.24.0 – 2026-10-13

- Steam Workshop für alle Steam-Spiele

## v0.23.1 – 2026-10-13

- Zomboid speichert Welt und Einstellungen wieder im Serverordner (Java-Home auf /data/home, -cachedir), Workshop-Ordner werden überall im Serverordner gefunden, Einstellungs-Sicherungen kollidieren nicht mehr

## v0.23.0 – 2026-10-13

- Steam-Workshop-Suche mit Installieren/Entfernen per Klick (Project Zomboid), optionaler Steam-Web-API-Schlüssel

## v0.22.0 – 2026-10-12

- Reiter Mods für alle Server: installierte Mods (Workshop, Zomboid, BepInEx, Pak, UE4SS, Minecraft) mit Ladezustand aus der Konsole, Steam-Titeln, Update-Hinweis und Schalter

## v0.21.0 – 2026-10-12

- KI-Diagnose je Server: Knopf „Mit KI prüfen“ mit umsetzbaren Vorschlägen, automatische Prüfung nach Abstürzen, Hängern und dauerhaft voller Last; Exit-Code 137 zählt als Absturz

## v0.20.0 – 2026-10-12

- Umzug von AMP: Export-Skript mit Einmal-Schlüssel, Übernahme von Spielständen, Configs und Einstellungen; Auswahllisten zeigen Klartext statt Startparameter

## v0.19.2 – 2026-10-12

- Übersicht: Kamera richtet sich nach dem höchsten Rack samt Namensschild (nichts mehr abgeschnitten), lange Namen mit …

## v0.19.1 – 2026-10-12

- Project Zomboid: Java-Heap passt sich dem zugewiesenen RAM an (vorher fest 8 GB, Server wurde vom System beendet); Platzhalter {{memory_mb}} und {{heap_mb}}

## v0.19.0 – 2026-10-12

- 63 weitere Spiele aus AMPTemplates (experimentell, deutsch), Config-Vorlagen werden vor dem Start angelegt, geteilte Spiel-/RCON-Ports werden freigegeben

## v0.18.0 – 2026-10-12

- Übersicht: Server als Rack-Schränke (Höhe nach RAM, LEDs nach Status und Last)

## v0.17.0 – 2026-10-12

- Einstellungen: alle Werte jeder Config als Kacheln (AMPTemplates-Daten, deutsch), Sicherheitskopien mit Zurückholen, Selbsttest prüft Einstellungen

## v0.16.0 – 2026-10-12

- Einstellungen im Kachel-Layout mit Reitern, Suche und Bedingungen; Enshrouded mit allen Einstellungen (Daten aus AMPTemplates, MIT)

## v0.15.0 – 2026-10-12

- Erreichbarkeit von außen prüfen; RCON/Telnet/Web-Admin nie im Router freigeben; Bedrock-Abfrage

## v0.14.1 – 2026-10-12

- Steam-Konto: Anmeldung mit Mobile Authenticator klappt
- Steam-Konto: SteamCMD-Ausgabe der Anmeldung im Panel anzeigen (ohne Passwort)
- Steam-Konto: Code kann aus E-Mail oder Steam-App kommen
- UPnP: Routername ohne InternetGatewayDevice-Präfix

## v0.14.0 – 2026-10-12

- Automatische Portfreigabe per UPnP
- Enshrouded stabil: im Spiel getestet (Beitritt mit Admin-Rechten, Wine 11)

## v0.13.6 – 2026-10-12

- Kein Update-Check nach Abstürzen, Startskript vom Panel

## v0.13.5 – 2026-10-12

- Enshrouded: gültige Startdatei und Passwort-Regeln

## v0.13.4 – 2026-10-12

- Wine-Einrichtung hängt nicht mehr, Laufzeit 11

## v0.13.3 – 2026-10-12

- Wine-Umgebung wird beim Start wirklich geprüft, Laufzeit 10

## v0.13.2 – 2026-10-12

- Wine-Umgebung repariert sich selbst, Laufzeit 9

## v0.13.1 – 2026-10-12

- Einstellungen schon vor dem ersten Start

## v0.13.0 – 2026-10-10

- Neues Layout: Seitenleiste, eigene Seiten, ruhigerer Stil

## v0.12.0 – 2026-10-10

- Sicherheit & Team: Benutzer mit Rollen, Zwei-Faktor, Protokoll, HTTPS

## v0.11.2 – 2026-10-10

- Korrekturen aus dem vierten Selbsttest

## v0.11.1 – 2026-10-08

- Korrekturen aus dem dritten Selbsttest, Laufzeit 8

## v0.11.0 – 2026-10-08

- Neun Spiele mit Ports in der Spielkonfiguration

## v0.10.0 – 2026-10-08

- Steam-Konto: Anmeldung mit Steam Guard für Spiele ohne anonymen Download

## v0.9.1 – 2026-10-06

- Mods: leere Mod-Liste nicht als null senden (Fehler im Mods-Reiter)

## v0.9.0 – 2026-10-06

- Mods: Modrinth-Verwaltung für Minecraft, Workshop für Zomboid und GMod

## v0.8.0 – 2026-10-06

- Spiel-Einstellungen: Formular statt Konfigurationsdateien

## v0.7.0 – 2026-10-05

- Server-Alltag: geplante Aufgaben, Absturz-Wächter, Update per Knopf, Verlauf

## v0.6.1 – 2026-10-02

- Vorlagen nach zweitem Selbsttest, Laufzeit 6

## v0.6.0 – 2026-10-01

- Selbsttest: Auswahl „noch nicht bestanden“ schließt unsichere ein
- Vorlagen nach Selbsttest korrigiert, Laufzeit 5

## v0.5.0 – 2026-10-01

- Selbsttest: Vorlagen automatisch prüfen (Download, Start, Bereit, Abfrage)
- Enshrouded: TCP-Ports, Favoriten-Hinweis, Status beta

## v0.4.0 – 2026-10-01

- README: Vorlagen-Übersicht, LinuxGSM-Lizenz beigelegt
- Version 0.4.0: 84 Spiel-Vorlagen, Wine für Windows-Server, Spielsuche

## v0.3.0 – 2026-09-30

- Version 0.3.0: Externe Backups (Ordner und S3-kompatibel)
- Wiederherstellung: erst stoppen, dann Sicherung des aktuellen Stands

## v0.2.0 – 2026-09-30

- Version 0.2.0: Backups mit Zeitplan und Wiederherstellung, Dateimanager

## v0.1.1 – 2026-09-30

- Version 0.1.1: Valheim-Crossplay, Startstatus, Spielerzahlen, saubere Konsole, Update-Skript

## v0.1.0 – 2026-09-29

- Version 0.1.0: Panel mit Docker, Vorlagen, 3D-Oberfläche, KI-Assistent und Lizenzen
