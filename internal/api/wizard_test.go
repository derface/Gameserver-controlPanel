package api

import (
	"net/http"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestWizard(t *testing.T) {
	a, st, _ := setupAPI(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/setup", a.csrfOnly(a.setup))
	a.routesWizard(mux)
	c := &client{t: t, h: mux}
	c.do("POST", "/api/setup", `{"name":"chef","password":"geheim-geheim"}`)
	if code, out := c.do("GET", "/api/wizard", ""); code != 200 || out["done"] != false {
		t.Fatal(code, out)
	}
	for _, bad := range []string{"play.example.org:2456", "http://a b", "ä.de"} {
		if code, _ := c.do("PUT", "/api/wizard", `{"public_host":"`+bad+`"}`); code != 400 {
			t.Errorf("%q accepted", bad)
		}
	}
	if code, out := c.do("PUT", "/api/wizard", `{"public_host":"https://Play.Example.org/","done":true}`); code != 200 || out["public_host"] != "play.example.org" || out["done"] != true {
		t.Fatal(code, out)
	}
	st.Read(func(d *store.Data) {
		if d.Settings.PublicHost != "play.example.org" || !d.Settings.WizardDone {
			t.Errorf("%+v", d.Settings.PublicHost)
		}
	})
}
