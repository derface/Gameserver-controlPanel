package manager

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"slices"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/notify"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Webhooks: on events the panel sends an HTTP POST with JSON to URLs the
// admin entered (Home Assistant, n8n, own scripts …). Each call carries an
// HMAC-SHA256 signature of the body, so the receiver can check it came
// from this panel. Discord and Slack webhook URLs get their own format.

// WebhookEvents are the kinds a webhook can subscribe to.
func WebhookEvents() []struct{ Key, Label string } {
	out := []struct{ Key, Label string }{
		{"status", "Server-Status geändert (startet, läuft, gestoppt, Fehler)"},
		{"players", "Spieler kommen und gehen"},
	}
	return append(out, notify.Kinds...)
}

func validWebhookEvent(k string) bool {
	for _, e := range WebhookEvents() {
		if e.Key == k {
			return true
		}
	}
	return false
}

// WebhookPayload is the JSON body of every call.
type WebhookPayload struct {
	Event  string         `json:"event"` // e.g. server.status, player.join, server.crash
	Kind   string         `json:"kind"`  // the subscribed kind (status, players, crash …)
	Time   time.Time      `json:"time"`
	Panel  string         `json:"panel"` // host name of this machine
	Server *WebhookServer `json:"server,omitempty"`
	Level  string         `json:"level"` // info | warn | error
	Text   string         `json:"text"`
	Data   map[string]any `json:"data,omitempty"`
	Test   bool           `json:"test,omitempty"`
}

type WebhookServer struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Game string `json:"game"`
}

var hookClient = &http.Client{
	Timeout: 10 * time.Second,
	CheckRedirect: func(*http.Request, []*http.Request) error {
		return http.ErrUseLastResponse // a webhook is called, not followed
	},
}

// hookSlots limits how many calls run at the same time.
var hookSlots = make(chan struct{}, 4)

// ValidateWebhook checks and cleans a webhook before it is saved.
func ValidateWebhook(h *store.Webhook) error {
	h.Name = strings.TrimSpace(h.Name)
	h.URL = strings.TrimSpace(h.URL)
	if h.Name == "" {
		h.Name = "Webhook"
	}
	if len([]rune(h.Name)) > 40 {
		return errors.New("Der Name ist zu lang (höchstens 40 Zeichen)")
	}
	u, err := url.Parse(h.URL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || len(h.URL) > 500 {
		return errors.New("Bitte eine Adresse mit http:// oder https:// angeben")
	}
	if u.User != nil {
		return errors.New("Zugangsdaten gehören nicht in die Adresse")
	}
	var evs []string
	for _, e := range h.Events {
		if validWebhookEvent(e) && !slices.Contains(evs, e) {
			evs = append(evs, e)
		}
	}
	if len(evs) == 0 {
		return errors.New("Bitte mindestens ein Ereignis wählen")
	}
	h.Events = evs
	if h.Secret == "" {
		b := make([]byte, 24)
		_, _ = rand.Read(b)
		h.Secret = hex.EncodeToString(b)
	}
	return nil
}

// webhook sends an event to every webhook that wants it.
func (m *Manager) webhook(kind, event, serverID, level, text string, data map[string]any) {
	m.pushEvent(kind, event, serverID, level, text)
	m.smartEvent(kind, event, serverID, text, data)
	var hooks []store.Webhook
	m.st.Read(func(d *store.Data) {
		for _, h := range d.Webhooks {
			if !h.Enabled || !slices.Contains(h.Events, kind) {
				continue
			}
			if serverID != "" && len(h.Servers) > 0 && !slices.Contains(h.Servers, serverID) {
				continue
			}
			hooks = append(hooks, h)
		}
	})
	if len(hooks) == 0 {
		return
	}
	p := m.hookPayload(kind, event, serverID, level, text, data)
	for _, h := range hooks {
		go m.deliver(h, p, 3)
	}
}

func (m *Manager) hookPayload(kind, event, serverID, level, text string, data map[string]any) WebhookPayload {
	host, _ := os.Hostname()
	p := WebhookPayload{Event: event, Kind: kind, Time: time.Now().UTC(), Panel: host, Level: level, Text: text, Data: data}
	if serverID != "" {
		ws := &WebhookServer{ID: serverID}
		if sv, tpl, err := m.get(serverID); err == nil {
			ws.Name, ws.Game = sv.Name, tpl.Name
		}
		p.Server = ws
	}
	if p.Level == "" {
		p.Level = "info"
	}
	return p
}

// TestWebhook sends a test event right away and reports the result.
func (m *Manager) TestWebhook(h store.Webhook) error {
	p := m.hookPayload("test", "test", "", "info", "Testnachricht vom Bytewise Panel. Wenn du das liest, funktioniert der Webhook.", nil)
	p.Test = true
	return m.deliver(h, p, 1)
}

// hookBody builds the request body: plain JSON, or the message format of
// Discord and Slack when the URL points there.
func hookBody(rawURL string, p WebhookPayload) ([]byte, error) {
	u, _ := url.Parse(rawURL)
	line := p.Text
	if p.Server != nil && p.Server.Name != "" {
		line = "**" + p.Server.Name + "**: " + p.Text
	}
	host := ""
	if u != nil {
		host = strings.ToLower(u.Hostname())
	}
	switch {
	case (host == "discord.com" || host == "discordapp.com" || strings.HasSuffix(host, ".discord.com")) && strings.HasPrefix(u.Path, "/api/webhooks/"):
		return json.Marshal(map[string]any{"content": trimHook(line, 1900), "allowed_mentions": map[string]any{"parse": []string{}}})
	case host == "hooks.slack.com":
		return json.Marshal(map[string]string{"text": trimHook(strings.ReplaceAll(line, "**", "*"), 3000)})
	}
	return json.Marshal(p)
}

func trimHook(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n]) + "…"
	}
	return s
}

// Sign returns the signature header value for a body.
func Sign(secret string, body []byte) string {
	mac := hmac.New(sha256.New, []byte(secret))
	mac.Write(body)
	return "sha256=" + hex.EncodeToString(mac.Sum(nil))
}

// deliver posts the payload, trying up to tries times (after 10 s and 60 s),
// and stores the outcome on the webhook.
func (m *Manager) deliver(h store.Webhook, p WebhookPayload, tries int) error {
	body, err := hookBody(h.URL, p)
	if err != nil {
		return err
	}
	delivery := make([]byte, 8)
	_, _ = rand.Read(delivery)
	waits := []time.Duration{0, 10 * time.Second, 60 * time.Second}
	var code int
	for i := 0; i < tries && i < len(waits); i++ {
		time.Sleep(waits[i])
		hookSlots <- struct{}{}
		code, err = postHook(h, p.Event, hex.EncodeToString(delivery), body)
		<-hookSlots
		if err == nil {
			break
		}
		if code >= 400 && code < 500 && code != 408 && code != 429 {
			break // the receiver refuses; trying again will not help
		}
	}
	_ = m.st.Update(func(d *store.Data) error {
		for i := range d.Webhooks {
			x := &d.Webhooks[i]
			if x.ID != h.ID {
				continue
			}
			x.LastAt, x.LastCode = time.Now(), code
			if err != nil {
				x.LastErr = trimHook(err.Error(), 200)
				x.Fails++
			} else {
				x.LastErr, x.Fails = "", 0
			}
		}
		return nil
	})
	return err
}

func postHook(h store.Webhook, event, delivery string, body []byte) (int, error) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, "POST", h.URL, bytes.NewReader(body))
	if err != nil {
		return 0, err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "BytewisePanel-Webhook")
	req.Header.Set("X-Bytewise-Event", event)
	req.Header.Set("X-Bytewise-Delivery", delivery)
	req.Header.Set("X-Bytewise-Signature", Sign(h.Secret, body))
	resp, err := hookClient.Do(req)
	if err != nil {
		return 0, fmt.Errorf("nicht erreichbar (%v)", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode >= 300 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 200))
		msg := strings.Join(strings.Fields(string(b)), " ")
		if strings.Contains(msg, "<") || !strings.Contains(resp.Header.Get("Content-Type"), "text/plain") && !strings.Contains(resp.Header.Get("Content-Type"), "json") {
			msg = http.StatusText(resp.StatusCode) // no HTML pages in the panel
		}
		return resp.StatusCode, fmt.Errorf("HTTP %d %s", resp.StatusCode, msg)
	}
	_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, 64<<10))
	return resp.StatusCode, nil
}

// statusText names a phase for people.
var statusText = map[string]string{
	"running": "Läuft", "stopped": "Gestoppt", "starting": "Startet", "error": "Fehler",
	"installing": "Wird installiert", "preparing": "Wird vorbereitet", "updating": "Wird aktualisiert",
}

func (m *Manager) hookStatus(id, from, to, msg string) {
	if from == to || to == "" {
		return
	}
	level := "info"
	if to == "error" {
		level = "error"
	}
	txt := statusText[to]
	if txt == "" {
		txt = to
	}
	if msg != "" && to == "error" {
		txt += ": " + msg
	}
	m.webhook("status", "server.status", id, level, txt, map[string]any{"from": from, "to": to})
}
