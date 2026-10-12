package manager

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

func settingsMgr(t *testing.T) (*Manager, *store.Store) {
	t.Helper()
	dir := t.TempDir()
	st, _ := store.Open(dir)
	tpls, err := templates.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return New(st, docker.New("/nonexistent.sock"), tpls, dir), st
}

func writeFile(t *testing.T, path, content string) {
	t.Helper()
	_ = os.MkdirAll(filepath.Dir(path), 0o755)
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatal(err)
	}
}

func find(v SettingsView, file int, key string) SettingValue {
	for _, s := range v.Files[file].Settings {
		if s.Key == key {
			return s
		}
	}
	return SettingValue{}
}

func TestSettingsPalworldInitFrom(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.SaveServer(store.Server{ID: "p", Name: "Pal", Template: "palworld", Vars: map[string]string{"max_players": "16", "community": "false"}})
	def := "[/Script/Pal.PalGameWorldSettings]\nOptionSettings=(ExpRate=1.000000,ServerName=\"Default Palworld Server\",ServerPassword=\"\",AdminPassword=\"\",bIsPvP=False,DeathPenalty=All)\n"
	writeFile(t, filepath.Join(m.ServerDir("p"), "server/DefaultPalWorldSettings.ini"), def)
	v, err := m.Settings("p")
	if err != nil {
		t.Fatal(err)
	}
	if !v.Files[0].Exists || !v.Files[0].Editable {
		t.Fatalf("Datei: %+v", v.Files[0])
	}
	if s := find(v, 0, "ServerName"); s.Value != "Default Palworld Server" || !s.Present {
		t.Fatalf("ServerName: %+v", s)
	}
	if s := find(v, 0, "bIsPvP"); s.Value != "false" {
		t.Fatalf("bool: %+v", s)
	}
	if s := find(v, 0, "PalCaptureRate"); s.Present {
		t.Fatal("nicht vorhandener Schlüssel als vorhanden gemeldet")
	}
	changed, err := m.ApplySettings(context.Background(), "p", nil, map[int]map[string]string{0: {
		"ServerName": "Meine Welt", "bIsPvP": "true", "ExpRate": "2", "ServerPassword": "geheim", "DeathPenalty": "Item",
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(changed) != 5 {
		t.Fatalf("geändert: %v", changed)
	}
	b, _ := os.ReadFile(filepath.Join(m.ServerDir("p"), "server/Pal/Saved/Config/LinuxServer/PalWorldSettings.ini"))
	if !strings.Contains(string(b), `OptionSettings=(ExpRate=2,ServerName="Meine Welt",ServerPassword="geheim",AdminPassword="",bIsPvP=True,DeathPenalty=Item)`) {
		t.Fatalf("Datei: %s", b)
	}
	v, _ = m.Settings("p")
	if s := find(v, 0, "ServerPassword"); s.Value != secretMask {
		t.Fatalf("Passwort nicht maskiert: %q", s.Value)
	}
	// unchanged values (incl. the mask) are not a change
	changed, err = m.ApplySettings(context.Background(), "p", nil, map[int]map[string]string{0: {"ServerName": "Meine Welt", "ServerPassword": secretMask}})
	if err != nil || len(changed) != 0 {
		t.Fatalf("keine Änderung erwartet: %v %v", changed, err)
	}
	for _, bad := range []map[string]string{{"ExpRate": "500"}, {"ExpRate": "viel"}, {"DeathPenalty": "Rm"}, {"ServerName": "a\nb"}} {
		if _, err := m.ApplySettings(context.Background(), "p", nil, map[int]map[string]string{0: bad}); err == nil {
			t.Fatalf("%v angenommen", bad)
		}
	}
}

func TestSettingsJSONAndPaths(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.SaveServer(store.Server{ID: "e", Name: "E", Template: "enshrouded", Vars: map[string]string{}})
	v, _ := m.Settings("e")
	// Enshrouded brings a starting file: editable before the first start
	if !v.Files[0].Editable || !find(v, 0, "userGroups.3.password").Present || find(v, 0, "name").Value != "Enshrouded Server" {
		t.Fatalf("Startinhalt fehlt: %+v", v.Files[0])
	}
	if _, err := m.ApplySettings(context.Background(), "e", nil, map[int]map[string]string{0: {"name": "Vorab", "userGroups.1.password": "freund"}}); err != nil {
		t.Fatal(err)
	}
	b, err := os.ReadFile(filepath.Join(m.ServerDir("e"), "server/enshrouded_server.json"))
	if err != nil || !strings.Contains(string(b), `"name": "Vorab"`) || !strings.Contains(string(b), `"password": "freund"`) || !strings.Contains(string(b), `"queryPort": 15637`) {
		t.Fatalf("vorab geschriebene Datei: %v\n%s", err, b)
	}
	// without a starting file a missing JSON file stays locked
	tpl, _ := m.Templates().Get("enshrouded")
	tpl.Config[0].Default = nil
	if d, ok, _ := m.readConfig(store.Server{ID: "leer", Vars: map[string]string{}}, tpl, tpl.Config[0]); ok || d != nil {
		t.Fatal("fehlende JSON-Datei ohne Startinhalt als vorhanden gemeldet")
	}
	writeFile(t, filepath.Join(m.ServerDir("e"), "server/enshrouded_server.json"), `{
	"name": "Enshrouded Server",
	"slotCount": 16,
	"gameSettingsPreset": "Default",
	"userGroups": [{"name": "Admin", "password": "a"}, {"name": "Friend", "password": "f"}]
}`)
	v, _ = m.Settings("e")
	if s := find(v, 0, "userGroups.2.password"); s.Present {
		t.Fatal("dritte Gruppe gibt es nicht")
	}
	if _, err := m.ApplySettings(context.Background(), "e", nil, map[int]map[string]string{0: {"slotCount": "8", "gameSettingsPreset": "Hard", "userGroups.1.password": "neu"}}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(m.ServerDir("e"), "server/enshrouded_server.json"))
	for _, w := range []string{"\t\"slotCount\": 8,", `"gameSettingsPreset": "Hard"`, `"password": "neu"`} {
		if !strings.Contains(string(b), w) {
			t.Fatalf("%q fehlt:\n%s", w, b)
		}
	}

	// Zomboid: file name comes from the server name; Minecraft: file is created
	_ = st.SaveServer(store.Server{ID: "z", Name: "Z", Template: "zomboid", Vars: map[string]string{"server_name": "meinserver", "admin_password": "x"}})
	v, _ = m.Settings("z")
	if v.Files[0].File != "/home/Zomboid/Server/meinserver.ini" {
		t.Fatalf("Pfad: %s", v.Files[0].File)
	}
	_ = st.SaveServer(store.Server{ID: "mc", Name: "MC", Template: "minecraft-java", Vars: map[string]string{}})
	v, _ = m.Settings("mc")
	if !v.Files[0].Editable {
		t.Fatal("server.properties lässt sich nicht anlegen")
	}
	if _, err := m.ApplySettings(context.Background(), "mc", nil, map[int]map[string]string{0: {"pvp": "false", "gamemode": "creative", "level-seed": ""}}); err != nil {
		t.Fatal(err)
	}
	b, _ = os.ReadFile(filepath.Join(m.ServerDir("mc"), "server.properties"))
	if string(b) != "gamemode=creative\npvp=false\n" {
		t.Fatalf("server.properties: %q", b)
	}
}

func TestSettingsVars(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.SaveServer(store.Server{ID: "v", Name: "V", Template: "valheim", Vars: map[string]string{"server_name": "Alt", "world": "Midgard", "password": "geheim1", "public": "1", "crossplay": "false"}})
	v, _ := m.Settings("v")
	for _, x := range v.Vars {
		if x.Key == "password" && x.Value != secretMask {
			t.Fatalf("Passwort im Klartext: %q", x.Value)
		}
	}
	changed, err := m.ApplySettings(context.Background(), "v", map[string]string{"server_name": "Neu", "password": secretMask}, nil)
	if err != nil || len(changed) != 1 {
		t.Fatalf("%v %v", changed, err)
	}
	sv, _ := st.Server("v")
	if sv.Vars["server_name"] != "Neu" || sv.Vars["password"] != "geheim1" {
		t.Fatalf("Variablen: %v", sv.Vars)
	}
	if _, err := m.ApplySettings(context.Background(), "v", map[string]string{"password": ""}, nil); err == nil {
		t.Fatal("Pflichtfeld geleert")
	}
}

func TestEnshroudedGroupRule(t *testing.T) {
	c := templates.ConfigFile{Format: "json", Check: "enshrouded_groups"}
	doc := func(pw ...string) []byte {
		var g []string
		for _, p := range pw {
			g = append(g, `{"name":"x","password":"`+p+`"}`)
		}
		return []byte(`{"userGroups":[` + strings.Join(g, ",") + `]}`)
	}
	for _, ok := range [][]string{{"a", "b", "c", "d"}, {"a", "b", "c", ""}} {
		if err := checkConfig(c, doc(ok...)); err != nil {
			t.Errorf("%v abgelehnt: %v", ok, err)
		}
	}
	for _, bad := range [][]string{{"a", "a", "c", "d"}, {"", "", "c", "d"}, {"a", "", "c", "d"}, {"", "", "", ""}} {
		if err := checkConfig(c, doc(bad...)); err == nil {
			t.Errorf("%v angenommen", bad)
		}
	}
	tpl := templates.ConfigFile{Default: []byte(`{"a":"{{random}}","b":"{{random}}"}`)}
	d := string(tpl.DefaultContent())
	if strings.Contains(d, "{{random}}") || strings.Count(d, `"`) != 8 {
		t.Fatalf("Zufallswerte: %s", d)
	}
	var m map[string]string
	if err := json.Unmarshal([]byte(d), &m); err != nil || m["a"] == m["b"] || len(m["a"]) != 12 {
		t.Fatalf("Zufallswerte: %v %v", m, err)
	}
}
