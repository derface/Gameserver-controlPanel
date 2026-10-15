# Bytewise Panel – Gameserver Control Panel

Selbst gehostetes Control Panel für Gameserver mit 3D-Übersicht und KI-Assistent.
Eine einzige Datei, läuft auf jedem Linux mit Docker.

> Technisch heißt alles weiter `gamepanel` (Dienst, Datenordner `/var/lib/gamepanel`, Programmdatei), damit bestehende Installationen beim Update nicht umziehen müssen.

## Was Version 0.1 kann

- **Ersteinrichtung & Login:** Admin-Konto beim ersten Aufruf, Sitzungen per Cookie, Schutz gegen Passwort-Raten.
- **Gameserver per Vorlage:** Minecraft Java und Bedrock, Valheim, Palworld, Counter-Strike 2, Satisfactory, Project Zomboid, 7 Days to Die, Rust, Garry's Mod, TF2 – plus **„Eigenes Steam-Spiel“** für jeden Linux-Dedicated-Server, der anonym über SteamCMD ladbar ist.
- **Jeder Server ein Docker-Container:** eigener Datenordner, RAM-Limit, automatische Portvergabe (zweiter Valheim-Server bekommt 2458/2459 usw.), startet nach einem Neustart des Rechners wieder.
- **Live:** Status (vorbereiten → installieren → starten → läuft), CPU/RAM je Server, Rechner-Auslastung, Live-Konsole mit Befehlseingabe.
- **3D-Server-Landschaft** und **KI-Kern** im freigegebenen Design, Hell/Dunkel, Strg+K-Befehlssuche, Animationen abschaltbar.
- **KI-Assistent:** Anthropic, OpenAI oder Ollama (lokal) mit eigenem Key. Die KI erstellt einen Plan, du gibst ihn frei, dann legt das Panel den Server an.
- **Freie Software:** GNU AGPL-3.0, kostenlos, alle Funktionen ohne Schlüssel.

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
4. **Admin-Konto anlegen**, dann ersten Server erstellen.
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

Das Panel bringt **151 Vorlagen** mit. Der Status kommt aus dem Selbsttest (siehe unten):

- **stabil** (im Spiel erprobt): Minecraft Java, Minecraft Bedrock, Valheim, Palworld.
- **beta** (Selbsttest bestanden): Enshrouded und Abiotic Factor (beide über Wine), Rust, 7 Days to Die, Project Zomboid, Satisfactory, Factorio, Terraria, TeamSpeak 3 sowie 40 Community-Spiele, u. a. ARK: Survival Evolved, CS 1.6, CS: Condition Zero, CS: Source, CS:GO, Left 4 Dead 1 und 2, Day of Defeat, TFC, TF2 Classified, Squad 44, Soulmask, The Front, Mordhau, Pavlov VR, Core Keeper, Tower Unite.
- **experimentell** (noch nicht bestanden oder nach einer Korrektur noch nicht erneut getestet): CS2, TF2, Garry's Mod, V Rising, Sons of the Forest und die übrigen Community-Spiele.
- **defekt**: im Selbsttest abgestürzt, Ursache offen. Diese Vorlagen sind im Panel ausgeblendet und für die KI gesperrt, der Selbsttest prüft sie weiter.

Die Community-Vorlagen werden automatisch aus den Daten von [LinuxGSM](https://github.com/GameServerManagers/LinuxGSM) erzeugt (MIT-Lizenz, siehe `internal/templates/games/community/LINUXGSM-LICENSE.md`), inklusive der Nacharbeiten aus LinuxGSMs `fix_*.sh` (Bibliothekspfade, steamclient.so, GoldSrc-Mods per `app_set_config`, Basis-Apps wie TF2 für TF2 Classified).

Neu erzeugen: `git clone --depth 1 https://github.com/GameServerManagers/LinuxGSM /tmp/lgsm && python3 tools/gen_lgsm.py /tmp/lgsm internal/templates/games/community`. Spiele, die einen Steam-Login brauchen oder deren Ports nur in einer Spielkonfiguration stehen, werden übersprungen.

Weitere 63 Vorlagen (`internal/templates/games/amp`, IDs `amp-…`) werden aus [AMPTemplates](https://github.com/CubeCoders/AMPTemplates) erzeugt: Startzeile, Ports, Einstellungen, Vorbereitungsschritte und Config-Vorlagen, Texte deutsch. Übernommen werden Spiele mit SteamCMD-Download, die unter Linux laufen (nativ oder über Wine) und die das Panel noch nicht hat. Alle sind „experimentell“, bis der Selbsttest sie bestätigt. Neu erzeugen: `git clone --depth 1 https://github.com/CubeCoders/AMPTemplates /tmp/ampt && python3 tools/gen_amp.py /tmp/ampt internal/templates/games internal/templates/games/amp`; was übersprungen wurde und warum, steht in `amp/_bericht.md`. Vorlagenfeld `config[].write_default: true`: Fehlt die Datei vor dem Start, legt das Panel sie aus der Vorlage an (mit eingesetzten Werten und Ports).

Beschreibungen, Wertebereiche und Gruppen der Spiel-Einstellungen stammen teils aus [AMPTemplates](https://github.com/CubeCoders/AMPTemplates) von CubeCoders (MIT-Lizenz, siehe `internal/templates/games/AMPTEMPLATES-LICENSE.md`), übersetzt und angepasst.

### Einstellungs-Seite

Das Panel liest die Config-Dateien eines Spiels und macht aus **jedem** Wert eine Kachel (Reiter, Suche, Schalter/Zahl/Auswahl). Woher die Beschriftung kommt, in dieser Reihenfolge:

1. Einstellungen der Vorlage (`config[].settings`, von Hand gepflegt, deutsch),
2. AMPTemplates-Daten (`internal/templates/amp/meta.json`, erzeugt mit `tools/gen_amp_meta.py`; deutsche Texte in `amp/de.json`),
3. sonst aus dem Schlüssel geraten (`bEnablePvP` → „Enable PvP“, Art aus dem Wert).

Ports, IP-Adressen und Pfade blendet das Panel aus (die verwaltet es selbst). Kennt AMP weitere Config-Dateien eines Spiels und hat das Spiel sie angelegt, erscheinen auch die. Vor jedem Speichern hebt das Panel die alten Dateien auf (`<data>/settings-backup/<id>`, letzte 5), „Frühere Stände“ holt sie zurück. Der Selbsttest prüft nach dem Start jedes Spiels: Kacheln da, Datei bleibt beim Zurückschreiben gleich, nach geändertem Servernamen startet das Spiel noch und behält den Namen.

Felder einer Vorlagen-Einstellung:
`group` (Reiter, z. B. „Server“, „Spieler“), `help` (Beschreibung), `unit` (steht im Feld, z. B. „Spieler“), `min`/`max`, `scale` (Umrechnung: Wert in der Datei = angezeigter Wert × scale, z. B. Minuten ↔ Nanosekunden) und `when` („schlüssel=wert“: wirkt nur dann, sonst ausgegraut mit Hinweis). Typ `list` ist eine kommagetrennte Liste in JSON. Fehlende JSON-Werte legt das Panel beim Speichern mit dem richtigen Typ an. `note_group` zeigt den Hinweis einer Datei nur in einem Reiter. Pro Datei: `auto: false` schaltet die automatischen Kacheln ab, `hide` blendet Schlüssel aus (`*` als Platzhalter, z. B. `userGroups.*.name`), `auto_group` legt den Reiter für automatische Kacheln fest.

Platzhalter seit 0.19.1: `{{memory_mb}}` (RAM-Grenze des Servers) und `{{heap_mb}}` (75 % davon, für Java-Heaps wie bei Project Zomboid). Vorlagen-Felder seit 0.6: `steam_mod` (GoldSrc-Mod über App 90), `steam_base_app` + `steam_base_dir` (zusätzliche App, die vorher installiert wird), `steam_preplatform` (Erstinstallation einmal mit anderer Plattform, z. B. L4D2), Status `defekt`. Seit 0.4: `packages` (zusätzliche Debian-Pakete, eigene Image-Schicht je Paketkombination), `runtime: "wine"` + `platform: "windows"` (Windows-Server über Wine), `log_file` (Spiel schreibt in eine Datei, die Konsole liest mit), `data_path` / `data_uid` / `cmd` für Container-Images.

### Selbsttest

Einstellungen → Selbsttest (oder Strg+K → „Selbsttest“). Das Panel prüft die Vorlagen auf dem eigenen Rechner, eine nach der anderen, kleine Downloads zuerst:

1. Download über SteamCMD bzw. Image laden
2. Start, ohne Absturz oder Neustart durch Docker
3. Bereit-Meldung im Log (falls die Vorlage eine hat)
4. Antwort auf die Spielerabfrage (A2S bzw. Minecraft-Ping)
5. 30 Sekunden stabil, danach wird der Testserver samt Dateien gelöscht

Ergebnis je Spiel: **ok**, **unsicher** (läuft, aber Abfrage fehlt oder ist nicht prüfbar), **fehler** (mit den letzten Logzeilen) oder **übersprungen** (feste Ports belegt, zu wenig Platz oder RAM). Ein Spielekauf ist nicht nötig: Dedicated Server lädt SteamCMD anonym. Der Lauf übersteht Neustarts des Panels und setzt fort; ab 10 GB freiem Platz hält er an. Der Bericht lässt sich als Markdown herunterladen.

## Spiel-Einstellungen

„Server verwalten“ → **Einstellungen** zeigt ein Formular statt Konfigurationsdateien:

- **Start-Einstellungen:** die Variablen der Vorlage (Servername, Passwort, Weltname …). Ändern legt den Container neu an, Spielstände bleiben.
- **Dateieinstellungen:** Werte direkt in den Konfigurationsdateien des Spiels, z. B. Minecraft `server.properties`, Palworld `PalWorldSettings.ini`, Enshrouded `enshrouded_server.json`, 7DTD `serverconfig.xml`, Zomboid-INI, Factorio `server-settings.json`, V Rising, Sons of the Forest, ARK `GameUserSettings.ini` sowie `server.cfg` aller Source- und GoldSrc-Spiele.

Nur geänderte Werte werden geschrieben, alles andere in der Datei (Kommentare, Reihenfolge, unbekannte Schlüssel) bleibt unverändert. Läuft der Server, wird er dafür gestoppt und wieder gestartet, weil viele Spiele ihre Konfiguration beim Beenden überschreiben. Einstellungen, die es in der installierten Spielversion nicht gibt, werden ausgeblendet. Passwörter werden nie an den Browser geschickt.

Vorlagenfeld `config`: Liste von Dateien mit `file` (relativ zum Serverordner, `{{var}}` erlaubt), `format` (`properties`, `ini` mit Schlüsseln `Abschnitt/Name`, `json` mit Punkt-Pfaden, `cvar`, `xmlprop`, `palworld`), optional `init_from` (Vorlage, falls die Datei fehlt) und `settings` (`key`, `label`, `type` text/number/bool/select/secret, `options`/`labels`, `true`/`false` für die Schreibweise in der Datei, `min`/`max`, `help`).

## Steam-Konto

Manche Dedicated Server gibt Steam nicht anonym heraus (Arma 3, DayZ, Action: Source, Blade Symphony). Unter Einstellungen → **Steam-Konto** meldet sich das Panel einmal an. Fragt Steam nach einem Steam-Guard-Code (E-Mail oder Steam-App), erscheint ein Feld dafür; eine Bestätigung in der Steam-App wird abgewartet. Das Passwort wird nie gespeichert und nur bis zum Abschluss der Anmeldung im Speicher gehalten. SteamCMD legt seinen Anmelde-Cache in `<data>/steam-auth` ab; dieser Ordner wird nur in die Container der Spiele eingehängt, die ein Konto brauchen (Vorlagenfeld `steam_login: true`), und die melden sich dann nur mit dem Kontonamen an. Ist die Anmeldung abgelaufen, wartet der Server mit einer Meldung, statt Steam mit Neustarts zu überfluten. Empfehlung: ein eigenes Steam-Konto nur für Server; manche Spiele (DayZ) verlangen, dass das Konto das Spiel besitzt.

Arma 3 und DayZ haben ein Einstellungsformular für `server.cfg` bzw. `serverDZ.cfg` (Format `armacfg`), Arma 3 außerdem eine Mod-Liste (Ordner wie `@CBA_A3`, mit Komma getrennt).

### Spiele mit festen Ports

Killing Floor 2, Eco, Avorion, Necesse, Craftopia, Rising World, Barotrauma, Stationeers und Starbound führen ihre Ports nur in einer Konfigurationsdatei, die sie beim ersten Start selbst anlegen. Das Panel nutzt die Standard-Ports dieser Spiele (`MANUAL_PORTS` im Generator), daher läuft von jedem höchstens ein Server pro Rechner. Euro Truck Simulator 2 und American Truck Simulator fehlen bewusst: ihr Server braucht eine Paketdatei, die man im Spiel selbst exportieren muss.

## Mods und Workshop

- **Minecraft Java:** Reiter **Mods** unter „Server verwalten“. Suche auf Modrinth, gefiltert nach Server-Art (Fabric, Forge, NeoForge, Quilt → Mods; Paper, Purpur → Plugins) und Minecraft-Version, nur Projekte, die auf dem Server laufen. Änderungen werden gesammelt und mit „Übernehmen“ gespeichert; das Image itzg/minecraft-server lädt die Mods samt Pflicht-Abhängigkeiten beim Start und räumt entfernte wieder ab. Vanilla hat keine Mods, dann zeigt der Reiter den Hinweis, die Server-Art umzustellen.
- **Project Zomboid:** Workshop-IDs, Mod-IDs und Karte unter Einstellungen; der Server lädt die Workshop-Inhalte beim Start selbst.
- **Garry's Mod:** Workshop-Sammlung (ID) plus Steam-Web-API-Schlüssel unter Einstellungen → Start-Einstellungen.

Vorlagenfeld `mods`: `{"source": "modrinth", "var": "mods", "type_var": "type", "version_var": "version"}`.

## Aufgaben, Absturz-Wächter, Verlauf

Im Dialog „Server verwalten“ gibt es die Reiter **Aufgaben** und **Verlauf**.

- **Geplante Aufgaben:** Neustart, Update, Backup, Befehl, Starten oder Stoppen, täglich zu einer Uhrzeit, an bestimmten Wochentagen oder alle N Stunden. Neustart, Update und Stopp können die Spieler 1 bis 30 Minuten vorher warnen (Erinnerungen bei 30, 15, 10, 5 und 1 Minute), sofern das Spiel einen Chat-Befehl hat (Vorlagenfeld `broadcast`, z. B. `say {{msg}}`). Verpasste Läufe (Panel war aus) werden nach 30 Minuten übersprungen, damit ein 4-Uhr-Neustart nicht mittags passiert. Jede Aufgabe lässt sich auch sofort ausführen.
- **Bedingungen (seit 0.26):** Neustart, Update und Stopp können
  - *nur laufen, wenn niemand spielt*: Zur geplanten Zeit wartet die Aufgabe, bis die Spielerabfrage 0 meldet, höchstens 30 Min. bis 8 Std., dann läuft sie trotzdem (mit Vorwarnung). Nur bei Spielen mit Spielerabfrage.
  - *nur laufen, wenn es ein Update gibt* (Steam): Die installierte Version steht in `server/steamapps/appmanifest_<App>.acf`, die neueste liefert `app_info_print` in einem kurzen SteamCMD-Container (`gp-steaminfo-…`, Ergebnis 10 Minuten zwischengespeichert). Ohne neue Version wird die Aufgabe mit „übersprungen: kein Update“ vermerkt. Schlägt die Prüfung fehl, läuft die Aufgabe trotzdem.
  - *vorher ein Backup machen*: Schlägt das Backup fehl, wird die Aktion abgebrochen.

  Mit Bedingungen wird erst geprüft und gewartet, dann gewarnt; die Aktion läuft also frühestens zur geplanten Zeit plus Vorwarnung. Unter „Zuletzt“ steht, was passiert ist, z. B. „Update 2045 → 2051 · nach 35 Min. Warten, Server leer · Backup …“. Ein Neustart prüft bei Steam-Spielen immer auf Updates, solange „Beim Start aktualisieren“ an ist; die Liste zeigt das an. Über der Liste stehen passende Vorschläge mit einem Klick (nachts neu starten, Updates einspielen sobald verfügbar, alle 6 Stunden sichern). „Auf Updates prüfen“ im Update-Kasten vergleicht die Versionen sofort.
- **Absturz-Wächter:** Docker startet einen abgestürzten Server neu, das Panel trägt den Absturz in den Verlauf ein. Bei 3 Abstürzen in 10 Minuten hält das Panel den Server an und zeigt den Grund. Optional: Neustart, wenn ein Server, der auf die Spielerabfrage geantwortet hat, 2 bis 30 Minuten lang nicht mehr antwortet. Der automatische Neustart lässt sich pro Server abschalten.
- **Update per Knopf:** Steam-Spiele werden beim nächsten Start per SteamCMD aktualisiert (ein laufender Server startet dafür neu), Container-Spiele laden ihr Image neu.
- **Verlauf:** Anlegen, Starts und Stopps (mit Benutzer), Abstürze, Updates, Aufgaben, Backups und Wiederherstellungen, die letzten 200 Einträge je Server.

## Backups und Dateien

Auf der Server-Karte bzw. in der Detailansicht: **Backups** und **Dateien**.

- **Backups** liegen unter `/var/lib/gamepanel/backups/<server-id>/` als `.tar.gz`. Gesichert werden die Spieldaten, nicht die per SteamCMD geladenen Spieldateien (die lädt das Panel jederzeit neu). Was genau, steht im Backup-Tab („Gesichert werden: …“) und kommt aus `backup_exclude`/`backup_include` der Vorlage.
- Minecraft Java wird im laufenden Betrieb konsistent gesichert (`save-off` / `save-all flush` / `save-on`).
- **Automatisch** alle 6 h, 12 h oder täglich zu einer Uhrzeit. Alte automatische Backups werden nach „Behalten“ gelöscht, manuelle nie.
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

## Benutzer, Zwei-Faktor, Protokoll, HTTPS

Unter **Einstellungen → Benutzer und Rechte** legt ein Admin weitere Konten an:

| Rolle | darf |
|---|---|
| Zuschauer | Server, Konsole, Backup-Liste, Aufgaben und Verlauf ansehen |
| Betreiber | zusätzlich starten, stoppen, Befehle, Update, Backups, Dateien, Spiel-Einstellungen, Mods, Aufgaben |
| Admin | alles: Server anlegen/löschen, Panel-Einstellungen, Steam, Panel-Update, Selbsttest, KI, Benutzer, Protokoll, HTTPS |

Betreiber und Zuschauer lassen sich auf einzelne Server beschränken; andere Server sehen sie gar nicht. Rechteänderungen und Sperren wirken sofort, gesperrte oder gelöschte Konten werden überall abgemeldet. Der letzte aktive Admin kann nicht gesperrt, herabgestuft oder gelöscht werden.

**Zwei-Faktor-Anmeldung** (Einstellungen → Mein Konto): TOTP nach RFC 6238, funktioniert mit Aegis, 2FAS, Google Authenticator, Bitwarden usw. Dazu 10 Wiederherstellungscodes (einmal gültig, gespeichert nur als Hash). Handy weg und keine Codes mehr: ein anderer Admin setzt die Zwei-Faktor-Anmeldung unter „Benutzer“ zurück.

**Protokoll**: jede ändernde Aktion (Server, Backups, Dateien, Befehle mit Text, Einstellungen, Benutzer, HTTPS) und jede An- und Abmeldung, auch fehlgeschlagene, mit Benutzer, IP und Ergebnis. Passwörter und Schlüssel landen nie im Protokoll. Datei `/var/lib/gamepanel/audit.log`, bei 2 MB rotiert (`audit.log.1`).

Sitzungen werden nur als SHA-256 des Cookies gespeichert. Wer `panel.json` in die Finger bekommt, kann damit niemanden übernehmen.

**HTTPS** (Einstellungen → HTTPS): HTTP und HTTPS laufen auf demselben Port. Ist HTTPS an, leitet das Panel http:// auf https:// um.
- *Selbst signiert*: für das Heimnetz. Das Zertifikat gilt für alle IPs des Rechners, seinen Namen und die „Adresse für Spieler“; der Browser warnt einmal.
- *Let's Encrypt*: braucht eine Domain, die auf deine öffentliche IP zeigt, und im Router Port 80 → 80 und 443 → 8420 dieses Rechners. Erneuert sich selbst. Klappt die Prüfung nicht, liefert das Panel solange das selbst signierte Zertifikat aus, statt unerreichbar zu werden.
- *Eigenes Zertifikat*: PEM mit Kette plus Schlüssel einfügen.

**Hinter einem Reverse Proxy** (Traefik, nginx, Caddy): Unter Einstellungen → HTTPS → „Über einen Reverse Proxy“ die Adresse des Proxys eintragen (IP oder Netz). Nur von dort glaubt das Panel `X-Forwarded-For` (echte Besucher-IP für Protokoll, Sitzungen und die Sperre nach falschen Passwörtern) und `X-Forwarded-Proto` (keine zweite Umleitung auf HTTPS, Cookies mit `Secure`). Kommen Proxy-Angaben von einer Adresse, die nicht eingetragen ist, schlägt das Panel sie dort zum Übernehmen vor. Den Proxy am besten per HTTPS ans Panel anbinden (das Panel spricht auf seinem Port immer auch TLS, notfalls mit dem selbst signierten Zertifikat), Beispiel Traefik:

```yaml
http:
  routers:
    gamepanel:
      entryPoints: [websecure]
      rule: "Host(`panel.example.com`)"
      service: gamepanel
      tls: {certResolver: letsencrypt}
  services:
    gamepanel:
      loadBalancer:
        servers: [{url: "https://192.168.1.20:8420"}]
        serversTransport: gamepanel-tls
  serversTransports:
    gamepanel-tls:
      insecureSkipVerify: true
```

Ausgesperrt? Vom Rechner selbst (`http://localhost:8420`, z. B. per `ssh -L 8420:localhost:8420 admin@192.168.178.54`) wird nie umgeleitet. Oder `Environment=GP_NO_TLS=1` in `/etc/systemd/system/gamepanel.service` eintragen, `systemctl daemon-reload && systemctl restart gamepanel`.

## Automatische Portfreigabe (UPnP)

Unter **Einstellungen → Router & Ports** einschalten. Solange ein Server läuft, öffnet das Panel seine Ports im Router; beim Stoppen oder Löschen schließt es sie wieder. Es findet den Router per SSDP und nutzt UPnP-IGD (WANIPConnection/WANPPPConnection), dieselbe Technik wie Konsolen und Steam.

- Fritzbox: *Heimnetz → Netzwerk → Netzwerkeinstellungen* „Statusinformationen über UPnP übertragen“ an, und beim Gerät des Panel-Rechners „Selbstständige Portfreigaben erlauben“.
- Entfernt werden nur Freigaben, die das Panel selbst angelegt hat (gemerkt in `panel.json`). Von Hand angelegte Freigaben zu diesem Rechner werden erkannt und nie angefasst; Ports, die an ein anderes Gerät gehen, meldet das Panel als Konflikt.
- Einzelne Server lassen sich auf ihrer Seite im Menü „⋯“ ausnehmen.
- Abgeglichen wird bei Start/Stopp und alle 2 Minuten, also auch nach einem Router-Neustart. Router, die keine dauerhaften Freigaben erlauben, bekommen eine Stunde und werden erneuert.
- Verwaltungszugänge (RCON, Telnet, Web-Admin, TeamSpeak-ServerQuery …) werden nie freigegeben.

## Von außen erreichbar?

Auf der Server-Seite unter *Details* → **Von außen erreichbar? Prüfen**. Das Panel prüft Schritt für Schritt und sagt, wo es hakt:

1. Läuft der Server?
2. Hat der Anschluss eine eigene öffentliche IPv4? (Router-WAN-Adresse gegen die Adresse, die das Internet sieht; erkennt DS-Lite/CGNAT und doppelte Router)
3. Zeigt die Spieler-Adresse (z. B. `play.example.com`) im öffentlichen DNS auf diese IP? Gefragt werden 1.1.1.1/9.9.9.9/8.8.8.8, nicht der lokale DNS (AdGuard, Fritzbox).
4. Je Port: Ist er im Router an diesen Rechner freigegeben (per UPnP gelesen)?
5. Je TCP-Port: echte Verbindung aus dem Ausland über check-host.net. Minecraft Bedrock: Ping von außen über mcsrvstat.us. Reine UDP-Spiele lassen sich ohne Spiel-Client nicht neutral von außen testen; dann zählt, ob das Spiel über die öffentliche IP antwortet.

Die Prüfung läuft nur auf Knopfdruck. Die Dienste (api.ipify.org, check-host.net, mcsrvstat.us) sehen dabei nur die öffentliche IP und den Port.

## Installierte Mods

Reiter **„Mods“** bei jedem Server: „Installiert und geladen“ listet, was auf der Platte liegt:
- Steam-Workshop-Inhalte, mit Titel und Update-Hinweis über Steams öffentliche Schnittstelle (`GetPublishedFileDetails`, ohne Schlüssel)
- Project-Zomboid-Mods (`mod.info`, Build 42 bevorzugt)
- BepInEx-Plugins (`manifest.json` von Thunderstore)
- Unreal-Pak-Mods (`~mods`, `LogicMods`)
- UE4SS-Mods (`mods.txt`)
- Minecraft-Jars
- Mod-IDs in Startwerten

Dazu gleicht das Panel die Liste des Spiels ab (Zomboid `Mods=` und `WorkshopItems=`: „fehlt“, „nicht heruntergeladen“, „aus“) und wertet die Konsole seit dem letzten Start aus: Mod-Name oder -ID zusammen mit einer Fehlermeldung ergibt „Fehler“ samt Zeile, mit Lade-Wörtern „geladen“. Probleme stehen oben.

Ein- und Ausschalten: Bei Zomboid geht das über die Mod-Liste in der Server-INI, gespeichert wie die übrigen Einstellungen (ein laufender Server wird gestoppt und neu gestartet). BepInEx-, Pak- und UE4SS-Mods werden durch Umbenennen (`.off`) bzw. über `mods.txt` geschaltet und wirken ab dem nächsten Start. Dateien werden nie gelöscht.

### Steam Workshop (Project Zomboid)

Im Reiter „Mods“ steht bei Vorlagen mit `workshop` (`{"app": 108600, "build42_tag": "Build 42"}`) eine Workshop-Suche:
- **Suche:** Mit einem Steam-Web-API-Schlüssel (Einstellungen → Steam-Konto, optional) läuft sie über `IPublishedFileService/QueryFiles`, sonst über die öffentliche Workshop-Seite. Titel, Bild, Abos und Beschreibung kommen aus `GetPublishedFileDetails`.
- **Filter:** „Nur Build 42“ filtert nach dem Tag und ist eingeschaltet, wenn die Mod-Liste im Build-42-Format (`\ModID`) ist.
- **Installieren:** Die Workshop-ID kommt nach `WorkshopItems=`, die Mod-IDs aus der Beschreibung („Mod ID: …“) nach `Mods=`, Kartenordner („Map Folder: …“) vor die Standardkarte in `Map=`. Den Download erledigt der Server beim nächsten Start.
- **Entfernen:** Nimmt die Mod aus beiden Listen. Die Dateien bleiben.

### Steam Workshop für alle Steam-Spiele

Ab 0.24 gibt es die Workshop-Suche bei jedem Steam-Spiel, dessen Workshop Steam meldet. Das Panel sucht dafür die App des Spiels (nicht die des Dedicated Servers): erst `SteamAppId=` im Startbefehl, dann die Store-Suche nach dem Namen, und prüft in `store.steampowered.com/api/appdetails` die Kategorie 30 („Steam Workshop“). Das Ergebnis liegt 30 Tage in `<data>/steam-apps.json`; war Steam nicht erreichbar, wird nichts gemerkt.

- **Herunterladen:** Die ID kommt in `<server>/.gp-workshop` (`app <id>` und eine ID je Zeile). Vor dem Start lädt gp-run die Liste mit `workshop_download_item` (bei Installation, Update oder wenn die Liste neuer ist als der letzte Lauf) und schreibt je ID `ok` oder `fehler <Meldung>` nach `.gp-workshop-status`. Die Mod-Übersicht zeigt das an („Steam-Konto nötig“ bei Access Denied).
- **Einbinden:** Für einige Spiele ist hinterlegt, wie sie Mods laden (`internal/templates/recipes.go`): Arma 3 und DayZ (`@ws<ID>`, Schlüssel nach `keys/`, `-mod=`), Starbound (`.pak` nach `mods/`), Garry's Mod (`.gma` nach `garrysmod/addons`, `resource.AddWorkshop`), Conan Exiles (`ConanSandbox/Mods/modlist.txt`). Das Skript liegt als `.gp-workshop-after.sh` im Server-Ordner und läuft vor jedem Start. Dann heißt der Knopf „Installieren“.
- **Ohne Rezept:** Die Mod wird nur heruntergeladen. In der Mod-Übersicht steht „KI fragen“: Die KI bekommt Spiel, Startbefehl, Titel, Beschreibung und die Dateiliste der Mod und antwortet mit Schritten.
- **Aus/Entfernen:** Der Schalter bzw. „Entfernen“ nimmt die ID aus der Liste; die Dateien bleiben.

Eigene Vorlagen können das über `"workshop": {"app": 107410, "after": "<bash>", "note": "…"}` selbst festlegen.

## KI-Assistent: vorhandene Server ändern

Der KI-Assistent plant nicht nur neue Server, er ändert auch vorhandene („den Enshrouded-Server als Mid/Highrate“, „mehr Loot“, „schwerer“). Er bekommt dafür den bisherigen Gesprächsverlauf (letzte 12 Nachrichten) und, wenn eine Nachricht einen Server oder ein Spiel nennt, dessen Einstellungen als Liste: Schlüssel, Beschriftung, aktueller Wert, Typ und Bereich, Bedingungen wie „wirkt nur wenn gameSettingsPreset=Custom“. Geheime Werte gehen nicht mit. Die Antwort (`action: change`) prüft das Panel mit denselben Regeln wie das Einstellungsformular: Unbekannte Schlüssel und ungültige Auswahlen fallen weg, Zahlen außerhalb des Spielbereichs werden begrenzt (mit Hinweis). Angezeigt wird eine Tabelle „Bisher → Neu“; erst „Übernehmen“ schreibt die Werte (ein laufender Server startet dafür neu).

## KI-Diagnose

Server-Seite → Details → **„Mit KI prüfen“** (ab Rolle Betreiber). Das Panel sammelt Fakten (RAM-/CPU-Grenze, Auslastung der letzten sechs Stunden als Minutenwerte, Laufzeit, Spieler, Exit-Code, freier RAM und Plattenplatz des Rechners, andere Server), den Verlauf, einen Konsolenauszug (Fehler- und Warnzeilen dedupliziert plus die letzten 40 Zeilen, ohne Rauschen) und die Treffer eigener Regeln („Killed“/OOM, Speicherfehler, Segfault, Port belegt, fehlende Bibliothek, Steam-Fehler, RAM/CPU dauerhaft voll, Abstürze und Hänger der letzten 24 Stunden, Platte fast voll). Passwörter, IP-Adressen, Steam-IDs, E-Mail-Adressen und lange Schlüssel werden vorher entfernt.

Die KI antwortet mit Schweregrad, Zusammenfassung, Befunden mit Beleg und bis zu vier Vorschlägen. Ausführbar per Klick (mit Rückfrage): RAM ändern (auf 256 MB gerundet, höchstens 90 % des Rechners), Startwert ändern (nur vorhandene, nicht geheime Variablen mit erlaubtem Wert), Neustart, Update. Alles andere ist ein Hinweis. Die letzten fünf Diagnosen je Server liegen in `<data>/diagnose/<id>.json`.

Einstellungen → Allgemein & KI → **„Bei Problemen automatisch mit KI prüfen“**: nach einem Absturz, einem Hänger-Neustart oder wenn RAM (über 92 %) bzw. CPU (über 95 %) 15 Minuten am Stück voll sind. Höchstens einmal in sechs Stunden je Server und zwölfmal am Tag. Das Ergebnis steht im Verlauf. Ein Ende mit Exit-Code 137 ohne Zutun des Panels zählt seit 0.21 als Absturz („vom System beendet, oft zu wenig RAM“).

## Umzug von AMP

Server anlegen → **„Server aus AMP übernehmen“**. Das Panel zeigt einen Befehl mit einem Einmal-Schlüssel (zwei Stunden gültig), der als root auf dem AMP-Rechner läuft:

    curl -fsSk https://<panel>/import/amp.py | python3 - --panel https://<panel> --token <schlüssel> [instanzen …]

Das Skript (`internal/manager/ampexport.py`, nur Python-Standardbibliothek) packt pro AMP-Instanz `GenericModule.kvp` (Einstellungen), die Config-Dateien aus AMPs `metaconfig.json` und die Spielstände (Unreal-Ordner `Saved` außer `Engine/Saved`, `Zomboid`, `savegame` usw., Ordner aus den Instanz-Einstellungen wie `saveDirectory`), ohne Logs, Absturzberichte und `.trash`, und schickt sie ans Panel. Spieldateien werden nicht übertragen. Laufende Instanzen werden übersprungen. In AMP wird nichts verändert.

Im Panel erscheint jede Instanz mit der erkannten Vorlage (über die Steam-App-ID, eigene Vorlagen vor erzeugten). „Übernehmen“ legt den Server an: Instanz-Einstellungen werden auf die Variablen der Vorlage abgebildet (Name ohne Sonderzeichen gleich, `$MaxUsers` → `max_players`), Dateien landen unter `server/`. Vorlagenfeld `amp_import` für Abweichungen: `map` (z. B. Zomboid `{"Zomboid/": "home/Zomboid/"}`), `vars_from_files` (Wert aus einem Dateinamen, z. B. der Zomboid-Servername) und feste `vars` (Icarus setzt die letzte Prospect fort). Empfangene Archive liegen bis zur Übernahme unter `<data>/import/amp`.

## Spielerverwaltung

Reiter **Spieler** auf jeder Server-Seite: wer online ist (mit Spielzeit), dazu Rauswerfen, Bannen, Whitelist und Admins, soweit das Spiel es kann. Ab Rolle Betreiber; Zuschauer sehen nur die Liste.

| Spiel | Online-Liste | Aktionen | Listen |
|---|---|---|---|
| Minecraft Java | Konsole `list` (rcon-cli) | kick, ban, pardon, whitelist, op/deop | whitelist.json, ops.json, banned-players.json (Änderungen per Konsole) |
| Project Zomboid | Konsole `players` | kickuser, banuser, unbanuser, setaccesslevel, Whitelist | in der Spieldatenbank |
| Valheim | Steam-Abfrage | – | `saves/adminlist.txt`, `bannedlist.txt`, `permittedlist.txt` (Steam-IDs, bearbeitet das Panel direkt) |
| Factorio | Konsole `/players online` | /kick, /ban, /unban, /promote, /demote, /whitelist | – |
| Terraria | Konsole `playing` | kick, ban | – |
| Source-Spiele (CS2, TF2, GMod, …) | Steam-Abfrage | kick | – |
| übrige Steam-Spiele | Steam-Abfrage (A2S_PLAYER), sofern das Spiel Namen meldet | – | – |

Bei Spielen mit Konsole über stdin liest das Panel die Antwort aus dem Log direkt nach dem Befehl. Namen und IDs werden streng geprüft (keine Anführungszeichen, Semikolons usw.), jede Aktion steht im Verlauf und im Protokoll. Neue Spiele kommen als Profil in `internal/manager/players.go` dazu.

## Benachrichtigungen

Einstellungen → **Benachrichtigungen**: Discord-Webhook und/oder ntfy-Thema (auch eigener ntfy-Server, optional mit Zugangstoken). Auswählbar: Abstürze und Hänger, Fehler (Aufgaben, Backups, externe Kopien, Installation), Spiel-Updates, ausgeführte Aufgaben, erstellte Backups, Start/Stopp durch Benutzer, KI-Diagnosen, neue Panel-Version. Dieselbe Meldung kommt je Server höchstens alle 10 Minuten. „Testnachricht senden“ prüft die Einstellung. Webhook-URL und Token werden in der Oberfläche nur gekürzt angezeigt.

## Panel-Update

Einstellungen → **Panel & Updates**: Das Panel fragt GitHub kurz nach dem Start und dann alle 6 Stunden nach dem neuesten Release (`GP_REPO`, Standard `derface/Gameserver-controlPanel`; für private Repos `GP_GITHUB_TOKEN`). Ist eine neuere Version da, zeigt die Seitenleiste „Update da“ und auf Wunsch kommt eine Benachrichtigung. „Aktualisieren“ lädt `gamepanel-linux-<arch>` aus dem Release, prüft es gegen `SHA256SUMS` (ohne Prüfsumme wird nichts installiert), legt die alte Datei als `gamepanel.old` daneben und startet das Panel im selben Prozess neu. Spieleserver laufen weiter. Releases baut `.github/workflows/release.yml` bei jedem Tag `v*` (Programmdateien, `install.sh`, `SHA256SUMS`). Zurück zur alten Version: `mv /opt/gamepanel/gamepanel.old /opt/gamepanel/gamepanel && systemctl restart gamepanel`.

## Events (zeitlich begrenzt)

Werte im Reiter **Einstellungen** ändern und **Als Event planen** wählen: Name, Beginn, Ende, auf Wunsch jede Woche. Gespeichert wird dabei nichts dauerhaft. Zum Beginn merkt sich das Panel die alten Werte, setzt die neuen und startet den Server neu; zum Ende stellt es alles zurück. Spieler werden 10 und 1 Minute vorher im Spiel gewarnt (wenn das Spiel Nachrichten kann), die Ankündigung geht an Discord/ntfy (Art „Events“). Die Liste steht im Reiter **Aufgaben** unter „Events“; ein laufendes Event lässt sich dort sofort beenden.

Die KI versteht das auch: „Mach am Wochenende ein Highrate-Event auf Enshrouded“. Sie schlägt Zeitraum und Werte vor, angelegt wird erst nach „Event anlegen“. Grenzen: höchstens 14 Tage, wöchentlich höchstens 6 Tage, 20 Events pro Server.

## Statusseite für Mitspieler

**Einstellungen → Statusseite**: eine Seite ohne Anmeldung unter `/status` mit den ausgewählten Servern, wer spielt, Adresse, Beitreten-Knopf (Steam-Spiele) und einem Hinweis pro Server (z. B. „Passwort im Discord“). Passwörter oder Einstellungen stehen dort nie. Mit einer eigenen Adresse (z. B. `status.bytewise-it.de`, per Reverse Proxy auf das Panel geleitet) zeigt das Panel unter dieser Domain nur die Statusseite; Anmeldung und API sind dort nicht erreichbar.

## Discord-Bot

**Einstellungen → Discord-Bot**: eigener Bot ohne Dauerverbindung. Discord schickt die Befehle signiert an `https://<panel>/api/discord/interactions` (Ed25519-geprüft), das Panel antwortet direkt.

- `/status` (jeder), `/start`, `/stop`, `/restart` (nur freigegebene Rollen oder Personen), mit Server-Auswahl beim Tippen.
- Eine Statusnachricht im gewählten Kanal, die sich selbst aktualisiert (bei Änderungen, sonst alle 15 Minuten).
- Nur die angehakten Server sind für den Bot sichtbar und steuerbar.
- Die Anleitung zum Anlegen der Discord-Application steht aufklappbar im Panel. Das Panel muss dafür von außen per HTTPS erreichbar sein.

## Spieler-Statistik

Das Panel zählt jede Minute die Spieler laufender Server (Steam-Abfrage oder Konsole) und hebt 90 Tage auf. Im Reiter **Spieler** steht eine Wochen-Heatmap, die Spielzeit pro Spieler (wo das Spiel Namen liefert) und Hinweise: die ruhigste Stunde für Neustarts, geplante Neustarts, die oft Spieler treffen, und Server, auf denen seit 14 Tagen niemand gespielt hat. Die Hinweise aller Server stehen auch auf der Übersicht.

## Herkunftskarte

Im Reiter **Spieler** unter „Herkunft“ (einmal von einem Admin einschalten): eine Weltkarte, aus welchen Ländern und Städten Spieler verbinden, für einen Server oder alle zusammen, mit „Jetzt verbunden“ und einer Länderliste.

- Viele Steam-Spiele (z. B. Enshrouded) bauen die Spielverbindung vom Server aus auf, zum Spieler oder zu einem Valve-Relay, über zufällige Ports. Das Panel erkennt auch diese an der Adresse des Server-Containers.
- Das Panel liest alle 20 Sekunden die Verbindungstabelle des Kernels (`/proc/net/nf_conntrack` oder das Programm `conntrack`, das `install.sh` mitinstalliert) und ordnet Verbindungen den Spiel-Ports laufender Server zu. Gezählt wird eine Verbindung erst, wenn sie über eine Minute lang Daten schickt; Server-Browser-Abfragen und Port-Scanner fallen so heraus. Meldet das Spiel 0 Spieler, zählt nichts.
- Den Ort schlägt das Panel lokal in der freien Datenbank **DB-IP Lite** nach (CC BY 4.0, wird beim Einschalten und dann monatlich geladen, gut 100 MB in `<data>/geo/`). Wer lieber MaxMind GeoLite2 nutzt, legt die Datei als `<data>/geo/custom.mmdb` ab.
- Gespeichert werden nur Land, Stadt, Anzahl Besuche und Spielzeit, keine IP-Adressen. Wer innerhalb von 30 Minuten neu verbindet, zählt als derselbe Besuch.
- Verbindungen aus dem eigenen Netz zählen als „Heimnetz“. Spiele, die über Valves Steam Datagram Relay laufen (z. B. teils Enshrouded, Palworld, Valheim mit Crossplay), zeigen nur einen Valve-Server; diese zählt das Panel getrennt als „über Steam-Relay“.
- Die Orte sind ungefähr, oft die Stadt des Internetanbieters.

- Admins sehen unter „Was das Panel gerade sieht“ die erkannten Verbindungen der letzten Abfrage, zur Fehlersuche ohne Kommandozeile.

## Leistungsverlauf

Das Panel schreibt jede Minute CPU, RAM, Spielerzahl und Zustand jedes Servers mit (Dateien pro Tag unter `<data>/history/`, 30 Tage). Im Reiter **Verlauf** stehen drei Diagramme für 24 Stunden, 7 oder 30 Tage mit gemeinsamem Fadenkreuz, graue Flächen für „Server war aus“, rote Striche für Fehler. Darüber Hinweise, etwa wenn der RAM lange über 90 % voll war oder die CPU mit Spielern am Limit läuft. Die Auslastung des ganzen Rechners steht unter Einstellungen → Speicher & Auslastung.

## Speicherplatz

**Einstellungen → Speicher & Auslastung**: freier Platz, was jeder Server belegt (Spiel und Welt, Backups), Reste gelöschter Server zum Wegräumen und Docker-Images, die kein Server mehr nutzt, sowie Bau-Zwischenstände, beides mit einem Klick löschbar. Wird der Platz knapp (unter 10 % oder 10 GB), meldet das Panel das über die Benachrichtigungen.

## Wartungsmodus

Im Server-Menü „⋯“ → **Wartungsmodus**: mit Ende (oder offen) und Hinweis. Solange er läuft, pausieren geplante Aufgaben, Events und der Absturz-Wächter, Statusseite und Discord zeigen „Wartung“, Discord-Befehle für den Server sind gesperrt, und Spieler im Spiel bekommen eine Nachricht. Auf Wunsch schließt das Panel die Ports im Router, sodass nur noch das Heimnetz herankommt.

## Einladungslink

Im Server-Menü „⋯“ → **Einladungslink**: eine Seite ohne Anmeldung mit Adresse, Anleitung zum Beitreten, deinem Hinweis und QR-Code. Wer den Link hat, kann sie öffnen; ein neuer Link macht den alten ungültig. Mit eigener Statusseiten-Domain zeigt der Link auf diese Domain.

## Kalender-Abo

Unter Einstellungen → Statusseite lässt sich ein Kalender-Link erzeugen (iCalendar). Er enthält die geplanten Events und Wartungen der angezeigten Server, wöchentliche Events als Serie, und aktualisiert sich stündlich. Die Statusseite zeigt „Kommende Events“ und einen Knopf zum Abonnieren, außerdem pro Server den Online-Verlauf der letzten 30 Tage.

## Mod-Paket für Mitspieler

Statusseite und Einladungslinks zeigen pro Server die Mods, die Spieler selbst brauchen:
- **Minecraft:** Die Jars in `mods/` werden über ihre Prüfsumme bei Modrinth erkannt. Reine Server-Mods lässt die Liste weg, optionale sind markiert.
- **Valheim:** BepInEx-Plugins.
- **Steam Workshop:** Links zum Abonnieren.

Unter Einstellungen → Statusseite lässt sich zusätzlich der Download als ZIP erlauben (höchstens zwei Downloads gleichzeitig).

## Mod-Updates

Alle 6 Stunden prüft das Panel Workshop-Mods (Steam) und Minecraft-Mods/-Plugins (Modrinth, passend zu Loader und Minecraft-Version) auf neue Versionen. Neue Updates meldet es einmal über die Benachrichtigungen. Der Reiter Mods zeigt sie mit „Jetzt prüfen“. Eine Neustart- oder Update-Aufgabe mit „Nur wenn es ein Update gibt (Spiel oder Mods)“ spielt sie nachts ein. Mods aus der Modrinth-Liste des Servers lädt der Minecraft-Container beim Start selbst in der neuen Version.

## Whitelist über Discord

Unter Einstellungen → Discord-Bot: eine Rolle und die Server mit Whitelist (z. B. Minecraft) wählen. Mitglieder mit der Rolle tragen sich mit `/whitelist spielername:Name` ein. Ein Name gehört genau einem Discord-Konto, ein neuer Name ersetzt den alten. Alle 30 Minuten prüft das Panel die Rollen: Wer die Rolle verliert oder den Discord-Server verlässt, fliegt von der Whitelist. Gestoppte Server bekommen die Änderungen beim nächsten Start. Die Liste der Verknüpfungen steht in den Einstellungen und lässt sich dort bereinigen.

## Spielerliste auf der Statusseite

Auf Wunsch (Einstellungen → Statusseite) zeigt die Statusseite die Spieler aller angezeigten Server: Spielzeit, Lieblingsserver, dabei seit, zuletzt online. Das funktioniert nur bei Spielen, die Namen verraten.

## Saisons

Im Reiter Backups → **Neue Saison** wird die aktuelle Welt als Saison-Archiv gesichert. Dieses Backup wird nie automatisch gelöscht. Danach werden nur die gewählten Ordner zurückgesetzt; das Panel schlägt die Spielstand-Ordner selbst vor, Einstellungen und Mods bleiben. Zur Sicherheit muss man den Servernamen eintippen. Mit Termin warnt das Panel die Spieler 60, 10 und 1 Minute vorher. War das Panel zum Termin mehr als 6 Stunden aus, fällt die Saison aus, statt überraschend zu löschen. Die Ruhmeshalle mit den Top 3 jeder Saison steht im Reiter Backups und auf der Statusseite, und die Welt einer alten Saison lässt sich zurückholen.

## Lizenz

Bytewise Panel ist freie Software unter der **GNU Affero General Public License v3.0** (`LICENSE`). Du darfst es kostenlos nutzen, verändern und weitergeben. Wer eine veränderte Fassung anderen über das Netz anbietet, muss deren Quellcode ebenfalls offenlegen (AGPL §13); das Panel verlinkt dazu unter Einstellungen → Panel & Updates auf den Quellcode. Fremdbestandteile: `internal/thirdparty/acme` (BSD), `web/static/vendor/qrcode.js` und `three.js` (MIT), Vorlagen-Daten aus LinuxGSM und AMPTemplates (MIT).

## Stand & Grenzen von 0.1

- Getestet: kompletter Ablauf mit echtem Docker (anlegen, starten, Konsole, Befehle, stoppen, RAM ändern, zweite Instanz mit neuem Port, löschen), Login.
- Auf echter Hardware geprüft (Debian 13, Docker 29): Minecraft Java (Beitritt mit Client), Valheim (SteamCMD-Download, Start). Die übrigen Steam-Vorlagen (Status „Beta“) sind noch ungetestet.
- Spielerzahlen: Minecraft Java (Server-List-Ping) sowie Valheim, CS2, GMod, TF2, 7DTD (Steam A2S). Übrige Spiele zeigen CPU statt Spieler.
- Das Panel läuft als root (braucht Docker-Zugriff). Ins Internet nur mit HTTPS (eingebaut oder per Traefik) und Zwei-Faktor-Anmeldung.
- API-Keys liegen im Klartext in `panel.json` (nur für root lesbar). Verschlüsselung folgt.

## Nächste Schritte

1. Selbsttest über alle Vorlagen laufen lassen, Fehler korrigieren, Status anpassen
5. Mehrere Rechner (Nodes)
6. KI-Überwachung (Crash-Analyse, Tipps wie „Sichtweite senken“)

## English / Sprache

The panel is also available in English. It follows the browser language; under **Settings → My account → Language** (or the link below the sign-in form) you can pick German, English or automatic. The status page and invite links switch too, also via `?lang=en`.

Wie es funktioniert: Das Panel ist auf Deutsch geschrieben. `web/static/i18n.js` übersetzt die Seite beim Anzeigen mit einem Wörterbuch (`web/static/i18n/en.json`): ganze Texte, Muster für Texte mit Zahlen und Namen, bekannte Wendungen in längeren Texten. Texte aus den Spielvorlagen (Einstellungen, Hinweise) tauscht der Server aus, wenn der Browser Englisch anfragt (`X-Lang: en`); die AMP-Texte haben dafür ohnehin ihr englisches Original, die übrigen stehen in `internal/templates/en.json`. Konsole, Dateiinhalte und Namen bleiben wie sie sind. Was schon im Verlauf steht, bleibt in der Sprache, in der es geschrieben wurde; Nachrichten an Discord und im Spiel bleiben Deutsch.

Übersetzungen pflegen: Quellen in `tools/i18n/en/*.json` (Deutsch → Englisch), danach `python3 tools/i18n/build.py`. Neue Texte aus dem Go-Code listet `go run ./tools/i18n/gostrings ./internal`.
