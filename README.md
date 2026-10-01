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

Das Panel bringt **84 Vorlagen** mit:

- **Handgepflegt und getestet** („stabil“): Minecraft Java, Valheim, Palworld.
- **Handgepflegt** („ungetestet“): Counter-Strike 2, Satisfactory, Project Zomboid, 7 Days to Die, Rust, Garry's Mod, TF2, Minecraft Bedrock, TeamSpeak 3, Factorio, Terraria.
- **Windows-Server über Wine** („experimentell“): Enshrouded, V Rising, Sons of the Forest, Abiotic Factor. Beim ersten Start wird einmalig Wine von WineHQ in eine eigene Laufzeit-Schicht installiert.
- **Steam (Community)**, 64 Spiele: automatisch aus den Daten von [LinuxGSM](https://github.com/GameServerManagers/LinuxGSM) erzeugt (MIT-Lizenz, siehe `internal/templates/games/community/LINUXGSM-LICENSE.md`). Dazu gehören u. a. ARK: Survival Evolved, Counter-Strike 1.6/Source/GO, Left 4 Dead 1 und 2, Insurgency (Sandstorm), Squad, Mordhau, Unturned, Core Keeper, The Front, Soulmask, Sven Co-op und viele Source- und GoldSrc-Mods. Alle „ungetestet“.

Neu erzeugen: `git clone --depth 1 https://github.com/GameServerManagers/LinuxGSM /tmp/lgsm && python3 tools/gen_lgsm.py /tmp/lgsm internal/templates/games/community`. Spiele, die einen Steam-Login brauchen oder deren Ports nur in einer Spielkonfiguration stehen, werden übersprungen.

Vorlagen-Felder seit 0.4: `packages` (zusätzliche Debian-Pakete, eigene Image-Schicht je Paketkombination), `runtime: "wine"` + `platform: "windows"` (Windows-Server über Wine), `log_file` (Spiel schreibt in eine Datei, die Konsole liest mit), `data_path` / `data_uid` / `cmd` für Container-Images.

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

1. Steam-Vorlagen auf echter Hardware prüfen und korrigieren
2. Lizenz-Server + Anbindung an den Shop, Online-Prüfung
5. Mehrere Rechner (Nodes), Mehrbenutzer, Discord-Webhooks
6. KI-Überwachung (Crash-Analyse, Tipps wie „Sichtweite senken“)
