// Package notify sends panel events to Discord (webhook) and ntfy.
package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"sync"
	"time"
)

// Kinds a user can switch on.
var Kinds = []struct{ Key, Label string }{
	{"crash", "Abstürze und Hänger"},
	{"error", "Fehler (Aufgaben, Backups, Installation)"},
	{"update", "Spiel-Updates"},
	{"task", "Ausgeführte Aufgaben"},
	{"event", "Events (Ankündigung, Beginn, Ende)"},
	{"backup", "Backups erstellt"},
	{"startstop", "Start und Stopp durch Benutzer"},
	{"ai", "KI-Diagnosen"},
	{"panel", "Neue Panel-Version"},
	{"wish", "Neue Wünsche auf der Statusseite"},
}

// DefaultKinds are on when nothing was chosen yet.
var DefaultKinds = []string{"crash", "error", "update", "event", "ai", "panel", "wish"}

type Config struct {
	Discord   string   `json:"discord,omitempty"`    // webhook URL
	NtfyURL   string   `json:"ntfy_url,omitempty"`   // e.g. https://ntfy.sh/mein-topic
	NtfyToken string   `json:"ntfy_token,omitempty"` // optional access token
	Kinds     []string `json:"kinds,omitempty"`
}

func (c Config) Enabled() bool { return c.Discord != "" || c.NtfyURL != "" }

func (c Config) Wants(kind string) bool {
	ks := c.Kinds
	if ks == nil {
		ks = DefaultKinds
	}
	for _, k := range ks {
		if k == kind {
			return true
		}
	}
	return false
}

// Validate checks the URLs.
func (c *Config) Validate() error {
	c.Discord, c.NtfyURL, c.NtfyToken = strings.TrimSpace(c.Discord), strings.TrimSpace(c.NtfyURL), strings.TrimSpace(c.NtfyToken)
	if c.Discord != "" {
		u, err := url.Parse(c.Discord)
		if err != nil || u.Scheme != "https" || !(u.Host == "discord.com" || u.Host == "discordapp.com" || strings.HasSuffix(u.Host, ".discord.com")) || !strings.HasPrefix(u.Path, "/api/webhooks/") {
			return errors.New("Das ist keine Discord-Webhook-Adresse (https://discord.com/api/webhooks/…)")
		}
	}
	if c.NtfyURL != "" {
		u, err := url.Parse(c.NtfyURL)
		if err != nil || (u.Scheme != "https" && u.Scheme != "http") || u.Host == "" || strings.Trim(u.Path, "/") == "" {
			return errors.New("ntfy-Adresse bitte mit Thema angeben, z. B. https://ntfy.sh/mein-panel")
		}
	}
	known := map[string]bool{}
	for _, k := range Kinds {
		known[k.Key] = true
	}
	out := []string{}
	for _, k := range c.Kinds {
		if known[k] {
			out = append(out, k)
		}
	}
	if c.Kinds != nil {
		c.Kinds = out
	}
	return nil
}

// Message is one notification.
type Message struct {
	Title string // usually the server name
	Text  string
	Level string // info | warn | error
	Link  string // optional URL into the panel
}

var client = &http.Client{Timeout: 15 * time.Second}

// Send delivers a message to all configured targets.
func Send(ctx context.Context, c Config, m Message) error {
	var errs []string
	if c.Discord != "" {
		if err := discord(ctx, c.Discord, m); err != nil {
			errs = append(errs, "Discord: "+err.Error())
		}
	}
	if c.NtfyURL != "" {
		if err := ntfy(ctx, c.NtfyURL, c.NtfyToken, m); err != nil {
			errs = append(errs, "ntfy: "+err.Error())
		}
	}
	if len(errs) > 0 {
		return errors.New(strings.Join(errs, "; "))
	}
	return nil
}

func discord(ctx context.Context, hook string, m Message) error {
	color := map[string]int{"error": 0xE5484D, "warn": 0xF5A524, "info": 0xFF5A1F}[m.Level]
	if color == 0 {
		color = 0xFF5A1F
	}
	embed := map[string]any{"title": trim(m.Title, 250), "description": trim(m.Text, 3500), "color": color, "timestamp": time.Now().UTC().Format(time.RFC3339)}
	if m.Link != "" {
		embed["url"] = m.Link
	}
	body, _ := json.Marshal(map[string]any{"username": "Bytewise Panel", "embeds": []any{embed}, "allowed_mentions": map[string]any{"parse": []string{}}})
	return post(ctx, hook, "application/json", body, nil)
}

func ntfy(ctx context.Context, u, token string, m Message) error {
	h := map[string]string{
		"Title":    headerSafe(m.Title),
		"Priority": map[string]string{"error": "high", "warn": "default", "info": "low"}[m.Level],
		"Tags":     map[string]string{"error": "rotating_light", "warn": "warning", "info": "video_game"}[m.Level],
	}
	if h["Priority"] == "" {
		h["Priority"] = "default"
	}
	if m.Link != "" {
		h["Click"] = m.Link
	}
	if token != "" {
		h["Authorization"] = "Bearer " + token
	}
	return post(ctx, u, "text/plain; charset=utf-8", []byte(trim(m.Text, 3500)), h)
}

func post(ctx context.Context, u, ctype string, body []byte, headers map[string]string) error {
	req, err := http.NewRequestWithContext(ctx, "POST", u, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", ctype)
	for k, v := range headers {
		if v != "" {
			req.Header.Set(k, v)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("nicht erreichbar (%v)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
		return fmt.Errorf("HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	return nil
}

// headerSafe keeps HTTP headers ASCII-free of line breaks; ntfy reads UTF-8
// titles fine, but Go refuses control characters.
func headerSafe(s string) string {
	return strings.Map(func(r rune) rune {
		if r < 32 || r == 127 {
			return ' '
		}
		return r
	}, trim(s, 200))
}

func trim(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Limiter drops repeats of the same text within a window, so a crash loop
// does not flood the phone.
type Limiter struct {
	mu   sync.Mutex
	seen map[string]time.Time
}

func (l *Limiter) Allow(key string, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string]time.Time{}
	}
	now := time.Now()
	for k, t := range l.seen {
		if now.Sub(t) > window {
			delete(l.seen, k)
		}
	}
	if t, ok := l.seen[key]; ok && now.Sub(t) < window {
		return false
	}
	l.seen[key] = now
	return true
}
