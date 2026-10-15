package api

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/discord"
	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

func wlInteraction(t *testing.T, user string, roles []string, name string) discord.Interaction {
	raw := map[string]any{"type": 2, "token": "x", "member": map[string]any{"roles": roles, "user": map[string]any{"id": user, "username": "u" + user}},
		"data": map[string]any{"name": "whitelist", "options": []map[string]any{{"name": "spielername", "value": name}}}}
	b, _ := json.Marshal(raw)
	var in discord.Interaction
	if err := json.Unmarshal(b, &in); err != nil {
		t.Fatal(err)
	}
	return in
}

func TestWhitelistCommand(t *testing.T) {
	a, st, dir := setupAPI(t)
	tpls, _ := templates.Load("")
	a.Mgr = manager.New(st, docker.New(dir+"/kein.sock"), tpls, dir)
	_ = st.SaveServer(store.Server{ID: "mc", Name: "Minecraft", Template: "minecraft-java", Vars: map[string]string{}})
	c := discord.Config{WLRole: "111111111111111111", WLServers: []string{"mc"}}
	_ = st.Update(func(d *store.Data) error { d.Settings.Discord = c; return nil })
	text := func(r map[string]any) string { return r["data"].(map[string]any)["content"].(string) }

	if r := a.wlCommand(c, wlInteraction(t, "200000000000000001", nil, "Steve")); !strings.Contains(text(r), "Rolle") {
		t.Fatal(text(r))
	}
	if r := a.wlCommand(c, wlInteraction(t, "200000000000000001", []string{c.WLRole}, "St eve")); !strings.Contains(text(r), "Buchstaben") {
		t.Fatal(text(r))
	}
	if r := a.wlCommand(c, wlInteraction(t, "200000000000000001", []string{c.WLRole}, "Steve")); !strings.Contains(text(r), "✅") {
		t.Fatal(text(r))
	}
	if r := a.wlCommand(c, wlInteraction(t, "200000000000000002", []string{c.WLRole}, "steve")); !strings.Contains(text(r), "schon jemand") {
		t.Fatal(text(r))
	}
	// relink changes the name
	a.wlCommand(c, wlInteraction(t, "200000000000000001", []string{c.WLRole}, "Alex"))
	time.Sleep(100 * time.Millisecond)
	var links []store.DiscordLink
	st.Read(func(d *store.Data) { links = d.Settings.DiscordLinks })
	if len(links) != 1 || links[0].Player != "Alex" || !links[0].Active {
		t.Fatalf("%+v", links)
	}
}
