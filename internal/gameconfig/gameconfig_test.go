package gameconfig

import (
	"strings"
	"testing"
)

func set(t *testing.T, format, in string, kv ...string) string {
	t.Helper()
	vals, keys := map[string]string{}, []string{}
	for i := 0; i < len(kv); i += 2 {
		vals[kv[i]] = kv[i+1]
		keys = append(keys, kv[i])
	}
	out, err := Set(format, []byte(in), vals, keys)
	if err != nil {
		t.Fatalf("%s: %v", format, err)
	}
	return string(out)
}

func get(t *testing.T, format, in, key string) (string, bool) {
	t.Helper()
	v, ok, err := Get(format, []byte(in), key)
	if err != nil {
		t.Fatalf("%s get %s: %v", format, key, err)
	}
	return v, ok
}

func TestProperties(t *testing.T) {
	in := "#Minecraft server properties\r\npvp=true\r\nmotd=A Minecraft Server\r\n# pvp=false\r\n"
	if v, ok := get(t, "properties", in, "pvp"); !ok || v != "true" {
		t.Fatalf("pvp = %q %v", v, ok)
	}
	out := set(t, "properties", in, "pvp", "false", "hardcore", "true")
	want := "#Minecraft server properties\r\npvp=false\r\nmotd=A Minecraft Server\r\n# pvp=false\r\nhardcore=true\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
	if _, err := Set("properties", []byte(in), map[string]string{"motd": "a\nb"}, []string{"motd"}); err == nil {
		t.Fatal("Zeilenumbruch angenommen")
	}
}

func TestIni(t *testing.T) {
	in := "[ServerSettings]\nServerPassword=\nXPMultiplier=1.0\n[SessionSettings]\nSessionName=ARK\n"
	if v, ok := get(t, "ini", in, "SessionSettings/SessionName"); !ok || v != "ARK" {
		t.Fatal(v)
	}
	if _, ok := get(t, "ini", in, "ServerSettings/SessionName"); ok {
		t.Fatal("Abschnitt ignoriert")
	}
	out := set(t, "ini", in, "ServerSettings/XPMultiplier", "2.5", "ServerSettings/ServerPVE", "True", "/Script/Engine.GameSession/MaxPlayers", "20")
	want := "[ServerSettings]\nServerPVE=True\nServerPassword=\nXPMultiplier=2.5\n[SessionSettings]\nSessionName=ARK\n[/Script/Engine.GameSession]\nMaxPlayers=20\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
}

func TestCvar(t *testing.T) {
	in := "// server.cfg\nhostname \"Alter Name\" // Kommentar\nsv_lan 0\nmp_timelimit 30\n"
	if v, _ := get(t, "cvar", in, "hostname"); v != "Alter Name" {
		t.Fatal(v)
	}
	if v, _ := get(t, "cvar", in, "sv_lan"); v != "0" {
		t.Fatal(v)
	}
	out := set(t, "cvar", in, "hostname", "Neuer Name", "sv_password", "geheim")
	if !strings.Contains(out, "hostname \"Neuer Name\"\n") || !strings.HasSuffix(out, "sv_password \"geheim\"\n") || !strings.Contains(out, "mp_timelimit 30") {
		t.Fatalf("got %q", out)
	}
	if _, err := Set("cvar", []byte(in), map[string]string{"hostname": `a"; quit`}, []string{"hostname"}); err == nil {
		t.Fatal("Anführungszeichen angenommen")
	}
}

func TestXMLProp(t *testing.T) {
	in := `<?xml version="1.0"?>
<ServerSettings>
	<property name="ServerName"	value="My Game Host"/> <!-- name -->
	<property name="ServerPassword"			value=""/>
</ServerSettings>`
	// 7DTD uses tabs between attributes
	if v, ok := get(t, "xmlprop", in, "ServerName"); !ok || v != "My Game Host" {
		t.Fatalf("%q %v", v, ok)
	}
	out := set(t, "xmlprop", in, "ServerName", `Ich & "Du"`, "ServerPassword", "pw")
	if !strings.Contains(out, `value="Ich &amp; &quot;Du&quot;"/> <!-- name -->`) || !strings.Contains(out, `value="pw"`) {
		t.Fatalf("got %s", out)
	}
	if v, _ := get(t, "xmlprop", out, "ServerName"); v != `Ich & "Du"` {
		t.Fatal(v)
	}
	if _, err := Set("xmlprop", []byte(in), map[string]string{"Nope": "x"}, []string{"Nope"}); err == nil {
		t.Fatal("fehlender Schlüssel angenommen")
	}
}

func TestPalworld(t *testing.T) {
	in := "[/Script/Pal.PalGameWorldSettings]\nOptionSettings=(Difficulty=None,DayTimeSpeedRate=1.000000,ServerName=\"Default Palworld Server\",ServerPassword=\"\",bIsPvP=False,CrossplayPlatforms=(Steam,Xbox,PS5,Mac),RCONEnabled=False)\n"
	if v, ok := get(t, "palworld", in, "ServerName"); !ok || v != "Default Palworld Server" {
		t.Fatalf("%q", v)
	}
	if v, _ := get(t, "palworld", in, "CrossplayPlatforms"); v != "(Steam,Xbox,PS5,Mac)" {
		t.Fatalf("%q", v)
	}
	out := set(t, "palworld", in, "ServerName", "Meine Welt", "bIsPvP", "True", "DayTimeSpeedRate", "2.0", "RCONEnabled", "True")
	want := "[/Script/Pal.PalGameWorldSettings]\nOptionSettings=(Difficulty=None,DayTimeSpeedRate=2.0,ServerName=\"Meine Welt\",ServerPassword=\"\",bIsPvP=True,CrossplayPlatforms=(Steam,Xbox,PS5,Mac),RCONEnabled=True)\n"
	if out != want {
		t.Fatalf("got %q", out)
	}
	for _, bad := range []string{"1,2", "x)"} {
		if _, err := Set("palworld", []byte(in), map[string]string{"DayTimeSpeedRate": bad}, []string{"DayTimeSpeedRate"}); err == nil {
			t.Fatalf("%q angenommen", bad)
		}
	}
}

func TestJSON(t *testing.T) {
	in := `{
    "name": "Enshrouded Server",
    "slotCount": 16,
    "voiceChat": false,
    "userGroups": [
        {"name": "Admin", "password": "AdminXXXX"},
        {"name": "Friend", "password": "FriendXXX"}
    ],
    "extra": null
}`
	if v, ok := get(t, "json", in, "userGroups.1.password"); !ok || v != "FriendXXX" {
		t.Fatal(v)
	}
	if v, _ := get(t, "json", in, "slotCount"); v != "16" {
		t.Fatal(v)
	}
	if _, ok := get(t, "json", in, "userGroups"); ok {
		t.Fatal("Array als Wert")
	}
	out := set(t, "json", in, "name", "Mein <Server>", "slotCount", "8", "voiceChat", "true", "userGroups.0.password", "neu")
	for _, w := range []string{`    "name": "Mein <Server>",`, `"slotCount": 8,`, `"voiceChat": true,`, `"password": "neu"`, `"extra": null`} {
		if !strings.Contains(out, w) {
			t.Fatalf("%q fehlt in\n%s", w, out)
		}
	}
	if strings.Index(out, `"name"`) > strings.Index(out, `"slotCount"`) {
		t.Fatal("Reihenfolge verändert")
	}
	for _, bad := range [][2]string{{"slotCount", "acht"}, {"voiceChat", "ja"}, {"fehlt", "x"}, {"userGroups", "x"}} {
		if _, err := Set("json", []byte(in), map[string]string{bad[0]: bad[1]}, []string{bad[0]}); err == nil {
			t.Fatalf("%v angenommen", bad)
		}
	}
}

func TestArmaCfg(t *testing.T) {
	in := "hostname = \"EXAMPLE NAME\";\t// The name of the server\r\npassword = \"\";\r\nmaxPlayers = 60;\r\nclass Missions\r\n{\r\n\tclass m1 { template = \"x\"; difficulty = \"Regular\"; };\r\n\tmaxPlayers = 5;\r\n};\r\n"
	if v, ok := get(t, "armacfg", in, "hostname"); !ok || v != "EXAMPLE NAME" {
		t.Fatalf("%q", v)
	}
	if v, _ := get(t, "armacfg", in, "maxPlayers"); v != "60" {
		t.Fatalf("Klassen-Inhalt gelesen: %q", v)
	}
	out := set(t, "armacfg", in, "hostname", "Davids Server", "maxPlayers", "40", "verifySignatures", "2", "motdInterval", "5")
	for _, w := range []string{"hostname = \"Davids Server\";\t// The name of the server\r\n", "maxPlayers = 40;\r\n", "\tmaxPlayers = 5;", "verifySignatures = 2;\n", "motdInterval = 5;"} {
		if !strings.Contains(out, w) {
			t.Fatalf("%q fehlt in %q", w, out)
		}
	}
	if _, err := Set("armacfg", []byte(in), map[string]string{"hostname": "x\"; exit"}, []string{"hostname"}); err == nil {
		t.Fatal("Anführungszeichen angenommen")
	}
}

func TestJSONCreateAndList(t *testing.T) {
	in := []byte("{\n\t\"name\": \"x\",\n\t\"tags\": [],\n\t\"gameSettings\": {\n\t\t\"a\": 1\n\t}\n}\n")
	out, err := SetKinds("json", in, map[string]string{
		"gameSettings.b": "2.5", "gameSettings.c": "true", "voice.mode": "Global", "tags": "pve, deutsch ,,rp", "gameSettings.a": "3",
	}, []string{"gameSettings.a", "gameSettings.b", "gameSettings.c", "voice.mode", "tags"},
		map[string]string{"gameSettings.b": "number", "gameSettings.c": "bool", "voice.mode": "string", "tags": "list", "gameSettings.a": "number"})
	if err != nil {
		t.Fatal(err)
	}
	for key, want := range map[string]string{"gameSettings.a": "3", "gameSettings.b": "2.5", "gameSettings.c": "true", "voice.mode": "Global", "tags": "pve, deutsch, rp", "name": "x"} {
		if v, ok, _ := Get("json", out, key); !ok || v != want {
			t.Errorf("%s = %q (%v)\n%s", key, v, ok, out)
		}
	}
	if !strings.Contains(string(out), "\"b\": 2.5") || !strings.Contains(string(out), "\"c\": true") || !strings.Contains(string(out), "\t\"tags\": [\n\t\t\"pve\",") {
		t.Fatalf("Typen/Format falsch:\n%s", out)
	}
	if strings.Index(string(out), "\"a\"") > strings.Index(string(out), "\"b\"") {
		t.Fatal("Reihenfolge verloren")
	}
	// without kind a missing value stays an error; a value below a non-object cannot be created
	if _, err := Set("json", in, map[string]string{"neu": "1"}, []string{"neu"}); err == nil {
		t.Fatal("fehlender Wert ohne Art angelegt")
	}
	if _, err := SetKinds("json", in, map[string]string{"name.x": "1"}, []string{"name.x"}, map[string]string{"name.x": "number"}); err == nil {
		t.Fatal("Wert unter einem Text angelegt")
	}
	// clearing a list
	out, _ = SetKinds("json", out, map[string]string{"tags": ""}, []string{"tags"}, map[string]string{"tags": "list"})
	if v, _, _ := Get("json", out, "tags"); v != "" || !strings.Contains(string(out), "\"tags\": []") {
		t.Fatalf("Liste nicht geleert:\n%s", out)
	}
}
