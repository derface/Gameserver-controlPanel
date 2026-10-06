# [Panel] – Gameserver Control Panel

Selbst gehostetes Control Panel für Gameserver mit 3D-Übersicht und KI-Assistent.
Eine einzige Datei, läuft auf jedem Linux mit Docker.

> Arbeitsname `[Panel]`. Der endgültige Produktname kommt noch.

## Was Version 0.1 kann

- **Ersteinrichtung & Login:** Admin-Konto beim ersten Aufruf, Sitzungen per Cookie, Schutz gegen Passwort-Raten.
- **Gameserver per Vorlage:** Minecraft Java und Bedrock, Valheim, Palworld, Counter-Strike 2, Satisfactory, Project Zomboid, 7 Days to Die, Rust, Garry's Mod, TF2 – plus **„Eigenes Steam-Spiel“** für jeden Linux-Dedicated-Server, der anonym über SteamCMD ladbar ist.
- **Jeder Server ein Docker-Container:** eigener Datenordner, RAM-Limit, automatische Portvergabe (zweiter Valheim-Server bekommt 2458/2459 usw.), startet nach einem Neustart des Rechners wieder.
- **Live:** Status (vorbereiten → installieren → starten → läuft), CPU/RAM je Server, Rechner-Auslastung, Live-Konsole mit Befehlseingabe.
- **3D-Server-Landschaft** und **KI-Kern** im freigegebenen Design, Hell/Dunkel, Strg+K-Befehlssuche, Animationen abschaltbar.
- **KI-Assistent:** Anthropic, OpenAI oder Ollama (lokal) mit eigenem Key. Die KI erstellt einen Plan, du gibst ihn frei, dann legt das Panel den Server an.
- **Lizenzen:** Offline geprüfte, signierte Schlüssel mit Edition (Testmodus/Starter/Pro/Ultimate) und „Updates bis“-Datum.

## Auf dem Homeserver testen

Empfehlung: eine **eigene Debian-12-VM** auf Proxmox (nicht in einem unprivilegierten LXC – Docker macht dort Ärger, siehe CT 100).

1. **VM anlegen** (Proxmox-Weboberfläche → „VM erstellen“):
   Debian 12 netinst-ISO, 4 Kerne, 8–16 GB RAM, 80 GB Disk auf `local-lvm`, Netzwerk `vmbr0` mit fester IP im Heimnetz (z. B. `192.168.178.30`).
   Bei der Installation nur „SSH-Server“ und „Standard-Systemwerkzeuge“ wählen.
2. **Panel bauen** (auf dem Arbeitsrechner, Go 1.24 nötig):
   ```bash
   git clone git@github.com:derface/Gameserver-controlPanel.git && cd Gameserver-controlPanel
   make dist
   scp dist/gamepanel-linux-amd64 dist/install.sh root@192.168.178.30:/root/
   ```
3. **Installieren** (in der VM):
   ```bash
   ssh root@192.168.178.30
   bash install.sh --binary ./gamepanel-linux-amd64
   ```
   Das Skript installiert Docker, legt den Dienst `gamepanel` an und zeigt die Adresse, z. B. `http://192.168.178.30:8420`.
4. **Admin-Konto anlegen**, dann ersten Server erstellen. Ohne Lizenz läuft der **Testmodus mit einem Server**. Einen Pro-Schlüssel zum Testen erzeugst du mit `licgen` (unten).
5. **Von außen spielen:** In der FritzBox die Game-Ports auf die **VM-IP** weiterleiten.
   ⚠️ Die AMP-VM (`192.168.178.19`) nutzt vermutlich dieselben Standard-Ports. Zwei Weiterleitungen auf denselben Port gehen nicht – solange beide laufen, im Panel andere Ports verwenden oder den AMP-Server stoppen.

Beim **ersten Steam-Server** baut das Panel einmalig die Steam-Laufzeit (Debian + SteamCMD, 2–5 Minuten), danach lädt SteamCMD das Spiel in den Serverordner. Der Fortschritt steht auf der Karte und in der Konsole.

### Aktualisieren

Wenn das Panel per Git installiert wurde (`/opt/gamepanel-src`):
```bash
cd /opt/gamepanel-src && bash scripts/update.sh
```
Holt den neuesten Stand, baut das Panel und installiert es neu. Server und Spielstände bleiben erhalten.
Wurde die Steam-Laufzeit geändert, bekommen Steam-Server beim nächsten Start/Neustart automatisch einen neuen Container.

Nützlich:
```bash
journalctl -u gamepanel -f            # Panel-Logs
docker ps --filter label=gamepanel.id # alle Gameserver-Container
ls /var/lib/gamepanel/servers/        # Spieldaten, ein Ordner pro Server
```

## Spiele und Vorlagen

Das Panel bringt **75 Vorlagen** mit. Der Status kommt aus dem Selbsttest (siehe unten):

- **stabil** (im Spiel erprobt): Minecraft Java, Minecraft Bedrock, Valheim, Palworld.
- **beta** (Selbsttest bestanden): Enshrouded und Abiotic Factor (beide über Wine), Rust, 7 Days to Die, Project Zomboid, Satisfactory, Factorio, Terraria, TeamSpeak 3 sowie 40 Community-Spiele, u. a. ARK: Survival Evolved, CS 1.6, CS: Condition Zero, CS: Source, CS:GO, Left 4 Dead 1 und 2, Day of Defeat, TFC, TF2 Classified, Squad 44, Soulmask, The Front, Mordhau, Pavlov VR, Core Keeper, Tower Unite.
- **experimentell** (noch nicht bestanden oder nach einer Korrektur noch nicht erneut getestet): CS2, TF2, Garry's Mod, V Rising, Sons of the Forest und die übrigen Community-Spiele.
- **defekt**: im Selbsttest abgestürzt, Ursache offen. Diese Vorlagen sind im Panel ausgeblendet und für die KI gesperrt, der Selbsttest prüft sie weiter.

Die Community-Vorlagen werden automatisch aus den Daten von [LinuxGSM](https://github.com/GameServerManagers/LinuxGSM) erzeugt (MIT-Lizenz, siehe `internal/templates/games/community/LINUXGSM-LICENSE.md`), inklusive der Nacharbeiten aus LinuxGSMs `fix_*.sh` (Bibliothekspfade, steamclient.so, GoldSrc-Mods per `app_set_config`, Basis-Apps wie TF2 für TF2 Classified).

Neu erzeugen: `git clone --depth 1 https://github.com/GameServerManagers/LinuxGSM /tmp/lgsm && python3 tools/gen_lgsm.py /tmp/lgsm internal/templates/games/community`. Spiele, die einen Steam-Login brauchen oder deren Ports nur in einer Spielkonfiguration stehen, werden übersprungen.

Vorlagen-Felder seit 0.6: `steam_mod` (GoldSrc-Mod über App 90), `steam_base_app` + `steam_base_dir` (zusätzliche App, die vorher installiert wird), `steam_preplatform` (Erstinstallation einmal mit anderer Plattform, z. B. L4D2), Status `defekt`. Seit 0.4: `packages` (zusätzliche Debian-Pakete, eigene Image-Schicht je Paketkombination), `runtime: "wine"` + `platform: "windows"` (Windows-Server über Wine), `log_file` (Spiel schreibt in eine Datei, die Konsole liest mit), `data_path` / `data_uid` / `cmd` für Container-Images.

### Selbsttest

Einstellungen → Selbsttest (oder Strg+K → „Selbsttest“). Das Panel prüft die Vorlagen auf dem eigenen Rechner, eine nach der anderen, kleine Downloads zuerst:

1. Download über SteamCMD bzw. Image laden
2. Start, ohne Absturz oder Neustart durch Docker
3. Bereit-Meldung im Log (falls die Vorlage eine hat)
4. Antwort auf die Spielerabfrage (A2S bzw. Minecraft-Ping)
5. 30 Sekunden stabil, danach wird der Testserver samt Dateien gelöscht

Ergebnis je Spiel: **ok**, **unsicher** (läuft, aber Abfrage fehlt oder ist nicht prüfbar), **fehler** (mit den letzten Logzeilen) oder **übersprungen** (feste Ports belegt, zu wenig Platz oder RAM). Ein Spielekauf ist nicht nötig: Dedicated Server lädt SteamCMD anonym. Testserver zählen nicht gegen das Server-Limit der Edition. Der Lauf übersteht Neustarts des Panels und setzt fort; ab 10 GB freiem Platz hält er an. Der Bericht lässt sich als Markdown herunterladen.

## Spiel-Einstellungen

„Server verwalten“ → **Einstellungen** zeigt ein Formular statt Konfigurationsdateien:

- **Start-Einstellungen:** die Variablen der Vorlage (Servername, Passwort, Weltname …). Ändern legt den Container neu an, Spielstände bleiben.
- **Dateieinstellungen:** Werte direkt in den Konfigurationsdateien des Spiels, z. B. Minecraft `server.properties`, Palworld `PalWorldSettings.ini`, Enshrouded `enshrouded_server.json`, 7DTD `serverconfig.xml`, Zomboid-INI, Factorio `server-settings.json`, V Rising, Sons of the Forest, ARK `GameUserSettings.ini` sowie `server.cfg` aller Source- und GoldSrc-Spiele.

Nur geänderte Werte werden geschrieben, alles andere in der Datei (Kommentare, Reihenfolge, unbekannte Schlüssel) bleibt unverändert. Läuft der Server, wird er dafür gestoppt und wieder gestartet, weil viele Spiele ihre Konfiguration beim Beenden überschreiben. Einstellungen, die es in der installierten Spielversion nicht gibt, werden ausgeblendet. Passwörter werden nie an den Browser geschickt.

Vorlagenfeld `config`: Liste von Dateien mit `file` (relativ zum Serverordner, `{{var}}` erlaubt), `format` (`properties`, `ini` mit Schlüsseln `Abschnitt/Name`, `json` mit Punkt-Pfaden, `cvar`, `xmlprop`, `palworld`), optional `init_from` (Vorlage, falls die Datei fehlt) und `settings` (`key`, `label`, `type` text/number/bool/select/secret, `options`/`labels`, `true`/`false` für die Schreibweise in der Datei, `min`/`max`, `help`).

## Mods und Workshop

- **Minecraft Java:** Reiter **Mods** unter „Server verwalten“. Suche auf Modrinth, gefiltert nach Server-Art (Fabric, Forge, NeoForge, Quilt → Mods; Paper, Purpur → Plugins) und Minecraft-Version, nur Projekte, die auf dem Server laufen. Änderungen werden gesammelt und mit „Übernehmen“ gespeichert; das Image itzg/minecraft-server lädt die Mods samt Pflicht-Abhängigkeiten beim Start und räumt entfernte wieder ab. Vanilla hat keine Mods, dann zeigt der Reiter den Hinweis, die Server-Art umzustellen.
- **Project Zomboid:** Workshop-IDs, Mod-IDs und Karte unter Einstellungen; der Server lädt die Workshop-Inhalte beim Start selbst.
- **Garry's Mod:** Workshop-Sammlung (ID) plus Steam-Web-API-Schlüssel unter Einstellungen → Start-Einstellungen.

Vorlagenfeld `mods`: `{"source": "modrinth", "var": "mods", "type_var": "type", "version_var": "version"}`.

## Aufgaben, Absturz-Wächter, Verlauf

Im Dialog „Server verwalten“ gibt es die Reiter **Aufgaben** und **Verlauf**.

- **Geplante Aufgaben:** Neustart, Update, Backup, Befehl, Starten oder Stoppen, täglich zu einer Uhrzeit, an bestimmten Wochentagen oder alle N Stunden. Neustart, Update und Stopp können die Spieler 1 bis 30 Minuten vorher warnen (Erinnerungen bei 30, 15, 10, 5 und 1 Minute), sofern das Spiel einen Chat-Befehl hat (Vorlagenfeld `broadcast`, z. B. `say {{msg}}`). Verpasste Läufe (Panel war aus) werden nach 30 Minuten übersprungen, damit ein 4-Uhr-Neustart nicht mittags passiert. Jede Aufgabe lässt sich auch sofort ausführen.
- **Absturz-Wächter:** Docker startet einen abgestürzten Server neu, das Panel trägt den Absturz in den Verlauf ein. Bei 3 Abstürzen in 10 Minuten hält das Panel den Server an und zeigt den Grund. Optional: Neustart, wenn ein Server, der auf die Spielerabfrage geantwortet hat, 2 bis 30 Minuten lang nicht mehr antwortet. Der automatische Neustart lässt sich pro Server abschalten.
- **Update per Knopf:** Steam-Spiele werden beim nächsten Start per SteamCMD aktualisiert (ein laufender Server startet dafür neu), Container-Spiele laden ihr Image neu.
- **Verlauf:** Anlegen, Starts und Stopps (mit Benutzer), Abstürze, Updates, Aufgaben, Backups und Wiederherstellungen, die letzten 200 Einträge je Server.

## Backups und Dateien

Auf der Server-Karte bzw. in der Detailansicht: **Backups** und **Dateien**.

- **Backups** liegen unter `/var/lib/gamepanel/backups/<server-id>/` als `.tar.gz`. Gesichert werden die Spieldaten, nicht die per SteamCMD geladenen Spieldateien (die lädt das Panel jederzeit neu). Was genau, steht im Backup-Tab („Gesichert werden: …“) und kommt aus `backup_exclude`/`backup_include` der Vorlage.
- Minecraft Java wird im laufenden Betrieb konsistent gesichert (`save-off` / `save-all flush` / `save-on`).
- **Automatisch** alle 6 h, 12 h oder täglich zu einer Uhrzeit (Pro und Ultimate). Alte automatische Backups werden nach „Behalten“ gelöscht, manuelle nie.
- **Wiederherstellen** sichert vorher den aktuellen Stand („Vor Wiederherstellung“, die letzten 3 bleiben), stoppt den Server, ersetzt die Daten und startet ihn wieder.
- **Dateien:** Ordner durchsuchen, Textdateien bis 2 MB im Browser bearbeiten (Strg+S), hochladen (auch per Drag & Drop), herunterladen, umbenennen, löschen. Zugriff ist auf den Serverordner beschränkt.

### Externe Backups

Einstellungen → **Externe Backups**. Jedes Backup wird nach dem Erstellen zusätzlich kopiert:
- **Ordner:** z. B. eine zweite Platte oder ein NAS-Share, eingehängt unter `/mnt/backup`. Nicht im Panel-Datenordner.
- **S3-kompatibel:** Backblaze B2, Wasabi, Hetzner Object Storage, MinIO, AWS S3. Endpunkt, Bucket, Access Key, Secret Key; die Region wird bei den bekannten Anbietern aus dem Endpunkt erkannt. Große Backups werden in 64-MB-Teilen hochgeladen.
  Beispiel Backblaze: Endpunkt `https://s3.eu-central-003.backblazeb2.com`, Key ID + Application Key eines Keys mit Zugriff auf den Bucket.

Ablage extern: `<unterordner>/<server-id>/<backup>.tar.gz`. Automatische Backups werden extern nach „Extern behalten“ aufgeräumt, manuelle nie. Im Backup-Tab zeigt die Spalte „Ort“, wo ein Backup liegt; nur extern vorhandene Backups lassen sich herunterladen und wiederherstellen. „Löschen“ entfernt ein Backup lokal und extern.

Die S3-Signatur (AWS SigV4) ist gegen botocore geprüft; getestet wurde gegen einen lokalen S3-Nachbau (moto), noch nicht gegen einen echten Anbieter.

Seit Laufzeit v3 zeigt das Home-Verzeichnis der Steam-Container auf `/data/home` im Serverordner. Spiele, die dort speichern (Project Zomboid, 7 Days to Die, Satisfactory), verlieren ihre Stände dadurch nicht mehr beim Neuanlegen des Containers.

## Lizenzen

```bash
go run ./cmd/licgen keygen   # einmalig: erzeugt licgen-private.key + öffentlichen Schlüssel
go run ./cmd/licgen sign -edition pro -name "Max Muster" -months 12
```
- Der **öffentliche** Schlüssel steht in `internal/license/license.go` (`PublicKey`).
- Der **private** Schlüssel gehört nur dir. **Nie ins Repo** (steht in `.gitignore`). Wer ihn hat, kann Lizenzen erzeugen.
- Editionen: Testmodus 1 Server · Starter 3 · Pro 10 + KI · Ultimate unbegrenzt + KI.

## Eigene Vorlagen

Eine JSON-Datei nach `/var/lib/gamepanel/templates/` legen und das Panel neu starten (`systemctl restart gamepanel`). Gleiche `id` wie eine eingebaute Vorlage überschreibt diese.

```json
{
  "id": "mein-spiel", "name": "Mein Spiel", "category": "Steam", "kind": "steam",
  "steam_app_id": 123456,
  "start": "./server_binary -port {{port}} -name \"{{server_name}}\"",
  "ports": [{"name": "Spiel", "port": 27015, "proto": "udp"}],
  "vars": [{"key": "server_name", "label": "Servername", "default": "Mein Server"}],
  "memory_mb": 4096, "min_memory_mb": 2048,
  "console": "stdin", "ready": "Server started", "status": "beta"
}
```
- `kind: steam` – nutzt die gemeinsame Steam-Laufzeit, installiert per SteamCMD nach `/data/server`.
- `kind: image` – beliebiges Docker-Image (`image`, `env`), z. B. die Minecraft-Vorlagen.
- Platzhalter: `{{port}}` (erster Port), `{{port_2}}` …, sowie jede Variable. Variablentypen: `text`, `number`, `secret`, `select` (+`options`), `bool` (+`flag`), `slug`, `command`.
- `console`: `stdin` (Befehle gehen an die Server-Eingabe) oder `exec` mit `exec_prefix` (z. B. `rcon-cli`).
- `ready`: Regulärer Ausdruck im Log, ab dem der Server als „läuft“ gilt.

## Aufbau

```
cmd/gamepanel     Programmstart
cmd/licgen        Lizenzschlüssel erzeugen (nur für dich)
internal/api      HTTP-API + Auslieferung der Oberfläche
internal/manager  Server anlegen/starten/stoppen, Live-Status, Ports
internal/docker   Docker-Engine-API über den Unix-Socket
internal/templates Vorlagen (games/*.json)
internal/ai       Plan-Erstellung mit Anthropic/OpenAI/Ollama
internal/license  Ed25519-Lizenzprüfung
web/static        Oberfläche (HTML/CSS/JS + three.js), wird in die Datei eingebettet
scripts/install.sh Installer
```
Nur Go-Standardbibliothek, keine externen Abhängigkeiten. Daten liegen in `/var/lib/gamepanel/panel.json` (Rechte 600).

## Stand & Grenzen von 0.1

- Getestet: kompletter Ablauf mit echtem Docker (anlegen, starten, Konsole, Befehle, stoppen, RAM ändern, zweite Instanz mit neuem Port, löschen), Login, Lizenzen.
- Auf echter Hardware geprüft (Debian 13, Docker 29): Minecraft Java (Beitritt mit Client), Valheim (SteamCMD-Download, Start). Die übrigen Steam-Vorlagen (Status „Beta“) sind noch ungetestet.
- Spielerzahlen: Minecraft Java (Server-List-Ping) sowie Valheim, CS2, GMod, TF2, 7DTD (Steam A2S). Übrige Spiele zeigen CPU statt Spieler.
- Das Panel läuft als root (braucht Docker-Zugriff). Nicht direkt ins Internet stellen – nur im Heimnetz oder hinter Traefik mit HTTPS.
- API-Keys liegen im Klartext in `panel.json` (nur für root lesbar). Verschlüsselung folgt.

## Nächste Schritte

1. Selbsttest über alle Vorlagen laufen lassen, Fehler korrigieren, Status anpassen
2. Lizenz-Server + Anbindung an den Shop, Online-Prüfung
5. Mehrere Rechner (Nodes), Mehrbenutzer, Discord-Webhooks
6. KI-Überwachung (Crash-Analyse, Tipps wie „Sichtweite senken“)
