/* Hilfe im Panel: kurze Artikel zu häufigen Fragen und Fehlern, auf Deutsch
 * und Englisch. `match` erkennt Fehlermeldungen, zu denen ein Artikel passt.
 * Help articles in German and English; `match` finds the article for an
 * error message. */
window.GP_HELP = [
  {
    id: 'start', tags: 'erste schritte anfang einrichtung getting started setup',
    de: { t: 'Erste Schritte', b: `
<p>So kommst du zum ersten laufenden Server:</p>
<ol>
<li><b>Server anlegen:</b> Oben rechts „Server anlegen“, Spiel wählen, Namen und RAM festlegen. Das Panel lädt das Spiel herunter und startet es. Alternativ „Mit KI planen“ oder unter „Server anlegen“ → „Server aus AMP übernehmen“.</li>
<li><b>Adresse für Spieler:</b> Unter Einstellungen → Allgemein &amp; KI die Domain oder öffentliche IP eintragen, die Mitspieler eingeben sollen.</li>
<li><b>Ports öffnen:</b> Entweder automatisch per UPnP (Einstellungen → Router &amp; Ports) oder von Hand im Router. Welche Ports ein Server braucht, steht in seinen Details.</li>
<li><b>Prüfen:</b> In den Server-Details „Von außen erreichbar? Prüfen“.</li>
</ol>
<p>Den Einrichtungsassistenten kannst du jederzeit unter Einstellungen → Panel &amp; Updates erneut starten.</p>` },
    en: { t: 'Getting started', b: `
<p>How to get your first server running:</p>
<ol>
<li><b>Create a server:</b> “Create server” at the top right, pick a game, name and RAM. The panel downloads and starts it. Or use “Plan with AI”, or import from AMP in the create dialog.</li>
<li><b>Address for players:</b> Under Settings → General &amp; AI enter the domain or public IP your friends should use.</li>
<li><b>Open ports:</b> Automatically via UPnP (Settings → Router &amp; ports) or by hand in your router. The ports a server needs are listed in its details.</li>
<li><b>Check:</b> In the server details, “Reachable from outside? Check”.</li>
</ol>
<p>You can run the setup wizard again any time under Settings → Panel &amp; updates.</p>` }
  },
  {
    id: 'reach', tags: 'erreichbar beitreten verbinden ports router freigabe cgnat ds-lite join connect reachable',
    match: /von außen keine Antwort|nicht offen|not reachable from outside|Ports nicht offen/i,
    de: { t: 'Mitspieler kommen nicht auf den Server', b: `
<p>Geh der Reihe nach vor:</p>
<ol>
<li><b>Läuft der Server wirklich?</b> Status „Läuft“ und in der Konsole keine Fehler. Manche Spiele brauchen nach dem Start noch 1–3 Minuten.</li>
<li><b>Richtiger Port?</b> Mitspieler brauchen den Port, der in den Details unter „Adresse für Spieler“ steht. Bei manchen Spielen (z. B. Enshrouded) ist das der Abfrage-Port, nicht der Spiel-Port.</li>
<li><b>Router-Freigabe:</b> Alle Ports aus den Details müssen im Router an diesen Rechner weitergeleitet sein (UDP/TCP beachten). Mit UPnP erledigt das Panel das selbst.</li>
<li><b>Eigene öffentliche IPv4?</b> Viele Anschlüsse (DS-Lite, CGNAT) haben keine. Dann kommt über IPv4 niemand rein. Der Test „Von außen erreichbar?“ erkennt das. Abhilfe: beim Anbieter eine öffentliche IPv4 anfragen.</li>
<li><b>Domain:</b> Zeigt die Domain im Internet auf deine öffentliche IP (DynDNS)?</li>
</ol>
<p>Von zu Hause aus klappt es nicht, von außen schon? Siehe „Von zu Hause klappt die Domain nicht“.</p>` },
    en: { t: 'Players cannot join', b: `
<p>Go through this in order:</p>
<ol>
<li><b>Is the server really running?</b> State “Running” and no errors in the console. Some games need 1–3 minutes after starting.</li>
<li><b>Right port?</b> Players need the port shown in the details under “Address for players”. For some games (e.g. Enshrouded) that is the query port, not the game port.</li>
<li><b>Port forwarding:</b> All ports from the details must be forwarded to this machine in your router (mind UDP/TCP). With UPnP the panel does this itself.</li>
<li><b>Own public IPv4?</b> Many connections (DS-Lite, CGNAT) have none, so nobody gets in over IPv4. The “Reachable from outside?” check detects this. Ask your provider for a public IPv4.</li>
<li><b>Domain:</b> Does your domain point to your public IP on the internet (dynamic DNS)?</li>
</ol>
<p>Works from outside but not from home? See “The domain does not work from home”.</p>` }
  },
  {
    id: 'loopback', tags: 'heimnetz zu hause domain nat loopback fritzbox adguard pihole home lan',
    de: { t: 'Von zu Hause klappt die Domain nicht', b: `
<p>Viele Router (z. B. die FritzBox) leiten Anfragen aus dem Heimnetz an die eigene öffentliche Adresse nicht zurück ins Heimnetz („NAT-Loopback“). Freunde von außen kommen rein, du von zu Hause mit derselben Domain nicht.</p>
<ul>
<li><b>Einfach:</b> Zu Hause die Heimnetz-Adresse benutzen. Die Statusseite und Einladungslinks zeigen sie Besuchern aus dem Heimnetz automatisch an.</li>
<li><b>Elegant:</b> Im eigenen DNS (AdGuard Home, Pi-hole, FritzBox-DNS) die Domain auf die Heimnetz-IP dieses Rechners umschreiben. Dann klappt dieselbe Adresse überall.</li>
</ul>` },
    en: { t: 'The domain does not work from home', b: `
<p>Many routers (e.g. the FritzBox) do not send requests from your home network to your own public address back into the home network (“NAT loopback”). Friends outside get in, you at home with the same domain do not.</p>
<ul>
<li><b>Simple:</b> Use the home network address at home. The status page and invite links show it to visitors from your home network automatically.</li>
<li><b>Neat:</b> In your own DNS (AdGuard Home, Pi-hole, FritzBox DNS) rewrite the domain to this machine’s LAN IP. Then the same address works everywhere.</li>
</ul>` }
  },
  {
    id: 'steam-state', tags: 'steamcmd 0x6 0x2 0x202 state update job download fehlgeschlagen installation',
    match: /state is 0x|SteamCMD-Installation fehlgeschlagen|SteamCMD installation failed/i,
    de: { t: 'SteamCMD-Fehler „state is 0x…“', b: `
<p>SteamCMD meldet mit diesem Code, warum der Download nicht geklappt hat:</p>
<ul>
<li><b>0x6:</b> Meist hängt ein alter Download-Stand (das Panel räumt ihn beim nächsten Versuch selbst weg) oder der Rechner erreicht die Download-Server von Steam nicht (DNS-Filter, Firewall).</li>
<li><b>0x202 / 0x212:</b> Platte voll.</li>
<li><b>0x402 / 0x602:</b> Verbindungsproblem bei Steam, später erneut versuchen.</li>
<li><b>0x606:</b> Steam darf nicht in den Serverordner schreiben.</li>
<li><b>0x626 / 0x6A6:</b> Download-Dateien beschädigt; das Panel prüft beim nächsten Versuch alle Dateien.</li>
</ul>
<p>Die Zeilen mit <code>[steam]</code> in der Konsole stammen aus Steams eigenem Protokoll und sagen oft genauer, was los ist. Hilft nichts: Server einmal stoppen und neu starten.</p>` },
    en: { t: 'SteamCMD error “state is 0x…”', b: `
<p>With this code SteamCMD says why the download failed:</p>
<ul>
<li><b>0x6:</b> Usually an old download state is stuck (the panel clears it itself on the next attempt), or the machine cannot reach Steam’s download servers (DNS filter, firewall).</li>
<li><b>0x202 / 0x212:</b> Disk full.</li>
<li><b>0x402 / 0x602:</b> Connection problem at Steam, try again later.</li>
<li><b>0x606:</b> Steam may not write to the server folder.</li>
<li><b>0x626 / 0x6A6:</b> Download files damaged; the panel checks all files on the next attempt.</li>
</ul>
<p>Lines starting with <code>[steam]</code> in the console come from Steam’s own log and often say more. If nothing helps, stop the server and start it again.</p>` }
  },
  {
    id: 'steam-login', tags: 'steam konto anmeldung guard code account login workshop besitzen',
    match: /Steam-Anmeldung|Steam-Konto|Steam Guard|Steam sign-in|Steam account/i,
    de: { t: 'Spiel braucht ein Steam-Konto', b: `
<p>Manche Server (und viele Workshop-Mods) gibt Steam nur an angemeldete Konten heraus, manchmal nur an Konten, die das Spiel besitzen.</p>
<ul>
<li>Unter Einstellungen → Steam-Konto anmelden. Das Passwort speichert das Panel nicht, nur die Anmeldung, die Steam ausstellt.</li>
<li>Steam Guard: Den Code aus der E-Mail oder der Steam-App eintragen.</li>
<li>Tipp: Ein eigenes Zweitkonto für den Server verwenden.</li>
</ul>
<p>Läuft die Anmeldung ab, steht das beim Server; einfach neu anmelden und den Server neu starten.</p>` },
    en: { t: 'Game needs a Steam account', b: `
<p>Steam hands out some servers (and many Workshop mods) only to signed-in accounts, sometimes only to accounts that own the game.</p>
<ul>
<li>Sign in under Settings → Steam account. The panel does not store the password, only the session Steam issues.</li>
<li>Steam Guard: enter the code from the email or the Steam app.</li>
<li>Tip: use a separate second account for the server.</li>
</ul>
<p>If the sign-in expires, the server says so; just sign in again and restart the server.</p>` }
  },
  {
    id: 'ram', tags: 'ram speicher absturz killed oom 137 abgestürzt crash memory',
    match: /zu wenig RAM|Killed|OOM|vom System beendet|not enough RAM|ran out of RAM/i,
    de: { t: 'Server stürzt ab oder wird beendet', b: `
<ul>
<li><b>„Killed“, Code 137, „zu wenig RAM“:</b> Der Server hat seine RAM-Grenze erreicht. Im Server-Menü → „Name, RAM, Startoptionen“ mehr RAM geben. Der Leistungsverlauf zeigt, wie nah er an der Grenze war.</li>
<li><b>Abstürze nach Mod-Updates:</b> Im Reiter Mods nachsehen, welche Mod einen Fehler meldet, oder ein Backup von vorher wiederherstellen.</li>
<li><b>Unklar?</b> „Mit KI prüfen“ in den Server-Details wertet Konsole, Verlauf und Auslastung aus.</li>
</ul>
<p>Der Absturz-Wächter (Reiter Aufgaben) startet einen abgestürzten Server neu und hört nach drei Abstürzen in zehn Minuten auf.</p>` },
    en: { t: 'Server crashes or gets killed', b: `
<ul>
<li><b>“Killed”, code 137, “not enough RAM”:</b> The server hit its RAM limit. Give it more under the server menu → “Name, RAM, start options”. The performance history shows how close it was.</li>
<li><b>Crashes after mod updates:</b> Check in the Mods tab which mod reports an error, or restore a backup from before.</li>
<li><b>Not sure?</b> “Check with AI” in the server details analyses console, history and load.</li>
</ul>
<p>The crash watchdog (Tasks tab) restarts a crashed server and stops after three crashes in ten minutes.</p>` }
  },
  {
    id: 'ports', tags: 'port belegt address already in use einmal pro rechner',
    match: /Port.*belegt|address already in use|ports .* are taken|Ports von .* sind belegt/i,
    de: { t: 'Port belegt', b: `
<p>Ein anderes Programm oder ein anderer Server nutzt den Port schon.</p>
<ul>
<li>Neue Server bekommen automatisch freie Ports. Einige Spiele lassen sich aber nur mit ihren festen Ports betreiben, davon passt dann nur einer pro Rechner (steht in der Vorlage).</li>
<li>Den anderen Server stoppen, oder ihn auf einen zweiten Rechner umziehen (Mehrere Rechner).</li>
</ul>` },
    en: { t: 'Port in use', b: `
<p>Another program or server already uses the port.</p>
<ul>
<li>New servers get free ports automatically. Some games only work with their fixed ports, so only one fits per machine (the template says so).</li>
<li>Stop the other server, or move it to a second machine (Multiple machines).</li>
</ul>` }
  },
  {
    id: 'docker', tags: 'docker nicht erreichbar dienst systemctl',
    match: /Docker ist nicht erreichbar|Docker is not reachable/i,
    de: { t: 'Docker ist nicht erreichbar', b: `
<p>Jeder Gameserver läuft in einem Docker-Container. Ohne Docker kann das Panel nichts starten.</p>
<pre>sudo systemctl status docker
sudo systemctl start docker</pre>
<p>Das Installationsskript richtet Docker selbst ein. Läuft das Panel in einem Proxmox-Container (LXC), braucht der Container „nesting“, besser ist eine VM.</p>` },
    en: { t: 'Docker is not reachable', b: `
<p>Every game server runs in a Docker container. Without Docker the panel cannot start anything.</p>
<pre>sudo systemctl status docker
sudo systemctl start docker</pre>
<p>The install script sets up Docker itself. If the panel runs in a Proxmox container (LXC), it needs “nesting”; a VM is better.</p>` }
  },
  {
    id: 'backups', tags: 'backup sicherung wiederherstellen extern s3 sftp ordner restore',
    de: { t: 'Backups und Wiederherstellen', b: `
<ul>
<li><b>Manuell oder geplant:</b> Reiter Backups. Gesichert werden Spielstände und Einstellungen, nicht die Spieldateien (die kommen von Steam oder aus dem Image).</li>
<li><b>Wiederherstellen:</b> Das Panel stoppt den Server, sichert den aktuellen Stand („vor Wiederherstellung“) und spielt das Backup ein.</li>
<li><b>Extern:</b> Unter Einstellungen → Externe Backups ein S3-Ziel oder einen Ordner (z. B. eine eingehängte NAS-Freigabe) eintragen. Backups auf derselben Platte schützen nicht vor einem Plattendefekt.</li>
</ul>` },
    en: { t: 'Backups and restoring', b: `
<ul>
<li><b>Manual or scheduled:</b> Backups tab. Saves and settings are backed up, not the game files (those come from Steam or the image).</li>
<li><b>Restore:</b> The panel stops the server, saves the current state (“before restore”) and restores the backup.</li>
<li><b>External:</b> Under Settings → External backups add an S3 target or a folder (e.g. a mounted NAS share). Backups on the same disk do not protect against a disk failure.</li>
</ul>` }
  },
  {
    id: 'mods', tags: 'mods modrinth workshop bepinex thunderstore plugins installieren',
    de: { t: 'Mods installieren', b: `
<ul>
<li><b>Minecraft:</b> Reiter Mods → Modrinth durchsuchen und installieren. Mod-Updates prüft das Panel selbst.</li>
<li><b>Steam Workshop:</b> Reiter Mods → Workshop. Das Panel lädt die Mods vor dem Start per SteamCMD; manche Spiele brauchen dafür ein Steam-Konto, das das Spiel besitzt.</li>
<li><b>BepInEx, Paks, UE4SS:</b> Dateien über den Reiter Dateien hochladen; die Liste „Installiert und geladen“ zeigt, ob sie laden.</li>
<li><b>Mitspieler:</b> Die Statusseite kann die Client-Mods als Paket zum Herunterladen anbieten.</li>
</ul>` },
    en: { t: 'Installing mods', b: `
<ul>
<li><b>Minecraft:</b> Mods tab → search Modrinth and install. The panel checks for mod updates itself.</li>
<li><b>Steam Workshop:</b> Mods tab → Workshop. The panel downloads the mods via SteamCMD before starting; some games need a Steam account that owns the game.</li>
<li><b>BepInEx, paks, UE4SS:</b> upload the files in the Files tab; the “Installed and loaded” list shows whether they load.</li>
<li><b>Players:</b> the status page can offer the client mods as a download.</li>
</ul>` }
  },
  {
    id: 'https', tags: 'https zertifikat ssl lets encrypt selbst signiert warnung ausgesperrt reverse proxy',
    de: { t: 'HTTPS und Zertifikate', b: `
<ul>
<li><b>Selbst signiert:</b> Verschlüsselt sofort, der Browser warnt aber beim ersten Aufruf. Einmal „Trotzdem fortfahren“.</li>
<li><b>Let's Encrypt:</b> Braucht eine eigene Domain, die auf deine öffentliche IP zeigt, und Port 80 im Router.</li>
<li><b>Reverse Proxy</b> (Traefik, nginx, Caddy): HTTPS dort machen und den Proxy unter Einstellungen → HTTPS eintragen.</li>
<li><b>Ausgesperrt?</b> Auf dem Rechner selbst geht <code>http://localhost:8420</code> immer, oder in der Dienstdatei <code>Environment=GP_NO_TLS=1</code> ergänzen.</li>
</ul>` },
    en: { t: 'HTTPS and certificates', b: `
<ul>
<li><b>Self-signed:</b> Encrypts right away, but the browser warns on the first visit. Choose “continue anyway” once.</li>
<li><b>Let's Encrypt:</b> Needs your own domain pointing to your public IP, and port 80 in your router.</li>
<li><b>Reverse proxy</b> (Traefik, nginx, Caddy): do HTTPS there and enter the proxy under Settings → HTTPS.</li>
<li><b>Locked out?</b> On the machine itself <code>http://localhost:8420</code> always works, or add <code>Environment=GP_NO_TLS=1</code> to the service file.</li>
</ul>` }
  },
  {
    id: 'upnp', tags: 'upnp router portfreigabe fritzbox automatisch',
    de: { t: 'Ports automatisch im Router öffnen (UPnP)', b: `
<p>Mit UPnP öffnet das Panel die Ports laufender Server selbst und schließt sie wieder.</p>
<p><b>FritzBox:</b></p>
<ul>
<li>Heimnetz → Netzwerk → Netzwerkeinstellungen: „Statusinformationen über UPnP übertragen“ einschalten.</li>
<li>Internet → Freigaben → Portfreigaben: beim Gerät dieses Rechners „Selbstständige Portfreigaben für dieses Gerät erlauben“ anhaken.</li>
</ul>
<p>Danach unter Einstellungen → Router &amp; Ports „Router prüfen“.</p>` },
    en: { t: 'Open ports automatically (UPnP)', b: `
<p>With UPnP the panel opens the ports of running servers itself and closes them again.</p>
<p><b>FritzBox:</b></p>
<ul>
<li>Home Network → Network → Network Settings: turn on “Transmit status information over UPnP”.</li>
<li>Internet → Permit Access → Port Sharing: for this computer tick “Permit independent port sharing for this device”.</li>
</ul>
<p>Then click “Check router” under Settings → Router &amp; ports.</p>` }
  },
  {
    id: 'nodes', tags: 'mehrere rechner koppeln knoten umziehen zweiter rechner multiple machines',
    de: { t: 'Mehrere Rechner', b: `
<ol>
<li>Auf dem zweiten Rechner Bytewise Panel installieren (gleiche Version).</li>
<li>Dort unter Einstellungen → Mehrere Rechner einen Kopplungscode erzeugen.</li>
<li>Im ersten Panel den Code unter „Rechner hinzufügen“ einfügen.</li>
</ol>
<p>Danach erscheinen die Server des zweiten Rechners hier, lassen sich dort anlegen und über das Server-Menü zwischen den Rechnern umziehen. Statusseite und Discord-Bot zeigen alle Rechner.</p>` },
    en: { t: 'Multiple machines', b: `
<ol>
<li>Install Bytewise Panel on the second machine (same version).</li>
<li>There, create a pairing code under Settings → Multiple machines.</li>
<li>Paste the code in the first panel under “Add machine”.</li>
</ol>
<p>The second machine’s servers then appear here, can be created there and moved between machines via the server menu. Status page and Discord bot cover all machines.</p>` }
  },
  {
    id: 'discord', tags: 'discord bot befehle status whitelist',
    de: { t: 'Discord-Bot', b: `
<p>Unter Einstellungen → Discord-Bot steht eine Schritt-für-Schritt-Anleitung (5 Minuten). Der Bot zeigt eine Statusnachricht, die sich selbst aktualisiert, und kennt <code>/status</code>, <code>/start</code>, <code>/stop</code>, <code>/restart</code> und <code>/whitelist</code>. Wer starten und stoppen darf, legst du über Rollen fest.</p>` },
    en: { t: 'Discord bot', b: `
<p>Settings → Discord bot has a step-by-step guide (5 minutes). The bot posts a status message that updates itself and knows <code>/status</code>, <code>/start</code>, <code>/stop</code>, <code>/restart</code> and <code>/whitelist</code>. Roles decide who may start and stop.</p>` }
  },
  {
    id: 'status', tags: 'statusseite einladung link wunschliste kalender mitspieler',
    de: { t: 'Statusseite, Einladungslink und Wunschliste', b: `
<ul>
<li><b>Statusseite:</b> Einstellungen → Statusseite. Zeigt ausgewählte Server ohne Anmeldung, auf Wunsch unter eigener Domain, mit Spielerliste, Events, Kalender-Abo und Mod-Paket.</li>
<li><b>Einladungslink:</b> Server-Menü → Einladungslink. Eine Seite mit allem, was ein Freund zum Beitreten braucht, auch als QR-Code.</li>
<li><b>Wunschliste:</b> Mitspieler schlagen Spiele und Mods vor und stimmen ab; du setzt Stand und Antwort.</li>
</ul>` },
    en: { t: 'Status page, invite link and wish list', b: `
<ul>
<li><b>Status page:</b> Settings → Status page. Shows chosen servers without login, optionally on its own domain, with player list, events, calendar feed and mod pack.</li>
<li><b>Invite link:</b> Server menu → Invite link. One page with everything a friend needs to join, also as a QR code.</li>
<li><b>Wish list:</b> players suggest games and mods and vote; you set state and answer.</li>
</ul>` }
  },
  {
    id: 'tasks', tags: 'aufgaben neustart nachts makro geplant zeitplan',
    de: { t: 'Aufgaben und Makros', b: `
<ul>
<li><b>Aufgaben</b> (Reiter Aufgaben): Neustart, Update, Backup, Befehl oder Makro zu festen Zeiten, auf Wunsch nur wenn niemand spielt, mit Vorwarnung und Backup vorher.</li>
<li><b>Zur ruhigsten Zeit:</b> Statt einer festen Uhrzeit wählt das Panel die Stunde, in der in den letzten 30 Tagen am wenigsten los war. Dafür braucht es mindestens 7 Tage Spieler-Statistik, bis dahin gilt die eingetragene Uhrzeit. Die Stunde wird einmal am Tag neu berechnet.</li>
<li><b>Makros</b> (unter der Konsole): gespeicherte Befehlsfolgen, <code>warte 10</code> macht eine Pause.</li>
<li><b>Events</b>: Einstellungen für eine begrenzte Zeit ändern (z. B. doppelte XP am Wochenende), danach stellt das Panel die alten Werte wieder her.</li>
</ul>` },
    en: { t: 'Tasks and macros', b: `
<ul>
<li><b>Tasks</b> (Tasks tab): restart, update, backup, command or macro at fixed times, optionally only when nobody plays, with warning and backup first.</li>
<li><b>At the quietest time:</b> instead of a fixed time the panel picks the hour with the fewest players over the last 30 days. It needs at least 7 days of player statistics; until then the entered time applies. The hour is recalculated once a day.</li>
<li><b>Macros</b> (below the console): saved command sequences, <code>wait 10</code> pauses.</li>
<li><b>Events</b>: change settings for a limited time (e.g. double XP at the weekend); afterwards the panel restores the old values.</li>
</ul>` }
  },
  {
    id: 'api', tags: 'api schlüssel key webhook home assistant n8n stream deck skript automatisierung',
    de: { t: 'API-Schlüssel und Webhooks', b: `
<p>Unter Einstellungen → API &amp; Webhooks.</p>
<ul>
<li><b>API-Schlüssel</b> erlauben Skripten und Home Assistant, Server zu starten, zu stoppen oder Befehle zu schicken. Sie haben Bediener- oder Zuschauer-Rechte, nie Admin-Rechte, und lassen sich auf einzelne Server begrenzen. Der Schlüssel wird nur einmal angezeigt; gespeichert ist nur eine Prüfsumme.</li>
<li>Aufruf: <code>curl -X POST -H "Authorization: Bearer gpk_…" https://panel/api/servers/ID/start</code>. Weitere Beispiele stehen unter „So benutzt du die API“.</li>
<li><b>Webhooks</b> ruft das Panel bei Ereignissen auf: Status geändert, Spieler kommen und gehen, Absturz, Backup, Update und mehr. Discord- und Slack-Adressen bekommen eine fertige Nachricht, alle anderen JSON mit Signatur.</li>
<li>Mit „Testen“ siehst du sofort, ob die Adresse erreichbar ist. Schlägt ein Aufruf fehl, versucht das Panel es nach 10 und 60 Sekunden erneut.</li>
</ul>` },
    en: { t: 'API keys and webhooks', b: `
<p>Under Settings → API &amp; Webhooks.</p>
<ul>
<li><b>API keys</b> let scripts and Home Assistant start or stop servers or send commands. They have operator or viewer rights, never admin rights, and can be limited to some servers. The key is shown once; only a checksum is stored.</li>
<li>Call: <code>curl -X POST -H "Authorization: Bearer gpk_…" https://panel/api/servers/ID/start</code>. More examples under “How to use the API”.</li>
<li><b>Webhooks</b> are called on events: status changed, players join and leave, crash, backup, update and more. Discord and Slack URLs get a ready message, all others JSON with a signature.</li>
<li>“Test” shows right away whether the URL is reachable. If a call fails, the panel retries after 10 and 60 seconds.</li>
</ul>` }
  },
  {
    id: 'performance', tags: 'cpu leistung langsam lag bad performance proxmox kerne',
    match: /Bad Performance/i,
    de: { t: 'Leistung und CPU-Anzeige', b: `
<ul>
<li>Die CPU-Anzeige ist der Anteil an den Kernen, die der Server nutzen darf; in den Details steht zusätzlich, wie viele Kerne gerade arbeiten.</li>
<li><b>Läuft das Panel in einer Proxmox-VM?</b> CPU-Typ auf <code>host</code> stellen (<code>qm set &lt;id&gt; --cpu host</code>), sonst fehlen der VM Prozessorbefehle und Spiele unter Wine laufen merklich langsamer.</li>
<li>Meldungen wie „Bad Performance“ (Enshrouded) heißen: ein Rechenschritt dauert zu lange. Kurz nach dem Beitreten normal, dauerhaft ein Zeichen für zu wenig CPU.</li>
<li>Der Reiter Verlauf zeigt CPU und RAM über 24 Stunden bis 30 Tage.</li>
</ul>` },
    en: { t: 'Performance and CPU display', b: `
<ul>
<li>The CPU display is the share of the cores the server may use; the details also show how many cores are busy right now.</li>
<li><b>Panel in a Proxmox VM?</b> Set the CPU type to <code>host</code> (<code>qm set &lt;id&gt; --cpu host</code>), otherwise the VM lacks CPU instructions and games under Wine run noticeably slower.</li>
<li>Messages like “Bad Performance” (Enshrouded) mean a server tick takes too long. Normal right after joining, a sign of too little CPU if it stays.</li>
<li>The History tab shows CPU and RAM from 24 hours to 30 days.</li>
</ul>` }
  },
  {
    id: 'update', tags: 'panel update aktualisieren version',
    de: { t: 'Panel aktualisieren', b: `
<p>Unter Einstellungen → Panel &amp; Updates zeigt das Panel neue Versionen an und aktualisiert sich per Klick. Laufende Gameserver laufen dabei weiter. Von Hand auf dem Rechner:</p>
<pre>cd /opt/gamepanel-src &amp;&amp; git pull &amp;&amp; bash scripts/update.sh</pre>
<p>Bei mehreren Rechnern alle auf dieselbe Version bringen.</p>` },
    en: { t: 'Updating the panel', b: `
<p>Settings → Panel &amp; updates shows new versions and updates with one click. Running game servers keep running. By hand on the machine:</p>
<pre>cd /opt/gamepanel-src &amp;&amp; git pull &amp;&amp; bash scripts/update.sh</pre>
<p>With several machines, keep them all on the same version.</p>` }
  }
];
