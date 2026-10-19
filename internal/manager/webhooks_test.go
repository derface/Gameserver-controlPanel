package manager

import (
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestWebhooks(t *testing.T) {
	m, st := settingsMgr(t)
	var mu sync.Mutex
	var got []WebhookPayload
	var sigOK bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		var p WebhookPayload
		_ = json.Unmarshal(b, &p)
		mu.Lock()
		got = append(got, p)
		sigOK = r.Header.Get("X-Bytewise-Signature") == Sign("geheim", b) && r.Header.Get("X-Bytewise-Event") == p.Event
		mu.Unlock()
	}))
	defer srv.Close()

	h := store.Webhook{ID: "w1", URL: srv.URL + "/hook", Events: []string{"status", "crash", "quatsch", "crash"}, Enabled: true}
	if err := ValidateWebhook(&h); err != nil || len(h.Events) != 2 || h.Secret == "" || h.Name != "Webhook" {
		t.Fatalf("%v %+v", err, h)
	}
	h.Secret = "geheim"
	bad := store.Webhook{URL: "ftp://x", Events: []string{"crash"}}
	if ValidateWebhook(&bad) == nil {
		t.Fatal("ftp angenommen")
	}
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "en", Name: "Enshrouded", Template: "enshrouded", Vars: map[string]string{}})
		d.Webhooks = []store.Webhook{h, {ID: "w2", URL: srv.URL, Secret: "x", Events: []string{"crash"}, Servers: []string{"anderer"}, Enabled: true}}
		return nil
	})

	m.hookStatus("en", "starting", "running", "")
	m.EventKind("en", "error", "crash", "Abgestürzt")
	m.EventKind("en", "info", "backup", "Backup fertig") // not subscribed
	deadline := time.Now().Add(3 * time.Second)
	for {
		mu.Lock()
		n := len(got)
		mu.Unlock()
		if n >= 2 || time.Now().After(deadline) {
			break
		}
		time.Sleep(20 * time.Millisecond)
	}
	time.Sleep(100 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(got) != 2 || !sigOK {
		t.Fatalf("%d Aufrufe, Signatur %v: %+v", len(got), sigOK, got)
	}
	events := got[0].Event + "," + got[1].Event
	if !strings.Contains(events, "server.status") || !strings.Contains(events, "server.crash") {
		t.Fatal(events)
	}
	for _, p := range got {
		if p.Server == nil || p.Server.Name != "Enshrouded" || p.Server.Game != "Enshrouded" {
			t.Fatalf("%+v", p.Server)
		}
	}
	st.Read(func(d *store.Data) {
		if d.Webhooks[0].LastCode != 200 || d.Webhooks[0].LastErr != "" {
			t.Errorf("%+v", d.Webhooks[0])
		}
	})

	// Discord URLs get Discord's format
	b, _ := hookBody("https://discord.com/api/webhooks/1/abc", WebhookPayload{Text: "läuft", Server: &WebhookServer{Name: "Valheim"}})
	if !strings.Contains(string(b), `"content":"**Valheim**: läuft"`) {
		t.Fatal(string(b))
	}
}
