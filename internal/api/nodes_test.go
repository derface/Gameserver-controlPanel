package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/nodes"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestRemoteServerProxy(t *testing.T) {
	var mu sync.Mutex
	var seen []string // what the node got
	node := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		seen = append(seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Node-User"))
		mu.Unlock()
		if r.Header.Get("Authorization") != "Bearer gpn_k" {
			w.WriteHeader(401)
			return
		}
		writeJSON(w, 200, map[string]string{"id": "s1"})
	}))
	defer node.Close()

	a, st, _ := setupAPI(t)
	a.Nodes = nodes.New(st)
	_ = st.Update(func(d *store.Data) error {
		d.Nodes = []store.Node{{ID: "n1", Name: "Keller", URL: node.URL, Token: "gpn_k", Added: time.Now()}}
		return nil
	})
	ok := func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, map[string]string{"local": "1"}) }
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/servers/{id}", a.need(auth.Viewer, ok))
	mux.HandleFunc("GET /api/servers/{id}/backups/{name}", a.need(auth.Operator, ok))
	mux.HandleFunc("POST /api/servers/{id}/{action}", a.need(auth.Operator, ok))
	mux.HandleFunc("POST /api/setup", a.csrfOnly(a.setup))
	mux.HandleFunc("POST /api/login", a.csrfOnly(a.login))
	a.routesSecurity(mux)
	admin := &client{t: t, h: mux}
	admin.do("POST", "/api/setup", `{"name":"chef","password":"geheim-geheim"}`)
	admin.do("POST", "/api/users", `{"name":"gast","password":"passwort-gast","role":"viewer"}`)
	gast := &client{t: t, h: mux}
	gast.do("POST", "/api/login", `{"name":"gast","password":"passwort-gast"}`)

	if c, out := admin.do("GET", "/api/servers/n1~s1", ""); c != 200 || out["id"] != "n1~s1" {
		t.Fatalf("proxy: %d %v", c, out)
	}
	if c, _ := admin.do("POST", "/api/servers/n1~s1/start", "{}"); c != 200 {
		t.Fatal("start", c)
	}
	if c, _ := gast.do("POST", "/api/servers/n1~s1/start", "{}"); c != 403 {
		t.Error("viewer may start a remote server:", c)
	}
	if c, _ := admin.do("GET", "/api/servers/n9~s1", ""); c != 404 {
		t.Error("unknown node", c)
	}
	for _, p := range []string{"/api/servers/n1~s1/backups/..%2F..%2Fusers", "/api/servers/n1~s1/backups/..", "/api/servers/n1~s1/backups/a%5Cb"} {
		if c, _ := admin.do("GET", p, ""); c != 400 && c != 404 && c != 301 {
			t.Errorf("%s: %d", p, c)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if len(seen) != 2 || seen[0] != "GET /api/servers/s1 chef" || !strings.HasPrefix(seen[1], "POST /api/servers/s1/start") {
		t.Errorf("node saw %q", seen)
	}
}
