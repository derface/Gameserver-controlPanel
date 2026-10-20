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

func TestSmartTrigger(t *testing.T) {
	for _, c := range []struct {
		kind, event string
		data        map[string]any
		want        string
	}{
		{"players", "player.join", nil, "join"},
		{"status", "server.status", map[string]any{"from": "starting", "to": "running"}, "online"},
		{"status", "server.status", map[string]any{"from": "running", "to": "stopped"}, "offline"},
		{"status", "server.status", map[string]any{"from": "preparing", "to": "stopped"}, ""},
		{"crash", "server.crash", nil, "crash"},
		{"players", "players.count", nil, ""},
	} {
		if got := smartTrigger(c.kind, c.event, c.data); got != c.want {
			t.Errorf("%s/%s: %q, erwartet %q", c.kind, c.event, got, c.want)
		}
	}
	if s := renderSmart(`{"msg":"{{player}} auf {{server}}"}`, smartVars{Player: `Bj"orn`, Server: "Valheim"}, true); !json.Valid([]byte(s)) || !strings.Contains(s, `Bj\"orn auf Valheim`) {
		t.Fatal(s)
	}
}

type fakeCalls struct {
	mu    sync.Mutex
	calls []string
}

func (f *fakeCalls) add(s string) { f.mu.Lock(); f.calls = append(f.calls, s); f.mu.Unlock() }
func (f *fakeCalls) all() string  { f.mu.Lock(); defer f.mu.Unlock(); return strings.Join(f.calls, "\n") }

func TestHueAndHomeAssistant(t *testing.T) {
	var hue fakeCalls
	bridge := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		hue.add(r.Method + " " + r.URL.Path + " " + string(b))
		switch {
		case r.Method == "POST" && r.URL.Path == "/api/":
			io.WriteString(w, `[{"success":{"username":"abc123"}}]`)
		case r.URL.Path == "/api/abc123/lights":
			io.WriteString(w, `{"1":{"name":"Wohnzimmer"},"2":{"name":"Flur"}}`)
		case r.Method == "GET":
			io.WriteString(w, `{"state":{"on":true,"bri":100,"ct":300,"colormode":"ct"}}`)
		default:
			io.WriteString(w, `[{"success":{}}]`)
		}
	}))
	defer bridge.Close()
	host := strings.TrimPrefix(bridge.URL, "http://")
	user, err := HuePair(host)
	if err != nil || user != "abc123" {
		t.Fatal(user, err)
	}
	cfg := store.SmartHome{HueBridge: host, HueUser: user}
	lights, err := HueLights(cfg)
	if err != nil || len(lights) != 2 || lights[0].Name != "Flur" {
		t.Fatal(lights, err)
	}
	r := store.SmartRule{Action: "hue_light", Trigger: "join", Targets: []string{"1"}, Color: "green", Effect: "blink"}
	if err := ValidateSmartRule(&r); err != nil {
		t.Fatal(err)
	}
	if err := hueFlash(cfg, r); err != nil {
		t.Fatal(err)
	}
	if c := hue.all(); !strings.Contains(c, `PUT /api/abc123/lights/1/state {"alert":"select","bri":254,"on":true,"transitiontime":2,"xy":[0.17,0.7]}`) {
		t.Fatal(c)
	}
	time.Sleep(3200 * time.Millisecond) // restore after the blink
	if c := hue.all(); !strings.Contains(c, `"ct":300`) {
		t.Fatal("nicht zurückgestellt:", c)
	}

	// Home Assistant
	var ha fakeCalls
	hass := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer tok" {
			w.WriteHeader(401)
			return
		}
		b, _ := io.ReadAll(r.Body)
		ha.add(r.Method + " " + r.URL.Path + " " + string(b))
		switch r.URL.Path {
		case "/api/":
			io.WriteString(w, `{"message":"API running."}`)
		case "/api/states":
			io.WriteString(w, `[{"entity_id":"light.kueche","attributes":{"friendly_name":"Küche"}},{"entity_id":"sensor.x","attributes":{}},{"entity_id":"media_player.echo","attributes":{"friendly_name":"Echo"}}]`)
		case "/api/services":
			io.WriteString(w, `[{"domain":"notify","services":{"alexa_media_echo":{}}},{"domain":"light","services":{"turn_on":{}}}]`)
		default:
			io.WriteString(w, `[]`)
		}
	}))
	defer hass.Close()
	hc := store.SmartHome{HAURL: hass.URL, HAToken: "tok"}
	if err := HACheck(store.SmartHome{HAURL: hass.URL, HAToken: "falsch"}); err == nil || !strings.Contains(err.Error(), "Token") {
		t.Fatal(err)
	}
	ents, err := HAEntities(hc)
	if err != nil || len(ents) != 3 {
		t.Fatal(ents, err)
	}
	an := store.SmartRule{Action: "ha_announce", Trigger: "join", Service: "notify.alexa_media_echo", Message: "{{player}} ist jetzt auf {{server}}"}
	if err := ValidateSmartRule(&an); err != nil {
		t.Fatal(err)
	}
	if err := haAnnounce(hc, an, smartVars{Player: "Bjorn", Server: "Valheim"}); err != nil {
		t.Fatal(err)
	}
	if c := ha.all(); !strings.Contains(c, `POST /api/services/notify/alexa_media_echo {"data":{"type":"announce"},"message":"Bjorn ist jetzt auf Valheim"}`) {
		t.Fatal(c)
	}
	li := store.SmartRule{ID: "r1", Action: "ha_light", Trigger: "crash", Targets: []string{"light.kueche"}, Color: "red", Effect: "stay"}
	_ = ValidateSmartRule(&li)
	if err := haFlash(hc, li); err != nil {
		t.Fatal(err)
	}
	if c := ha.all(); !strings.Contains(c, `light/turn_on {"brightness":255,"entity_id":["light.kueche"],"rgb_color":[255,0,0]}`) || strings.Contains(c, "scene/create") {
		t.Fatal(c)
	}
	bad := store.SmartRule{Action: "ha_service", Trigger: "join", Service: "script.x", Data: "{kaputt"}
	if ValidateSmartRule(&bad) == nil {
		t.Fatal("kaputtes JSON angenommen")
	}
}
