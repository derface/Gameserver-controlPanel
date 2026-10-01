// Package ai turns a plain-language request into a server plan using the
// customer's own AI provider (Anthropic, OpenAI or a local Ollama).
// The AI only proposes; the user confirms before anything is created.
package ai

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

type Plan struct {
	Template string            `json:"template"`
	Name     string            `json:"name"`
	Vars     map[string]string `json:"vars"`
	MemoryMB int               `json:"memory_mb"`
	Reply    string            `json:"reply"`
	Steps    []string          `json:"steps"`
	Question string            `json:"question,omitempty"` // set when the AI needs more info
}

type Context struct {
	FreeMemMB  int
	TotalMemMB int
	Servers    []string // "Name (Spiel, Status)"
}

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
	b.WriteString(`Du bist der KI-Assistent eines Gameserver-Panels. Du planst neue Gameserver.
Antworte AUSSCHLIESSLICH mit einem JSON-Objekt, ohne Text davor oder danach, in diesem Format:
{"template":"<id>","name":"<kurzer Servername>","vars":{"<key>":"<wert>"},"memory_mb":<zahl>,
 "reply":"<2-3 Sätze auf Deutsch: was du vorschlägst und warum>","steps":["<Schritt>", "..."]}
Wenn die Anfrage unklar ist oder kein Spiel passt, antworte mit
{"question":"<eine kurze Rückfrage auf Deutsch>"}.
Regeln:
- Nutze nur Vorlagen-IDs und Variablen aus der Liste unten. Lass Variablen weg, deren Standardwert passt.
- Für Steam-Spiele ohne eigene Vorlage nimm "steam-custom" nur, wenn du App-ID, Startbefehl und Ports sicher kennst. Sonst stelle eine Rückfrage.
- memory_mb: realistisch für Spiel und Spielerzahl, mindestens das Minimum der Vorlage, höchstens den freien RAM.
- Passwörter nie erfinden: lass geheime Variablen leer und erwähne im reply, dass der Nutzer sie setzen soll.
- Für Minecraft-Mods nutze die Variable "mods" mit Modrinth-Namen (z. B. create,ferrite-core,modernfix) und passenden Loader als "type".
- Bevorzuge Vorlagen mit Status "stabil". Bei "beta" oder "experimentell" erwähne im reply, dass das Spiel im Panel noch nicht erprobt ist.
- steps: 4 bis 6 kurze deutsche Schritte, die das Panel ausführt (Container anlegen, Dateien laden, Einstellungen, Start).
`)
	fmt.Fprintf(&b, "\nRechner: %d MB RAM gesamt, davon %d MB frei.\n", c.TotalMemMB, c.FreeMemMB)
	if len(c.Servers) > 0 {
		b.WriteString("Vorhandene Server: " + strings.Join(c.Servers, "; ") + "\n")
	}
	b.WriteString("\nVorlagen:\n")
	for _, t := range tpls {
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
	model := s.AIModel
	if model == "" {
		model = DefaultModel(s.AIProvider)
	}
	switch s.AIProvider {
	case "anthropic":
		if s.AnthropicKey == "" {
			return "", errors.New("Kein Anthropic-API-Key hinterlegt")
		}
		var out struct {
			Content []struct {
				Type string `json:"type"`
				Text string `json:"text"`
			} `json:"content"`
		}
		err := post(ctx, "https://api.anthropic.com/v1/messages", map[string]string{
			"x-api-key": s.AnthropicKey, "anthropic-version": "2023-06-01",
		}, map[string]any{
			"model": model, "max_tokens": 1200, "system": system,
			"messages": []map[string]string{{"role": "user", "content": user}},
		}, &out)
		if err != nil {
			return "", err
		}
		var sb strings.Builder
		for _, c := range out.Content {
			if c.Type == "text" {
				sb.WriteString(c.Text)
			}
		}
		return sb.String(), nil
	case "openai":
		if s.OpenAIKey == "" {
			return "", errors.New("Kein OpenAI-API-Key hinterlegt")
		}
		var out struct {
			Choices []struct {
				Message struct {
					Content string `json:"content"`
				} `json:"message"`
			} `json:"choices"`
		}
		err := post(ctx, "https://api.openai.com/v1/chat/completions", map[string]string{
			"Authorization": "Bearer " + s.OpenAIKey,
		}, map[string]any{
			"model": model, "response_format": map[string]string{"type": "json_object"},
			"messages": []map[string]string{{"role": "system", "content": system}, {"role": "user", "content": user}},
		}, &out)
		if err != nil {
			return "", err
		}
		if len(out.Choices) == 0 {
			return "", errors.New("Leere Antwort vom KI-Anbieter")
		}
		return out.Choices[0].Message.Content, nil
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
		return out.Message.Content, err
	}
	return "", errors.New("Kein KI-Anbieter eingerichtet. Bitte in den Einstellungen auswählen")
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
	txt, err := complete(ctx, s, systemPrompt(tpls.All(), c), prompt)
	if err != nil {
		return p, err
	}
	i, j := strings.Index(txt, "{"), strings.LastIndex(txt, "}")
	if i < 0 || j <= i {
		return p, errors.New("Die KI hat keinen Plan geliefert. Bitte anders formulieren")
	}
	if err := json.Unmarshal([]byte(txt[i:j+1]), &p); err != nil {
		return p, errors.New("Die Antwort der KI war unlesbar. Bitte erneut versuchen")
	}
	if p.Question != "" {
		return p, nil
	}
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
		p.Vars = map[string]string{}
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
