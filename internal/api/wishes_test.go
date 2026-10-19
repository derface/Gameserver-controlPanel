package api

import (
	"net/http"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestWishes(t *testing.T) {
	a, st, _ := setupAPI(t)
	mux := http.NewServeMux()
	a.routesWishes(mux)
	visitor := func(ip string) *client { return &client{t: t, h: mux} }
	c := visitor("")
	if code, _ := c.do("GET", "/api/public/wishes", ""); code != 404 {
		t.Fatal("wish list without status page", code)
	}
	_ = st.Update(func(d *store.Data) error {
		d.Settings.Status.Enabled, d.Settings.Status.Wishes = true, true
		return nil
	})
	if code, out := c.do("POST", "/api/public/wishes", `{"title":"  Valheim\nmit Mods ","kind":"mod","by":"Bjorn"}`); code != 201 {
		t.Fatal(code, out)
	}
	if code, _ := c.do("POST", "/api/public/wishes", `{"title":"valheim mit mods"}`); code != 400 {
		t.Error("duplicate accepted")
	}
	if code, _ := c.do("POST", "/api/public/wishes", `{"title":"Spam","website":"http://x"}`); code != 201 {
		t.Error("honeypot should pretend success")
	}
	_, out := c.do("GET", "/api/public/wishes", "")
	list := out["wishes"].([]any)
	if len(list) != 1 {
		t.Fatalf("%v", out)
	}
	w0 := list[0].(map[string]any)
	if w0["title"] != "Valheim mit Mods" || w0["votes"].(float64) != 1 || w0["voted"] != true || w0["kind"] != "mod" {
		t.Errorf("%v", w0)
	}
	id := w0["id"].(string)
	// same visitor: second click takes the vote back
	if _, out := c.do("POST", "/api/public/wishes/"+id+"/vote", ""); out["votes"].(float64) != 0 || out["voted"] != false {
		t.Errorf("unvote %v", out)
	}
	if _, out := c.do("POST", "/api/public/wishes/"+id+"/vote", ""); out["votes"].(float64) != 1 {
		t.Errorf("vote %v", out)
	}
	// rate limit: 3 wishes per hour and address (one used above)
	c.do("POST", "/api/public/wishes", `{"title":"Zwei"}`)
	c.do("POST", "/api/public/wishes", `{"title":"Drei"}`)
	if code, _ := c.do("POST", "/api/public/wishes", `{"title":"Vier"}`); code != 429 {
		t.Error("no limit", code)
	}
	st.Read(func(d *store.Data) {
		for _, w := range d.Wishes {
			if w.IP == "" || len(w.Voters) > 1 || (len(w.Voters) == 1 && len(w.Voters[0]) != 24) {
				t.Errorf("stored %+v", w)
			}
		}
	})
}
