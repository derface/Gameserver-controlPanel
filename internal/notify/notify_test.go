package notify

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

func TestSendNtfy(t *testing.T) {
	var got *http.Request
	var body string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r
		b, _ := io.ReadAll(r.Body)
		body = string(b)
	}))
	defer srv.Close()
	c := Config{NtfyURL: srv.URL + "/panel", NtfyToken: "tk"}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if err := Send(t.Context(), c, Message{Title: "Zomboid", Text: "abgestürzt", Level: "error"}); err != nil {
		t.Fatal(err)
	}
	if got.Header.Get("Title") != "Zomboid" || got.Header.Get("Priority") != "high" || got.Header.Get("Authorization") != "Bearer tk" || body != "abgestürzt" {
		t.Fatalf("%v %q", got.Header, body)
	}
}

func TestDiscordPayloadAndValidate(t *testing.T) {
	c := Config{Discord: "https://example.com/x"}
	if c.Validate() == nil {
		t.Fatal("fremde URL angenommen")
	}
	c = Config{Discord: "https://discord.com/api/webhooks/1/abc", Kinds: []string{"crash", "quatsch"}}
	if err := c.Validate(); err != nil || len(c.Kinds) != 1 || !c.Wants("crash") || c.Wants("update") {
		t.Fatalf("%v %v", err, c.Kinds)
	}
	if !(Config{}).Wants("crash") || (Config{}).Wants("task") {
		t.Fatal("Standardauswahl falsch")
	}
	var payload map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewDecoder(r.Body).Decode(&payload)
		w.WriteHeader(204)
	}))
	defer srv.Close()
	if err := discord(t.Context(), srv.URL, Message{Title: "T", Text: strings.Repeat("x", 5000), Level: "warn"}); err != nil {
		t.Fatal(err)
	}
	e := payload["embeds"].([]any)[0].(map[string]any)
	if e["title"] != "T" || len([]rune(e["description"].(string))) > 3501 {
		t.Fatalf("%v", e)
	}
	var l Limiter
	if !l.Allow("a", time.Minute) || l.Allow("a", time.Minute) || !l.Allow("b", time.Minute) {
		t.Fatal("Limiter")
	}
}
