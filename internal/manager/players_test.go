package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

func TestPlayerParsers(t *testing.T) {
	tpls := mustTemplates(t)
	get := func(id string) *playerProfile {
		tpl, ok := tpls.Get(id)
		if !ok {
			t.Fatalf("Vorlage %s fehlt", id)
		}
		return profileFor(tpl)
	}
	mc := get("minecraft-java")
	if got := mc.Parse([]string{"There are 2 of a max of 20 players online: §eAnna, Bob"}); strings.Join(got, "|") != "Anna|Bob" {
		t.Fatal(got)
	}
	if got := mc.Parse([]string{"There are 0 of a max of 20 players online: "}); len(got) != 0 {
		t.Fatal(got)
	}
	pz := get("zomboid")
	if got := pz.Parse([]string{"LOG  : General     , 1697> Players connected (2):", "-Anna", "-Bob Baumeister", "LOG  : General , other"}); strings.Join(got, "|") != "Anna|Bob Baumeister" {
		t.Fatal(got)
	}
	tr := get("terraria")
	if got := tr.Parse([]string{": playing", "Anna (172.17.0.1:5555)", "1 player connected."}); strings.Join(got, "|") != "Anna" {
		t.Fatal(got)
	}
	fa := get("factorio")
	if got := fa.Parse([]string{"Online players (1):", "  Anna (online)"}); strings.Join(got, "|") != "Anna" {
		t.Fatal(got)
	}
	if get("cs2") == nil || get("valheim").IDLabel != "Steam-ID" {
		t.Fatal("Profile fehlen")
	}
}

func TestValheimLists(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "vh", Name: "V", Template: "valheim", Vars: map[string]string{}})
		return nil
	})
	p := filepath.Join(m.ServerDir("vh"), "saves", "adminlist.txt")
	writeFile(t, p, "// List admin players ID  ONE per line\n76561190000000001\n")
	if _, err := m.PlayerAction(t.Context(), "vh", "list_add", "76561190000000002", "admins"); err != nil {
		t.Fatal(err)
	}
	if _, err := m.PlayerAction(t.Context(), "vh", "list_remove", "76561190000000001", "admins"); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(p)
	if string(b) != "// List admin players ID  ONE per line\n76561190000000002\n" {
		t.Fatalf("%q", b)
	}
	if _, err := m.PlayerAction(t.Context(), "vh", "list_add", "x; rm -rf /", "admins"); err == nil {
		t.Fatal("unsichere ID angenommen")
	}
	v, err := m.Players(t.Context(), "vh")
	if err != nil || !v.Supported || len(v.Lists) != 3 || len(v.Lists[0].Entries) != 1 {
		t.Fatalf("%v %+v", err, v)
	}
	if _, err := m.PlayerAction(t.Context(), "vh", "kick", "Anna", ""); err == nil {
		t.Fatal("Kick bei Valheim angenommen")
	}
}

func mustTemplates(t *testing.T) *templates.Set {
	t.Helper()
	s, err := templates.Load("")
	if err != nil {
		t.Fatal(err)
	}
	return s
}
