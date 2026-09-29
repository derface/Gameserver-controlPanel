package templates

import "testing"

func TestLoadAndRender(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	vh, ok := set.Get("valheim")
	if !ok {
		t.Fatal("valheim fehlt")
	}
	vars, err := vh.ResolveVars(map[string]string{"password": "ge\"heim;rm", "crossplay": "true"})
	if err != nil {
		t.Fatal(err)
	}
	if vars["password"] != "geheimrm" {
		t.Fatalf("Sonderzeichen nicht entfernt: %q", vars["password"])
	}
	out := vh.Render(vh.Start, vars, []int{2466, 2467})
	for _, want := range []string{"-port 2466", "-crossplay", "-world \"Midgard\""} {
		if !contains([]string{out}, out) || !containsStr(out, want) {
			t.Fatalf("%q fehlt in %q", want, out)
		}
	}
	if _, err := vh.ResolveVars(map[string]string{}); err == nil {
		t.Fatal("Pflichtfeld Passwort wurde nicht geprüft")
	}
}

func TestParsePorts(t *testing.T) {
	p, err := ParsePorts("27015/udp, 27015/TCP ,7777")
	if err != nil || len(p) != 3 || p[1].Proto != "tcp" || p[2].Proto != "udp" {
		t.Fatalf("falsch: %v %v", p, err)
	}
	if _, err := ParsePorts("abc/udp"); err == nil {
		t.Fatal("ungültiger Port akzeptiert")
	}
}

func containsStr(s, sub string) bool {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return true
		}
	}
	return false
}
