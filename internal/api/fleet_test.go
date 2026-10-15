package api

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/discord"
	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/nodes"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// A machine with one Minecraft server "s1" (running) and one player.
func fakeFleetNode(t *testing.T, seen *[]string, mu *sync.Mutex) *httptest.Server {
	return httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gpn_k" {
			w.WriteHeader(401)
			return
		}
		b, _ := io.ReadAll(r.Body)
		mu.Lock()
		*seen = append(*seen, r.Method+" "+r.URL.Path+" "+r.Header.Get("X-Node-User")+" "+string(b))
		mu.Unlock()
		switch r.URL.Path {
		case "/api/node/hello":
			writeJSON(w, 200, nodes.Hello{Version: "dev", Servers: 1, Whitelist: []string{"s1"}})
		case "/api/servers":
			writeJSON(w, 200, []map[string]any{{"id": "s1", "name": "Keller-Minecraft", "template_name": "Minecraft Java", "status": "running", "players": 2, "max_players": 10}})
		case "/api/node/public":
			var o pubOpts
			_ = json.Unmarshal(b, &o)
			if _, ok := o.Notes["s1"]; !ok || len(o.Notes) != 1 {
				t.Errorf("node asked for %v", o.Notes)
			}
			writeJSON(w, 200, nodePublicAnswer{
				Servers: []nodePublicServer{{ID: "s1", publicServer: publicServer{Name: "Keller-Minecraft", State: "online", Players: 2, Note: o.Notes["s1"], Address: "keller:25565"}}},
				Events:  []publicEvent{{ID: "e1", Server: "Keller-Minecraft", Name: "Doppelte XP", Start: time.Now().Add(time.Hour), End: time.Now().Add(2 * time.Hour), Kind: "event"}},
				Players: []manager.PlayerTotal{{Name: "Bjorn", Seconds: 3600, Servers: 1}},
			})
		default:
			writeJSON(w, 200, map[string]bool{"ok": true})
		}
	}))
}

func TestFleetPublicAndDiscord(t *testing.T) {
	var seen []string
	var mu sync.Mutex
	node := fakeFleetNode(t, &seen, &mu)
	defer node.Close()

	a, st, dir := setupAPI(t)
	tpls, _ := templates.Load("")
	a.Mgr = manager.New(st, docker.New(dir+"/kein.sock"), tpls, dir)
	a.Nodes = nodes.New(st)
	_ = st.Update(func(d *store.Data) error {
		d.Nodes = []store.Node{{ID: "n1", Name: "Keller", URL: node.URL, Token: "gpn_k", Added: time.Now()}}
		return nil
	})
	a.Nodes.PollNode(context.Background(), store.Node{ID: "n1", Name: "Keller", URL: node.URL, Token: "gpn_k"})

	// status page view: the node's server, event and players come along
	v := pubView{Players: []manager.PlayerTotal{{Name: "bjorn", Seconds: 600, Servers: 1}}}
	a.addRemotePublic(&v, pubOpts{Notes: map[string]string{"n1~s1": "Im Keller", "mc": ""}, Players: true, Horizon: time.Hour * 24})
	if len(v.Servers) != 1 || v.Servers[0].ID != "n1~s1" || v.Servers[0].Note != "Im Keller" || v.Servers[0].Address != "keller:25565" {
		t.Fatalf("servers %+v", v.Servers)
	}
	if len(v.Events) != 1 || v.Events[0].ID != "n1-e1" {
		t.Errorf("events %+v", v.Events)
	}
	if len(v.Players) != 1 || v.Players[0].Seconds != 4200 || v.Players[0].Servers != 2 {
		t.Errorf("players %+v", v.Players)
	}

	// Discord: the node's server is listed and can be started
	c := discord.Config{Servers: []string{"n1~s1"}}
	list := a.cheapServers(c)
	if len(list) != 1 || list[0].State != "online" || list[0].Players != 2 {
		t.Fatalf("discord list %+v", list)
	}
	if !a.fleetIsUp("n1~s1") || !a.fleetCanWhitelist("n1~s1") || !a.fleetHas("n1~s1") || a.fleetHas("n1~zz") {
		t.Error("fleet checks")
	}
	if err := a.fleetPower(context.Background(), "n1~s1", "restart", "Discord: Bjorn"); err != nil {
		t.Fatal(err)
	}
	if err := a.fleetPlayerAction(context.Background(), "n1~s1", "whitelist_add", "Bjorn", "Discord-Whitelist"); err != nil {
		t.Fatal(err)
	}
	mu.Lock()
	defer mu.Unlock()
	want := []string{"POST /api/servers/s1/restart Discord: Bjorn ", `POST /api/servers/s1/players Discord-Whitelist {"action":"whitelist_add","name":"Bjorn"}`}
	for _, w := range want {
		if !slices.Contains(seen, w) {
			t.Errorf("node did not get %q (got %q)", w, strings.Join(seen, " | "))
		}
	}
}
