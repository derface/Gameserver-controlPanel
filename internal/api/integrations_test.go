package api

import (
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestAPIKeys(t *testing.T) {
	a, st, _ := setupAPI(t)
	_ = st.SaveServer(store.Server{ID: "mc", Name: "Minecraft"})
	_ = st.SaveServer(store.Server{ID: "ark", Name: "ARK"})
	ok := func(w http.ResponseWriter, r *http.Request) {
		writeJSON(w, 200, map[string]string{"user": userOf(r).Name})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/servers/{id}", a.need(auth.Viewer, ok))
	mux.HandleFunc("POST /api/servers/{id}/{action}", a.need(auth.Operator, ok))
	mux.HandleFunc("POST /api/setup", a.csrfOnly(a.setup))
	a.routesIntegrations(mux)
	admin := &client{t: t, h: mux}
	admin.do("POST", "/api/setup", `{"name":"chef","password":"geheim-geheim"}`)

	if c, _ := admin.do("POST", "/api/apikeys", `{"name":"HA","role":"admin"}`); c != 400 {
		t.Fatal("admin-Schlüssel erlaubt", c)
	}
	c, out := admin.do("POST", "/api/apikeys", `{"name":"Home Assistant","role":"operator","servers":["mc","gibtsnicht"]}`)
	key, _ := out["key"].(string)
	if c != 201 || !strings.HasPrefix(key, auth.APIKeyPrefix) {
		t.Fatal(c, out)
	}
	st.Read(func(d *store.Data) {
		if len(d.APIKeys) != 1 || strings.Contains(d.APIKeys[0].Hash, key) || len(d.APIKeys[0].Servers) != 1 {
			t.Fatalf("%+v", d.APIKeys)
		}
	})
	_, list := admin.do("GET", "/api/apikeys", "")
	if strings.Contains(stringify(list), key) || strings.Contains(stringify(list), `"hash":"`+"a") {
		t.Fatal("Schlüssel in der Liste")
	}

	call := func(method, path, k string) int {
		r := httptest.NewRequest(method, path, strings.NewReader(`{}`))
		r.RemoteAddr = "192.0.2.20:1"
		r.Header.Set("Authorization", "Bearer "+k) // no X-Panel: scripts do not send it
		w := httptest.NewRecorder()
		mux.ServeHTTP(w, r)
		return w.Code
	}
	for _, x := range []struct {
		m, p, k string
		want    int
	}{
		{"POST", "/api/servers/mc/start", key, 200},
		{"GET", "/api/servers/mc", key, 200},
		{"POST", "/api/servers/ark/start", key, 404},
		{"GET", "/api/apikeys", key, 403},
		{"POST", "/api/servers/mc/start", key + "x", 401},
		{"POST", "/api/servers/mc/start", auth.APIKeyPrefix + "falsch", 401},
	} {
		if got := call(x.m, x.p, x.k); got != x.want {
			t.Errorf("%s %s: %d, erwartet %d", x.m, x.p, got, x.want)
		}
	}
	var kid string
	st.Read(func(d *store.Data) { kid = d.APIKeys[0].ID })
	if c, _ := admin.do("DELETE", "/api/apikeys/"+kid, ""); c != 200 {
		t.Fatal(c)
	}
	if got := call("GET", "/api/servers/mc", key); got != 401 {
		t.Fatal("gelöschter Schlüssel geht noch", got)
	}

	// webhooks: secret kept on edit unless renewed
	if c, out := admin.do("POST", "/api/webhooks", `{"name":"n8n","url":"http://192.168.1.5:5678/webhook/x","events":["crash","players"]}`); c != 200 || len(out["events"].([]any)) < 10 {
		t.Fatal(c, out)
	}
	var h store.Webhook
	st.Read(func(d *store.Data) { h = d.Webhooks[0] })
	admin.do("PUT", "/api/webhooks/"+h.ID, `{"name":"n8n","url":"http://192.168.1.5:5678/webhook/x","events":["status"]}`)
	st.Read(func(d *store.Data) {
		if d.Webhooks[0].Secret != h.Secret || d.Webhooks[0].Events[0] != "status" || !d.Webhooks[0].Enabled {
			t.Errorf("%+v", d.Webhooks[0])
		}
	})
	if c, _ := admin.do("PUT", "/api/webhooks/"+h.ID, `{"url":"javascript:alert(1)","events":["status"]}`); c != 400 {
		t.Fatal("schlechte Adresse", c)
	}
}

func stringify(v any) string {
	b, _ := json.Marshal(v)
	return string(b)
}
