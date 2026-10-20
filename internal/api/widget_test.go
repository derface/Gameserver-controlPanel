package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
)

func TestWidget(t *testing.T) {
	a, _, _ := setupAPI(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/setup", a.csrfOnly(a.setup))
	a.routesWidget(mux)
	a.routesIntegrations(mux)
	admin := &client{t: t, h: mux}
	admin.do("POST", "/api/setup", `{"name":"chef","password":"geheim-geheim"}`)
	_, v := admin.do("POST", "/api/apikeys", `{"name":"Handy","role":"viewer"}`)
	_, o := admin.do("POST", "/api/apikeys", `{"name":"HA","role":"operator"}`)
	view, op := v["key"].(string), o["key"].(string)

	get := func(url, bearer string) (int, map[string]any) {
		r := httptest.NewRequest("GET", url, nil)
		if bearer != "" {
			r.Header.Set("Authorization", "Bearer "+bearer)
		}
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		var out map[string]any
		_ = json.Unmarshal(w.Body.Bytes(), &out)
		return w.Code, out
	}
	if c, out := get("/api/widget?key="+view, ""); c != 200 || !strings.Contains(out["title"].(string), "0/0 laufen") {
		t.Fatal(c, out)
	}
	if c, out := get("/api/widget?key="+view+"&lang=en", ""); c != 200 || !strings.Contains(out["title"].(string), "running") {
		t.Fatal(c, out)
	}
	if c, _ := get("/api/widget?key="+op, ""); c != 403 {
		t.Fatal("Bediener-Schlüssel im Link erlaubt", c)
	}
	if c, _ := get("/api/widget", op); c != 200 {
		t.Fatal("Bearer", c)
	}
	if c, _ := get("/api/widget?key=quatsch", ""); c != 401 {
		t.Fatal(c)
	}
	if c, _ := get("/api/widget?key="+auth.APIKeyPrefix+"falsch", ""); c != 401 {
		t.Fatal(c)
	}
	if c, _ := admin.do("GET", "/api/widget", ""); c != 200 {
		t.Fatal("Sitzung", c)
	}
}

func TestWidgetLine(t *testing.T) {
	l := widgetLine(widgetServer{Name: "Valheim", State: "online", Icon: "🟢", Players: 3, Max: 10, CPU: 12, RAM: 45}, false)
	if l != "🟢 Valheim 3/10 👤 · CPU 12% · RAM 45%" {
		t.Fatal(l)
	}
	if l := widgetLine(widgetServer{Name: "ARK", State: "aus", Icon: "⚫"}, true); l != "⚫ ARK · off" {
		t.Fatal(l)
	}
}
