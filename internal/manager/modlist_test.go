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
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestModList(t *testing.T) {
	steam := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		var d []map[string]any
		for k, v := range r.PostForm {
			if strings.HasPrefix(k, "publishedfileids") {
				d = append(d, map[string]any{"publishedfileid": v[0], "title": "Titel " + v[0], "time_updated": time.Now().Add(48 * time.Hour).Unix()})
			}
		}
		_ = json.NewEncoder(w).Encode(map[string]any{"response": map[string]any{"publishedfiledetails": d}})
	}))
	defer steam.Close()
	old := wsURL
	wsURL = steam.URL
	defer func() { wsURL = old }()

	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "pz", Name: "Zomboid", Template: "zomboid", MemoryMB: 6144, Vars: map[string]string{"server_name": "servertest", "admin_password": "x12345"}})
		return nil
	})
	dir := m.ServerDir("pz")
	ws := filepath.Join(dir, "server", "steamapps", "workshop", "content", "108600")
	writeFile(t, filepath.Join(ws, "2896041179", "mods", "AuthenticZCurrent", "42", "mod.info"), "name=Authentic Z - Current\nid=Authentic Z - Current\nmodversion=1.2\n")
	writeFile(t, filepath.Join(ws, "2896041179", "mods", "AuthenticZCurrent", "common", "media", "x.lua"), "x")
	writeFile(t, filepath.Join(ws, "3740052292", "mods", "Dogs", "42", "mod.info"), "name=Companion Dogs\nid=CompanionDogs\n")
	writeFile(t, filepath.Join(ws, "1111111", "x.bin"), "x")
	writeFile(t, filepath.Join(dir, "home", "Zomboid", "Server", "servertest.ini"),
		"PublicName=Bytewise\nMods=\\Authentic Z - Current;\\Fehlt\nWorkshopItems=2896041179;3740052292;9999999\n")
	writeFile(t, filepath.Join(dir, "server", "BepInEx", "plugins", "Valheim+", "ValheimPlus.dll"), "dll")
	writeFile(t, filepath.Join(dir, "server", "BepInEx", "plugins", "Valheim+", "manifest.json"), `{"name":"ValheimPlus","version_number":"0.9.9"}`)
	writeFile(t, filepath.Join(dir, "server", "Pal", "Content", "Paks", "~mods", "Bigger.pak"), "pak")

	inv, err := m.ModList(context.Background(), "pz")
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]ModEntry{}
	for _, e := range inv.Mods {
		by[e.Name] = e
	}
	if e := by["Authentic Z - Current"]; e.Enabled == nil || !*e.Enabled || e.Version != "1.2" || e.Workshop != "2896041179" || !e.UpdateHint || e.URL == "" {
		t.Fatalf("AZ %+v", e)
	}
	if e := by["Companion Dogs"]; e.State != "inaktiv" {
		t.Fatalf("Dogs %+v", e)
	}
	if e := by["Fehlt"]; e.State != "fehlt" {
		t.Fatalf("Fehlt %+v", e)
	}
	if e := by["Titel 9999999"]; e.State != "nicht_geladen" {
		t.Fatalf("nicht geladen %+v", inv.Mods)
	}
	if e := by["Titel 1111111"]; e.Source != "Steam Workshop" {
		t.Fatalf("Workshop-Titel %+v", inv.Mods)
	}
	if e := by["ValheimPlus"]; e.Version != "0.9.9" || e.Key != "bep:Valheim+" {
		t.Fatalf("%+v", e)
	}
	if inv.Mods[0].State != "fehlt" {
		t.Fatalf("Probleme zuerst: %+v", inv.Mods[0])
	}

	// switching
	if _, err := m.ToggleMod(context.Background(), "pz", "bep:Valheim+", false); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "server", "BepInEx", "plugins", "Valheim+", "ValheimPlus.dll.off")); err != nil {
		t.Fatal("dll nicht umbenannt")
	}
	if _, err := m.ToggleMod(context.Background(), "pz", "pak:server/Pal/Content/Paks/~mods/Bigger.pak", false); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ToggleMod(context.Background(), "pz", "pak:../../etc/passwd.pak", false); err == nil {
		t.Fatal("Pfad außerhalb erlaubt")
	}
	if _, err := m.ToggleMod(context.Background(), "pz", "pz:CompanionDogs", true); err != nil {
		t.Fatal(err)
	}
	if _, err := m.ToggleMod(context.Background(), "pz", "pz:Fehlt", false); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(dir, "home", "Zomboid", "Server", "servertest.ini"))
	if !strings.Contains(string(b), "Mods=\\Authentic Z - Current;\\CompanionDogs\n") || !strings.Contains(string(b), "PublicName=Bytewise") {
		t.Fatalf("%s", b)
	}
}
