package manager

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestWorkshopGeneric(t *testing.T) {
	steam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"publishedfiledetails": []any{}}})
	}))
	defer steam.Close()
	o := wsURL
	wsURL = steam.URL
	defer func() { wsURL = o }()

	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "gm", Name: "GMod", Template: "gmod", MemoryMB: 4096, Vars: map[string]string{}})
		return nil
	})
	ctx := context.Background()
	info, err := m.WorkshopInfoFor(ctx, "gm")
	if err != nil || !info.Available || info.App != 4000 || !info.Recipe || info.Mode != "steamcmd" {
		t.Fatalf("%+v %v", info, err)
	}
	if _, err := m.WorkshopInstall(ctx, "gm", "1234567890", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := m.WorkshopInstall(ctx, "gm", "2222222222", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(m.wsListFile("gm"))
	if string(b) != "app 4000\n1234567890\n2222222222\n" {
		t.Fatalf("%q", b)
	}
	if hook, err := os.ReadFile(filepath.Join(m.ServerDir("gm"), ".gp-workshop-after.sh")); err != nil || !strings.Contains(string(hook), "resource.AddWorkshop") {
		t.Fatalf("Hook fehlt: %v", err)
	}
	// one item arrived, one was refused
	_ = os.MkdirAll(filepath.Join(m.ServerDir("gm"), "server", "steamapps", "workshop", "content", "4000", "1234567890"), 0o755)
	writeFile(t, filepath.Join(m.ServerDir("gm"), ".gp-workshop-status"), "1234567890 ok\n2222222222 fehler ERROR! Download item 2222222222 failed (Access Denied).\n")
	inv, err := m.ModList(ctx, "gm")
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]ModEntry{}
	for _, e := range inv.Mods {
		got[e.Workshop] = e
	}
	if e := got["2222222222"]; e.State != "fehler" || !strings.Contains(e.Note, "Steam-Konto") || e.Key != "ws:2222222222" {
		t.Fatalf("%+v", e)
	}
	if e := got["1234567890"]; e.Key != "ws:1234567890" || e.Enabled == nil || !*e.Enabled || e.Advice {
		t.Fatalf("%+v", e)
	}
	// switching off takes it out of the list, the files stay
	if _, err := m.ToggleMod(ctx, "gm", "ws:1234567890", false); err != nil {
		t.Fatal(err)
	}
	inv, _ = m.ModList(ctx, "gm")
	for _, e := range inv.Mods {
		if e.Workshop == "1234567890" && (e.State != "inaktiv" || *e.Enabled) {
			t.Fatalf("%+v", e)
		}
	}
	if _, err := m.WorkshopRemove(ctx, "gm", "2222222222"); err != nil {
		t.Fatal(err)
	}
	if _, ids := m.readWsList("gm"); len(ids) != 0 {
		t.Fatalf("%v", ids)
	}
}

func TestGameAppLookup(t *testing.T) {
	calls := 0
	steam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		switch {
		case strings.Contains(r.URL.Path, "storesearch"):
			_ = json.NewEncoder(w).Encode(map[string]any{"items": []map[string]any{
				{"id": 896661, "name": "Valheim Dedicated Server", "type": "app"},
				{"id": 892970, "name": "Valheim", "type": "app"}}})
		default:
			id := r.URL.Query().Get("appids")
			cats := []map[string]int{{"id": 1}}
			if id == "892970" {
				cats = append(cats, map[string]int{"id": 30})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{id: map[string]any{"success": true, "data": map[string]any{"name": "Valheim", "categories": cats}}})
		}
	}))
	defer steam.Close()
	o1, o2 := steamStoreSearch, steamStoreDetails
	steamStoreSearch, steamStoreDetails = steam.URL+"/storesearch/", steam.URL+"/appdetails"
	defer func() { steamStoreSearch, steamStoreDetails = o1, o2 }()

	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "vh", Name: "V", Template: "valheim", MemoryMB: 4096, Vars: map[string]string{}})
		return nil
	})
	info, err := m.WorkshopInfoFor(context.Background(), "vh")
	if err != nil || !info.Available || info.App != 892970 || info.Recipe {
		t.Fatalf("%+v %v", info, err)
	}
	n := calls
	if info, _ = m.WorkshopInfoFor(context.Background(), "vh"); info.App != 892970 || calls != n {
		t.Fatalf("Cache nicht benutzt: %+v %d/%d", info, calls, n)
	}
}
