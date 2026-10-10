package api

import (
	"encoding/json"
	"io/fs"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"

	"github.com/derface/Gameserver-controlPanel/internal/audit"
	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

type client struct {
	t   *testing.T
	h   http.Handler
	jar []*http.Cookie
}

func (c *client) do(method, path, body string) (int, map[string]any) {
	c.t.Helper()
	r := httptest.NewRequest(method, path, strings.NewReader(body))
	r.RemoteAddr = "192.0.2.10:5555"
	r.Header.Set("X-Panel", "1")
	for _, k := range c.jar {
		r.AddCookie(k)
	}
	w := httptest.NewRecorder()
	c.h.ServeHTTP(w, r)
	for _, k := range w.Result().Cookies() {
		c.jar = []*http.Cookie{k}
	}
	var out map[string]any
	_ = json.Unmarshal(w.Body.Bytes(), &out)
	return w.Code, out
}

func setupAPI(t *testing.T) (*API, *store.Store, string) {
	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	a := &API{Auth: auth.New(st), Store: st, Docker: docker.New(dir + "/kein.sock"), Web: fstest.MapFS{"index.html": {Data: []byte("x")}}, Audit: audit.Open(dir)}
	var _ fs.FS = a.Web
	return a, st, dir
}

func TestRolesAndScope(t *testing.T) {
	a, st, _ := setupAPI(t)
	_ = st.SaveServer(store.Server{ID: "mc", Name: "Minecraft"})
	_ = st.SaveServer(store.Server{ID: "ark", Name: "ARK"})
	ok := func(w http.ResponseWriter, r *http.Request) {
		if r.PathValue("action") == "command" {
			Note(r, "say hallo")
		}
		writeJSON(w, 200, map[string]string{"user": userOf(r).Name})
	}
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/servers/{id}", a.need(auth.Viewer, ok))
	mux.HandleFunc("POST /api/servers/{id}/{action}", a.need(auth.Operator, ok))
	mux.HandleFunc("DELETE /api/servers/{id}", a.need(auth.Admin, ok))
	mux.HandleFunc("POST /api/setup", a.csrfOnly(a.setup))
	mux.HandleFunc("POST /api/login", a.csrfOnly(a.login))
	a.routesSecurity(mux)

	admin := &client{t: t, h: mux}
	if c, _ := admin.do("POST", "/api/setup", `{"name":"chef","password":"geheim-geheim"}`); c != 200 {
		t.Fatal("setup", c)
	}
	for _, u := range []string{
		`{"name":"op","password":"passwort-op1","role":"operator","servers":["mc"]}`,
		`{"name":"gast","password":"passwort-gast","role":"viewer"}`,
	} {
		if c, out := admin.do("POST", "/api/users", u); c != 201 {
			t.Fatal(c, out)
		}
	}
	op, gast := &client{t: t, h: mux}, &client{t: t, h: mux}
	op.do("POST", "/api/login", `{"name":"op","password":"passwort-op1"}`)
	gast.do("POST", "/api/login", `{"name":"gast","password":"passwort-gast"}`)

	cases := []struct {
		c            *client
		method, path string
		want         int
	}{
		{admin, "DELETE", "/api/servers/ark", 200},
		{op, "GET", "/api/servers/mc", 200},
		{op, "GET", "/api/servers/ark", 404}, // not his server
		{op, "POST", "/api/servers/mc/command", 200},
		{op, "POST", "/api/servers/ark/start", 404},
		{op, "DELETE", "/api/servers/mc", 403},
		{op, "GET", "/api/users", 403},
		{gast, "GET", "/api/servers/ark", 200},
		{gast, "POST", "/api/servers/mc/start", 403},
		{gast, "GET", "/api/audit", 403},
		{&client{t: t, h: mux}, "GET", "/api/servers/mc", 401},
	}
	for _, x := range cases {
		if c, out := x.c.do(x.method, x.path, `{}`); c != x.want {
			t.Errorf("%s %s: %d, erwartet %d (%v)", x.method, x.path, c, x.want, out)
		}
	}

	// without the X-Panel header changing requests are refused
	r := httptest.NewRequest("POST", "/api/servers/mc/start", nil)
	r.AddCookie(admin.jar[0])
	w := httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	if w.Code != 403 {
		t.Errorf("ohne X-Panel: %d", w.Code)
	}

	// the protocol
	entries := a.Audit.Recent(100, nil)
	find := func(user, action string) *audit.Entry {
		for i := range entries {
			if entries[i].User == user && entries[i].Action == action {
				return &entries[i]
			}
		}
		return nil
	}
	if e := find("op", "Konsolenbefehl"); e == nil || e.Target != "Minecraft" || e.Detail != "say hallo" || !e.OK {
		t.Errorf("Konsolenbefehl: %+v", e)
	}
	if e := find("chef", "Server gelöscht"); e == nil || e.Target != "ARK" {
		t.Errorf("Löschen: %+v", e)
	}
	found := false
	for _, e := range entries {
		if e.Action == "Benutzer angelegt" && e.Detail == "op; Rolle Betreiber, 1 Server" {
			found = true
		}
		if strings.Contains(e.Detail, "passwort-") {
			t.Errorf("Passwort im Protokoll: %+v", e)
		}
	}
	if !found {
		t.Errorf("Benutzer angelegt fehlt: %+v", entries)
	}
	if e := find("op", "Angemeldet"); e == nil || e.IP != "192.0.2.10" {
		t.Errorf("Anmeldung: %+v", e)
	}

	// admin changes the operator's servers; the operator sees the change at once
	if c, out := admin.do("PATCH", "/api/users/op", `{"servers":["ark"]}`); c != 200 {
		t.Fatal(c, out)
	}
	if c, _ := op.do("GET", "/api/servers/mc", ""); c != 404 {
		t.Errorf("alte Freigabe gilt noch: %d", c)
	}
	// own account cannot be demoted or removed
	if c, _ := admin.do("PATCH", "/api/users/chef", `{"role":"viewer"}`); c != 400 {
		t.Errorf("selbst herabgestuft: %d", c)
	}
	if c, _ := admin.do("DELETE", "/api/users/chef", ""); c != 400 {
		t.Errorf("selbst gelöscht: %d", c)
	}
	if c, _ := admin.do("DELETE", "/api/users/gast", ""); c != 200 {
		t.Errorf("Löschen: %d", c)
	}
	if c, _ := gast.do("GET", "/api/servers/mc", ""); c != 401 {
		t.Errorf("gelöschter Benutzer noch angemeldet: %d", c)
	}
	r = httptest.NewRequest("GET", "/api/audit?user=op&q=hallo", nil)
	r.AddCookie(admin.jar[0])
	w = httptest.NewRecorder()
	mux.ServeHTTP(w, r)
	var list []audit.Entry
	if err := json.Unmarshal(w.Body.Bytes(), &list); err != nil || len(list) != 1 || list[0].Action != "Konsolenbefehl" {
		t.Errorf("Protokoll-Filter: %s", w.Body.String())
	}
}

func TestLoginWithCodeAPI(t *testing.T) {
	a, _, _ := setupAPI(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/setup", a.csrfOnly(a.setup))
	mux.HandleFunc("POST /api/login", a.csrfOnly(a.login))
	a.routesSecurity(mux)
	c := &client{t: t, h: mux}
	c.do("POST", "/api/setup", `{"name":"chef","password":"geheim-geheim"}`)
	code, out := c.do("POST", "/api/me/2fa/setup", "")
	if code != 200 || out["secret"] == "" {
		t.Fatal(code, out)
	}
	if code, _ := c.do("POST", "/api/me/2fa/enable", `{"code":"000000"}`); code != 400 {
		t.Fatal("falscher Code", code)
	}
	if code, out := c.do("GET", "/api/me", ""); code != 200 || out["two_factor"] != false || out["role"] != "admin" {
		t.Fatal(code, out)
	}
	// password login now asks for the code
	fresh := &client{t: t, h: mux}
	if code, out := fresh.do("POST", "/api/login", `{"name":"chef","password":"geheim-geheim"}`); code != 200 || out["need_code"] != nil {
		t.Fatal("ohne 2FA: ", code, out)
	}
	if code, out := fresh.do("POST", "/api/login", `{"name":"chef","password":"falsch"}`); code != 401 {
		t.Fatal(code, out)
	}
	e := a.Audit.Recent(1, nil)
	if len(e) != 1 || e[0].Action != "Anmeldung fehlgeschlagen" || e[0].OK {
		t.Fatalf("%+v", e)
	}
}
