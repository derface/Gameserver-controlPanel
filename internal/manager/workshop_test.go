package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestModIDsFrom(t *testing.T) {
	mods, maps := modIDsFrom("Tolle Mod\n[b]Workshop ID:[/b] 123\n[b]Mod ID:[/b] SpawnSelector\nMod ID: Other; Third\nMap Folder: Raven Creek\n")
	if strings.Join(mods, ",") != "SpawnSelector,Other,Third" || strings.Join(maps, ",") != "Raven Creek" {
		t.Fatalf("%v %v", mods, maps)
	}
}

func TestWorkshopInstall(t *testing.T) {
	var lastQuery string
	steam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case strings.HasPrefix(r.URL.Path, "/browse"):
			lastQuery = r.URL.RawQuery
			fmt.Fprint(w, `<a href="https://steamcommunity.com/sharedfiles/filedetails/?id=2169435993&searchtext=">x</a>
			 <a href="https://steamcommunity.com/sharedfiles/filedetails/?id=2392709985">y</a><a href="https://steamcommunity.com/sharedfiles/filedetails/?id=2169435993">dup</a>`)
		default:
			_ = r.ParseForm()
			var d []map[string]any
			for i := 0; ; i++ {
				id := r.PostForm.Get(fmt.Sprintf("publishedfileids[%d]", i))
				if id == "" {
					break
				}
				desc := "Beschreibung\nMod ID: Mod" + id[len(id)-3:]
				if id == "2392709985" {
					desc += "\nMap Folder: RavenCreek"
				}
				d = append(d, map[string]any{"publishedfileid": id, "result": 1, "title": "Mod " + id, "description": desc, "file_size": "1234",
					"lifetime_subscriptions": 5, "tags": []map[string]string{{"tag": "Build 42"}}})
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"publishedfiledetails": d}})
		}
	}))
	defer steam.Close()
	o1, o2 := wsDetailsURL, wsBrowseURL
	wsDetailsURL, wsBrowseURL = steam.URL+"/details", steam.URL+"/browse"
	defer func() { wsDetailsURL, wsBrowseURL = o1, o2 }()

	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "pz", Name: "Zomboid", Template: "zomboid", MemoryMB: 6144, Vars: map[string]string{"server_name": "servertest", "admin_password": "x12345"}})
		return nil
	})
	ini := filepath.Join(m.ServerDir("pz"), "home", "Zomboid", "Server", "servertest.ini")
	writeFile(t, ini, "PublicName=X\nMods=\\PZ_Map;\\Mod993\nWorkshopItems=2169435993\nMap=Muldraugh, KY\n")

	res, err := m.WorkshopSearch(context.Background(), "pz", "zombie", 1, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Items) != 2 || !res.Items[0].Installed || res.Items[1].Installed || !res.Build42 || !strings.Contains(lastQuery, "Build+42") {
		t.Fatalf("%+v %s", res, lastQuery)
	}
	if _, err := m.WorkshopInstall(context.Background(), "pz", "2392709985", nil); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(ini)
	for _, want := range []string{"WorkshopItems=2169435993;2392709985", `Mods=\PZ_Map;\Mod993;\Mod985`, "Map=RavenCreek;Muldraugh, KY", "PublicName=X"} {
		if !strings.Contains(string(b), want) {
			t.Fatalf("%q fehlt:\n%s", want, b)
		}
	}
	if _, err := m.WorkshopInstall(context.Background(), "pz", "../etc", nil); err == nil {
		t.Fatal("ungültige ID angenommen")
	}
	if _, err := m.WorkshopRemove(context.Background(), "pz", "2169435993"); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(ini)
	if !strings.Contains(string(b), "WorkshopItems=2392709985\n") || !strings.Contains(string(b), `Mods=\PZ_Map;\Mod985`) {
		t.Fatalf("%s", b)
	}
}
