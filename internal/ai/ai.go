// Package ai turns a plain-language request into a server plan and
// diagnoses server problems, using the customer's own AI provider
// (Anthropic, OpenAI or a local Ollama). The AI only proposes; the user
// confirms before anything is created or changed.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

type Plan struct {
	Action   string            `json:"action,omitempty"` // "" or create | change (an existing server) | event (temporary change)
	Server   string            `json:"server,omitempty"` // id, for change
	Files    map[string]StrMap `json:"files,omitempty"`
	Template string            `json:"template"`
	Name     string            `json:"name"`
	Vars     StrMap            `json:"vars"`
	MemoryMB int               `json:"memory_mb"`
	Reply    string            `json:"reply"`
	Steps    []string          `json:"steps"`
	Question string            `json:"question,omitempty"` // set when the AI needs more info
	// for action event: the period the values apply, RFC3339 with offset
	Start  string `json:"start,omitempty"`
	End    string `json:"end,omitempty"`
	Weekly bool   `json:"weekly,omitempty"`
}

// StrMap takes numbers and booleans as text: models often write 2 instead
// of "2".
type StrMap map[string]string

func (m *StrMap) UnmarshalJSON(b []byte) error {
	var raw map[string]any
	if err := json.Unmarshal(b, &raw); err != nil {
		return err
	}
	out := StrMap{}
	flatten(raw, "", out)
	*m = out
	return nil
}

// flatten writes nested objects as dotted keys: {"gameSettings":{"a":2}}
// becomes "gameSettings.a" = "2".
func flatten(raw map[string]any, prefix string, out StrMap) {
	for k, v := range raw {
		if prefix != "" {
			k = prefix + "." + k
		}
		switch x := v.(type) {
		case map[string]any:
			flatten(x, k, out)
		case string:
			out[k] = x
		case float64:
			out[k] = strconv.FormatFloat(x, 'f', -1, 64)
		case bool:
			out[k] = strconv.FormatBool(x)
		case nil:
			out[k] = ""
		default:
			j, _ := json.Marshal(x)
			out[k] = string(j)
		}
	}
}

type Context struct {
	FreeMemMB  int
	TotalMemMB int
	Servers    []string // "id=…: Name (Spiel, Status)"
	// Focus holds the settings of the server the user talks about, if any.
	Focus string
	// History is the conversation so far ("Du: …" / "KI: …").
	History []string
	// Now is the user's local time, for events ("am Wochenende").
	Now time.Time
}

var weekdayDE = [...]string{"Sonntag", "Montag", "Dienstag", "Mittwoch", "Donnerstag", "Freitag", "Samstag"}

func DefaultModel(provider string) string {
	switch provider {
	case "anthropic":
		return "claude-sonnet-5-5"
	case "openai":
		return "gpt-4o-mini"
	case "ollama":
		return "llama3.1"
	}
	return ""
}

func systemPrompt(tpls []templates.Template, c Context) string {
	var b strings.Builder
	b.WriteString(`Du bist der KI-Assistent eines Gameserver-Panels. Du planst neue Gameserver und änderst vorhandene.
Antworte AUSSCHLIESSLICH mit einem JSON-Objekt, ohne Text davor oder danach. Es gibt vier Antworten:

A) Vorhandenen Server dauerhaft ändern, wenn der Nutzer von einem vorhandenen Server spricht und keinen Zeitraum nennt (z. B. "den Enshrouded-Server als Highrate", "mehr Loot", "schwerer", "mehr Spieler"):
{"action":"change","server":"<id>","vars":{"<key>":"<wert>"},"files":{"<datei-nr>":{"<key>":"<wert>"}},"memory_mb":<zahl oder 0>,
 "reply":"<2-4 Sätze auf Deutsch: was du änderst und welche Wirkung das hat>"}
- Nutze nur Schlüssel aus "Einstellungen des Servers" unten, mit Werten im angegebenen Typ und Bereich, als Text, Zahlen mit Punkt. Bleib innerhalb der Bereiche; geht ein Wunsch darüber hinaus, nimm den Höchstwert und sag das im reply.
- Formuliere das reply als Vorschlag ("Ich würde …"), nicht als erledigt: Der Nutzer muss die Änderung erst übernehmen.
- Steht bei einer Einstellung "wirkt nur wenn X=Y", setze X auf Y mit (z. B. die Voreinstellung auf "Custom"), sonst wirkt die Änderung nicht.
- Begriffe aus der Spieler-Szene übersetzt du selbst in Werte: "Highrate"/"x5" = deutlich schnelleres Leveln, mehr Erfahrung, mehr Ressourcen und Loot, schnelleres Wachsen und Herstellen; "Midrate" etwa x2 bis x3; "Mid/Highrate" dazwischen. Frag nicht nach, wenn der Wunsch so verständlich ist. Nenne im reply die wichtigsten neuen Werte.
- memory_mb nur angeben, wenn der Nutzer mehr oder weniger RAM will, sonst 0.
- Fehlen unten die Einstellungen des gemeinten Servers, frage nach, welcher Server gemeint ist, und nenne die Namen.

B) Neuen Server planen:
{"action":"create","template":"<id>","name":"<kurzer Servername>","vars":{"<key>":"<wert>"},"memory_mb":<zahl>,
 "reply":"<2-3 Sätze auf Deutsch: was du vorschlägst und warum>","steps":["<Schritt>", "..."]}

D) Zeitlich begrenztes Event auf einem vorhandenen Server, wenn der Nutzer einen Zeitraum nennt ("am Wochenende", "heute Abend", "von Freitag bis Sonntag", "jeden Samstag"):
{"action":"event","server":"<id>","name":"<kurzer Eventname, z. B. Highrate-Wochenende>","start":"<RFC3339 mit Zeitzone>","end":"<RFC3339 mit Zeitzone>","weekly":<true wenn jede Woche>,
 "vars":{...},"files":{...},"reply":"<2-3 Sätze: Zeitraum, wichtigste Werte, dass danach alles automatisch zurückgestellt wird>"}
- Werte wie bei A. Das Panel stellt die alten Werte nach dem Ende selbst wieder her.
- "Wochenende" ohne Uhrzeit = Freitag 18:00 bis Sonntag 23:00 des kommenden (oder laufenden) Wochenendes. "Heute Abend" = heute 18:00 bis 23:59. Ein Event dauert höchstens 14 Tage, ein wöchentliches höchstens 6 Tage.
- Läuft der genannte Zeitraum schon (z. B. Samstag und "am Wochenende"), beginne jetzt.

C) Rückfrage, wenn die Anfrage unklar ist oder kein Spiel passt:
{"question":"<eine kurze Rückfrage auf Deutsch>"}

Regeln für B:
- Nutze nur Vorlagen-IDs und Variablen aus der Liste unten. Lass Variablen weg, deren Standardwert passt.
- Für Steam-Spiele ohne eigene Vorlage nimm "steam-custom" nur, wenn du App-ID, Startbefehl und Ports sicher kennst. Sonst stelle eine Rückfrage.
- memory_mb: realistisch für Spiel und Spielerzahl, mindestens das Minimum der Vorlage, höchstens den freien RAM.
- Passwörter nie erfinden: lass geheime Variablen leer und erwähne im reply, dass der Nutzer sie setzen soll.
- Für Minecraft-Mods nutze die Variable "mods" mit Modrinth-Namen (z. B. create,ferrite-core,modernfix) und passenden Loader als "type".
- Bevorzuge Vorlagen mit Status "stabil". Bei "beta" oder "experimentell" erwähne im reply, dass das Spiel im Panel noch nicht erprobt ist.
- steps: 4 bis 6 kurze deutsche Schritte, die das Panel ausführt (Container anlegen, Dateien laden, Einstellungen, Start).
Beziehe dich auf das bisherige Gespräch, wenn die neue Nachricht daran anknüpft.
`)
	if !c.Now.IsZero() {
		fmt.Fprintf(&b, "\nJetzt ist %s, %s (Zeitzone des Nutzers).\n", weekdayDE[c.Now.Weekday()], c.Now.Format(time.RFC3339))
	}
	fmt.Fprintf(&b, "\nRechner: %d MB RAM gesamt, davon %d MB frei.\n", c.TotalMemMB, c.FreeMemMB)
	if len(c.Servers) > 0 {
		b.WriteString("Vorhandene Server: " + strings.Join(c.Servers, "; ") + "\n")
	}
	if c.Focus != "" {
		b.WriteString("\nEinstellungen des Servers, um den es geht:\n" + c.Focus + "\n")
	}
	b.WriteString("\nVorlagen:\n")
	for _, t := range tpls {
		if t.Status == "defekt" {
			continue
		}
		fmt.Fprintf(&b, "- id=%s | %s | RAM %d MB (min %d)", t.ID, t.Name, t.MemoryMB, t.MinMemoryMB)
		if t.Status != "stabil" {
			fmt.Fprintf(&b, " | %s", t.Status)
		}
		if t.SteamAppID != 0 {
			fmt.Fprintf(&b, " | Steam-App %d", t.SteamAppID)
		}
		var vs []string
		for _, v := range t.Vars {
			s := v.Key
			if v.Type == "select" && len(v.Options) > 0 {
				s += "=" + strings.Join(v.Options, "/")
			} else if v.Type == "bool" {
				s += "=true/false"
			} else if v.Default != "" {
				s += " (Standard " + v.Default + ")"
			}
			if v.Type == "secret" {
				s += " [geheim]"
			}
			vs = append(vs, s)
		}
		if len(vs) > 0 {
			b.WriteString(" | Variablen: " + strings.Join(vs, ", "))
		}
		b.WriteString("\n")
	}
	return b.String()
}

var client = &http.Client{Timeout: 90 * time.Second}

func post(ctx context.Context, url string, headers map[string]string, body any, out any) error {
	j, _ := json.Marshal(body)
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(j))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("KI-Anbieter nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 2<<20))
	if resp.StatusCode >= 400 {
		msg := string(b)
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error.Message != "" {
			msg = e.Error.Message
		}
		if resp.StatusCode == 401 || resp.StatusCode == 403 {
			return fmt.Errorf("Der API-Key wurde abgelehnt. Bitte in den Einstellungen prüfen (%s)", msg)
		}
		return fmt.Errorf("KI-Anbieter meldet Fehler %d: %s", resp.StatusCode, msg)
	}
	return json.Unmarshal(b, out)
}

func complete(ctx context.Context, s store.Settings, system, user string) (string, error) {
	return completeN(ctx, s, system, user, 1200)
}

func completeN(ctx context.Context, s store.Settings, system, user string, maxTokens int) (string, error) {
	txt, _, err := completeFull(ctx, s, system, user, maxTokens)
	return txt, err
}

// completeFull also reports whether the answer was cut off at maxTokens.
func completeFull(ctx context.Context, s store.Settings, system, user string, maxTokens int) (string, bool, error) {
	model := s.AIModel
	if model == "" {
		model = DefaultModel(s.AIProvider)
	}
	switch s.AIProvider {
	case "anthropic":
		if s.AnthropicKey == "" {
			return "", false, errors.New("Kein Anthropic-API-Key hinterlegt")
		}
		var out struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
			StopReason string `json:"stop_reason"`
		}
		err := post(ctx, "https://api.anthropic.com/v1/messages", map[string]string{
			"x-api-key": s.AnthropicKey, "anthropic-version": "2023-06-01",
		}, map[string]any{
			"model": model, "max_tokens": maxTokens, "system": system,
			"messages": []map[string]string{{"role": "user", "content": user}},
		}, &out)
		if err != nil {
			return "", false, err
		}
		var sb strings.Builder
		for _, c := range out.Content {
			if c.Type == "text" {
				sb.WriteString(c.Text)
			}
		}
		return sb.String(), out.StopReason == "max_tokens", nil
	case "openai":
		if s.OpenAIKey == "" {
			return "", false, errors.New("Kein OpenAI-API-Key hinterlegt")
		}
		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
				Finish string `json:"finish_reason"`
			} `json:"choices"`
		}
		err := post(ctx, "https://api.openai.com/v1/chat/completions", map[string]string{
			"Authorization": "Bearer " + s.OpenAIKey,
		}, map[string]any{
			"model": model, "response_format": map[string]string{"type": "json_object"}, "max_completion_tokens": maxTokens,
			"messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
		}, &out)
		if err != nil {
			return "", false, err
		}
		if len(out.Choices) == 0 {
			return "", false, errors.New("Leere Antwort vom KI-Anbieter")
		}
		return out.Choices[0].Message.Content, out.Choices[0].Finish == "length", nil
	case "ollama":
		base := strings.TrimRight(s.OllamaURL, "/")
		if base == "" {
			base = "http://localhost:11434"
		}
		var out struct {
			Message struct {
				Content string `json:"content"`
			} `json:"message"`
		}
		err := post(ctx, base+"/api/chat", nil, map[string]any{
			"model": model, "stream": false, "format": "json",
			"messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
		}, &out)
		return out.Message.Content, false, err
	}
	return "", false, errors.New("Kein KI-Anbieter eingerichtet. Bitte in den Einstellungen auswählen")
}

// MakePlan asks the provider for a plan and validates the template id.
func MakePlan(ctx context.Context, s store.Settings, tpls *templates.Set, c Context, prompt string) (Plan, error) {
	var p Plan
	prompt = strings.TrimSpace(prompt)
	if prompt == "" {
		return p, errors.New("Bitte beschreibe, welchen Server du möchtest")
	}
	if len(prompt) > 2000 {
		prompt = prompt[:2000]
	}
	user := prompt
	if len(c.History) > 0 {
		h := strings.Join(c.History, "\n")
		if len(h) > 3000 {
			h = h[len(h)-3000:]
		}
		user = "Bisheriges Gespräch:\n" + h + "\n\nNeue Nachricht: " + prompt
	}
	system := systemPrompt(tpls.All(), c)
	txt, cut, err := completeFull(ctx, s, system, user, 4000)
	if err != nil {
		return p, err
	}
	if cut { // a long change list ran out of room: once more with more room
		if t2, cut2, err2 := completeFull(ctx, s, system, user, 8000); err2 == nil && !cut2 {
			txt = t2
		}
	}
	perr := parsePlan(txt, &p)
	if perr != nil {
		// one repair round: show the model its answer and the problem
		fix := user + "\n\nDeine letzte Antwort war kein gültiges JSON (" + perr.Error() + "). Antworte jetzt NUR mit dem korrigierten JSON-Objekt, ohne Text davor oder danach. Deine Antwort war:\n" + trimTo(txt, 6000)
		if t2, _, err2 := completeFull(ctx, s, system, fix, 4000); err2 == nil {
			p = Plan{}
			if parsePlan(t2, &p) == nil {
				perr = nil
			} else {
				txt = t2
			}
		}
	}
	if perr != nil {
		LastBad = BadAnswer{Time: time.Now(), Prompt: prompt, Answer: trimTo(txt, 20000), Error: perr.Error()}
		return p, errors.New("Die Antwort der KI war unlesbar, auch nach einem zweiten Versuch. Bitte anders formulieren, z. B. mit dem Servernamen")
	}
	if p.Question != "" {
		return p, nil
	}
	if p.Action == "change" || p.Action == "event" {
		if p.Server == "" {
			return p, errors.New("Die KI hat nicht gesagt, welcher Server geändert werden soll. Bitte den Namen nennen")
		}
		return p, nil
	}
	p.Action = "create"
	tpl, ok := tpls.Get(p.Template)
	if !ok {
		return p, fmt.Errorf("Die KI schlug die unbekannte Vorlage „%s“ vor. Bitte erneut versuchen", p.Template)
	}
	if p.MemoryMB < tpl.MinMemoryMB {
		p.MemoryMB = tpl.MemoryMB
	}
	if c.FreeMemMB > 0 && p.MemoryMB > c.FreeMemMB && c.FreeMemMB >= tpl.MinMemoryMB {
		p.MemoryMB = c.FreeMemMB
	}
	if p.Vars == nil {
		p.Vars = StrMap{}
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = tpl.Name
	}
	return p, nil
}

// Test sends a tiny request to check the provider settings.
func Test(ctx context.Context, s store.Settings) error {
	_, err := complete(ctx, s, "Antworte nur mit {\"ok\":true}", "Test")
	return err
}

// BadAnswer keeps the last answer that could not be read, for the support
// log (GET /api/ai/last-error).
type BadAnswer struct {
	Time   time.Time `json:"time"`
	Prompt string    `json:"prompt"`
	Answer string    `json:"answer"`
	Error  string    `json:"error"`
}

var LastBad BadAnswer

func trimTo(s string, n int) string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

// extractJSON returns the first complete JSON object in txt (models
// sometimes add text or a second object after it).
func extractJSON(txt string) (string, error) {
	i := strings.Index(txt, "{")
	if i < 0 {
		return "", errors.New("kein JSON-Objekt")
	}
	depth, inStr, esc := 0, false, false
	for j := i; j < len(txt); j++ {
		c := txt[j]
		switch {
		case esc:
			esc = false
		case c == '\\' && inStr:
			esc = true
		case c == '"':
			inStr = !inStr
		case inStr:
		case c == '{':
			depth++
		case c == '}':
			depth--
			if depth == 0 {
				return txt[i : j+1], nil
			}
		}
	}
	return "", errors.New("JSON bricht ab (Antwort unvollständig)")
}

// parsePlan reads a plan tolerantly: numbers as text, text as numbers,
// "true"/"false" as text, a list of steps given as one text.
func parsePlan(txt string, p *Plan) error {
	js, err := extractJSON(txt)
	if err != nil {
		return err
	}
	if json.Unmarshal([]byte(js), p) == nil {
		return nil
	}
	var raw map[string]any
	if err := json.Unmarshal([]byte(js), &raw); err != nil {
		return fmt.Errorf("kein gültiges JSON: %v", err)
	}
	str := func(k string) string {
		switch v := raw[k].(type) {
		case string:
			return v
		case float64:
			return strconv.FormatFloat(v, 'f', -1, 64)
		case bool:
			return strconv.FormatBool(v)
		}
		return ""
	}
	*p = Plan{Action: str("action"), Server: str("server"), Template: str("template"), Name: str("name"), Reply: str("reply"),
		Question: str("question"), Start: str("start"), End: str("end")}
	if n, err := strconv.ParseFloat(strings.TrimSpace(str("memory_mb")), 64); err == nil {
		p.MemoryMB = int(n)
	}
	p.Weekly = str("weekly") == "true"
	if m, ok := raw["vars"].(map[string]any); ok {
		p.Vars = StrMap{}
		flatten(m, "", p.Vars)
	}
	switch f := raw["files"].(type) {
	case map[string]any:
		p.Files = map[string]StrMap{}
		for k, v := range f {
			if m, ok := v.(map[string]any); ok {
				sm := StrMap{}
				flatten(m, "", sm)
				p.Files[k] = sm
			}
		}
	case []any: // [{...}, {...}] → "0", "1"
		p.Files = map[string]StrMap{}
		for i, v := range f {
			if m, ok := v.(map[string]any); ok {
				sm := StrMap{}
				flatten(m, "", sm)
				p.Files[strconv.Itoa(i)] = sm
			}
		}
	}
	switch st := raw["steps"].(type) {
	case []any:
		for _, x := range st {
			if t, ok := x.(string); ok {
				p.Steps = append(p.Steps, t)
			}
		}
	case string:
		p.Steps = []string{st}
	}
	return nil
}
