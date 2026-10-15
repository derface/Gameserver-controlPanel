package manager

import (
	"regexp"
	"strings"
)

var steamStateRe = regexp.MustCompile(`state is (0x[0-9a-fA-F]+)`)

// SteamStateHint explains the code in SteamCMD's "Error! App '…' state is
// 0x… after update job" (codes from LinuxGSM's list and our own cases).
func SteamStateHint(code string) string {
	switch strings.ToLower(code) {
	case "":
		return ""
	case "0x6":
		return "Steam bricht den Download ab. Meist erreicht der Rechner die Download-Server von Steam nicht (DNS-Filter wie AdGuard oder Pi-hole, Firewall, Proxy), seltener hängt ein alter Download-Stand. Den setzt das Panel beim nächsten Versuch selbst zurück."
	case "0x2":
		return "Steam konnte die App nicht aktualisieren. Oft hilft ein weiterer Versuch; bleibt es dabei, Internetverbindung und Steam-Status prüfen."
	case "0x202", "0x212":
		return "nicht genug Speicherplatz auf der Platte."
	case "0x402":
		return "keine Verbindung zu Steam. Meist ein kurzer Ausfall bei Steam; später erneut versuchen."
	case "0x602":
		return "Steam hat den Download abgelehnt oder unterbrochen. Später erneut versuchen."
	case "0x606":
		return "Steam kann nicht auf die Platte schreiben (meist Dateirechte im Serverordner)."
	case "0x626", "0x6a6":
		return "Download-Dateien fehlen oder sind beschädigt. Das Panel setzt den Download-Stand zurück und prüft alle Dateien beim nächsten Versuch."
	}
	return "unbekannter Steam-Fehler. Die Zeilen mit [steam] in der Konsole zeigen Steams eigene Meldungen."
}
