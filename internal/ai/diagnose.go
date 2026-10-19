package ai

import (
	"context"
	"encoding/json"
	"errors"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// DiagInput is everything the panel knows about a server, already cleaned
// of passwords, IP addresses and Steam IDs.
type DiagInput struct {
	Server   string   `json:"server"`
	Game     string   `json:"game"`
	Status   string   `json:"status"`
	Facts    []string `json:"facts"`    // RAM, CPU, uptime, settings, host
	Findings []string `json:"findings"` // what the panel's own rules noticed
	Events   []string `json:"events"`   // history, newest last
	Log      string   `json:"log"`      // relevant console lines
	Vars     []string `json:"vars"`     // changeable start values: key (type, options) = value
	Trigger  string   `json:"trigger,omitempty"`
}

type DiagFinding struct {
	Title    string `json:"title"`
	Detail   string `json:"detail"`
	Evidence string `json:"evidence,omitempty"`
	Level    string `json:"level,omitempty"` // ok | info | problem
}

// DiagAction is a proposal. Kinds the panel can carry out with one click:
// memory (memory_mb), restart, update, var (key, value). "manual" is advice.
type DiagAction struct {
	Title    string `json:"title"`
	Why      string `json:"why"`
	Kind     string `json:"kind"`
	MemoryMB int    `json:"memory_mb,omitempty"`
	Key      string `json:"key,omitempty"`
	Value    string `json:"value,omitempty"`
}

type Diagnosis struct {
	Time     time.Time     `json:"time"`
	Trigger  string        `json:"trigger,omitempty"` // empty = asked by a user
	Severity string        `json:"severity"`          // ok | hinweis | warnung | kritisch
	Summary  string        `json:"summary"`
	Findings []DiagFinding `json:"findings"`
	Actions  []DiagAction  `json:"actions"`
}

const diagSystem = `Du bist der Diagnose-Assistent eines Gameserver-Panels (Docker-Container je Spieleserver, Steam-Spiele teils über Wine).
Du bekommst Fakten, Regel-Treffer des Panels, den Verlauf und relevante Konsolenzeilen eines Servers.
Finde heraus, ob etwas nicht stimmt (Abstürze, zu wenig RAM, hohe Last, Hänger, Fehler beim Start, Mods, Ports, Updates) und was hilft.
Antworte AUSSCHLIESSLICH mit einem JSON-Objekt:
{"severity":"ok|hinweis|warnung|kritisch","summary":"<1-2 Sätze auf Deutsch, für Laien verständlich>",
 "findings":[{"level":"ok|info|problem","title":"<kurz>","detail":"<1-3 Sätze>","evidence":"<eine Logzeile oder Zahl als Beleg, optional>"}],
 "actions":[{"title":"<kurz, Imperativ>","why":"<1 Satz>","kind":"memory|restart|update|var|manual","memory_mb":<zahl, nur bei memory>,"key":"<nur bei var>","value":"<nur bei var>"}]}
Regeln:
- Stütze dich nur auf die gelieferten Daten. Rate nicht; wenn die Daten nicht reichen, sag das im summary und schlage vor, was man prüfen kann.
- Ist alles in Ordnung, severity "ok", höchstens ein Hinweis, keine unnötigen Aktionen.
- level je finding: "ok" für etwas, das gut aussieht (z. B. Ressourcen im grünen Bereich), "info" für Auffälliges ohne Folgen, "problem" für echte Fehler.
- Zeilen im Abschnitt "Bekannt harmlos" sind bei diesem Spiel normal: keine findings, keine Aktionen dafür, und sie heben die severity nicht an.
- Viele Spiele schreiben Warnungen, die nichts bedeuten. Nur weil eine Zeile "error" oder "could not" enthält, ist sie kein Problem; entscheidend ist, ob der Server läuft, Spieler verbinden können und ob es Abstürze gibt. Keine Neustarts oder Prüfungen empfehlen, wenn nichts kaputt ist.
- Häufige Ursachen: "Killed"/OOM oder RAM dauerhaft nahe am Limit → kind "memory" mit realistischem Wert (höchstens der freie RAM des Rechners plus die bisherige Zuweisung); Java-Heap (-Xmx) zu groß für den Container; Mods, die beim Laden Fehler werfen → kind "manual" mit Mod-Namen; Steam-Download-Fehler → kind "update"; Port belegt ("address already in use") → kind "manual".
- kind "var" nur mit einem key aus der Liste der änderbaren Startwerte und einem erlaubten Wert.
- Höchstens 4 findings und 4 actions. Deutsch, sachlich, keine Floskeln.
- Wiederhole keine Passwörter oder Kennungen; sie sind ohnehin entfernt.`

// Diagnose asks the provider about one server.
func Diagnose(ctx context.Context, s store.Settings, in DiagInput) (Diagnosis, error) {
	var d Diagnosis
	var b strings.Builder
	b.WriteString("Server: " + in.Server + " (" + in.Game + "), Status: " + in.Status + "\n")
	if in.Trigger != "" {
		b.WriteString("Anlass: " + in.Trigger + "\n")
	}
	section := func(title string, lines []string) {
		if len(lines) == 0 {
			return
		}
		b.WriteString("\n" + title + ":\n")
		for _, l := range lines {
			b.WriteString("- " + l + "\n")
		}
	}
	section("Fakten", in.Facts)
	section("Regel-Treffer des Panels", in.Findings)
	section("Änderbare Startwerte", in.Vars)
	section("Verlauf (älteste zuerst)", in.Events)
	if in.Log != "" {
		b.WriteString("\nKonsole (Auszug, Fehler und letzte Zeilen):\n" + in.Log + "\n")
	}
	txt, err := completeN(ctx, s, diagSystem, b.String(), 1800)
	if err != nil {
		return d, err
	}
	i, j := strings.Index(txt, "{"), strings.LastIndex(txt, "}")
	if i < 0 || j <= i {
		return d, errors.New("Die KI hat keine Diagnose geliefert. Bitte erneut versuchen")
	}
	if err := json.Unmarshal([]byte(txt[i:j+1]), &d); err != nil {
		return d, errors.New("Die Antwort der KI war unlesbar. Bitte erneut versuchen")
	}
	switch d.Severity {
	case "ok", "hinweis", "warnung", "kritisch":
	default:
		d.Severity = "hinweis"
	}
	if len(d.Findings) > 4 {
		d.Findings = d.Findings[:4]
	}
	for i := range d.Findings {
		switch d.Findings[i].Level {
		case "ok", "info", "problem":
		default:
			d.Findings[i].Level = map[string]string{"ok": "ok", "hinweis": "info"}[d.Severity]
			if d.Findings[i].Level == "" {
				d.Findings[i].Level = "problem"
			}
		}
	}
	if len(d.Actions) > 4 {
		d.Actions = d.Actions[:4]
	}
	if d.Findings == nil {
		d.Findings = []DiagFinding{}
	}
	if d.Actions == nil {
		d.Actions = []DiagAction{}
	}
	d.Time = time.Now()
	d.Trigger = in.Trigger
	return d, nil
}
