package api

import (
	"net/http"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestPushSubscriptions(t *testing.T) {
	a, st, _ := setupAPI(t)
	mux := http.NewServeMux()
	mux.HandleFunc("POST /api/setup", a.csrfOnly(a.setup))
	mux.HandleFunc("POST /api/login", a.csrfOnly(a.login))
	a.routesSecurity(mux)
	a.routesPush(mux)
	admin := &client{t: t, h: mux}
	admin.do("POST", "/api/setup", `{"name":"chef","password":"geheim-geheim"}`)
	admin.do("POST", "/api/users", `{"name":"gast","password":"passwort-gast","role":"viewer"}`)
	gast := &client{t: t, h: mux}
	gast.do("POST", "/api/login", `{"name":"gast","password":"passwort-gast"}`)

	sub := `{"endpoint":"https://fcm.googleapis.com/fcm/send/abc","p256dh":"BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4","auth":"BTBZMqHH6r4Tts7J_aSIgg","device":"Android (App)"}`
	if c, out := gast.do("POST", "/api/push", sub); c != 200 || len(out["subs"].([]any)) != 1 {
		t.Fatal(c, out)
	}
	if c, _ := gast.do("POST", "/api/push", `{"endpoint":"https://evil.example/x","p256dh":"x","auth":"y"}`); c != 400 {
		t.Fatal("fremde Adresse angenommen", c)
	}
	// same device again: still one entry
	gast.do("POST", "/api/push", sub)
	var id string
	st.Read(func(d *store.Data) {
		if len(d.PushSubs) != 1 || d.PushSubs[0].User != "gast" || len(d.PushSubs[0].Kinds) == 0 {
			t.Fatalf("%+v", d.PushSubs)
		}
		id = d.PushSubs[0].ID
	})
	if c, _ := gast.do("PATCH", "/api/push/"+id, `{"kinds":["players","quatsch","crash"]}`); c != 200 {
		t.Fatal(c)
	}
	st.Read(func(d *store.Data) {
		if k := d.PushSubs[0].Kinds; len(k) != 2 || k[0] != "players" || k[1] != "crash" {
			t.Fatal(k)
		}
	})
	// the admin sees only his own devices in the list, but may remove any
	if _, out := admin.do("GET", "/api/push", ""); len(out["subs"].([]any)) != 0 {
		t.Fatal(out)
	}
	other := &client{t: t, h: mux}
	if c, _ := other.do("DELETE", "/api/push/"+id, ""); c != 401 {
		t.Fatal(c)
	}
	if c, _ := gast.do("DELETE", "/api/push/"+id, ""); c != 200 {
		t.Fatal(c)
	}
}
