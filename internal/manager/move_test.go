package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestMoveRoundTrip(t *testing.T) {
	a, ast := settingsMgr(t)
	b, bst := settingsMgr(t)
	sv := store.Server{ID: "v1", Name: "Valheim Keller", Template: "valheim", Vars: map[string]string{"server_name": "Midgard", "password": "geheim1"},
		MemoryMB: 4096, Created: time.Now().Add(-48 * time.Hour), CreatedBy: "ai",
		Tasks:  []store.Task{{ID: "t1", Action: "restart", At: "04:00", Enabled: true}},
		Invite: &store.Invite{Token: "abcdefghijklmnop1234", Created: time.Now()}}
	_ = ast.SaveServer(sv)
	dir := a.ServerDir("v1")
	_ = os.MkdirAll(filepath.Join(dir, "saves", "worlds_local"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "saves", "worlds_local", "Midgard.db"), []byte("welt"), 0o644)
	a.Event("v1", "info", "Alter Eintrag")
	_ = os.MkdirAll(a.historyDir("v1"), 0o700)
	_ = os.WriteFile(filepath.Join(a.historyDir("v1"), "2026-10-14.csv"), []byte("1,r,5,100,4096,1\n"), 0o600)
	_ = os.MkdirAll(filepath.Dir(a.statsFile("v1")), 0o700)
	_ = os.WriteFile(a.statsFile("v1"), []byte(`{"players":{}}`), 0o600)

	f, err := b.MoveFile()
	if err != nil {
		t.Fatal(err)
	}
	meta, err := a.ExportMove(t.Context(), "v1", f)
	f.Close()
	if err != nil || meta.Server.Name != "Valheim Keller" || meta.WasRunning {
		t.Fatalf("export: %+v %v", meta, err)
	}
	nv, err := b.ImportMove(f.Name(), "david", false)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(f.Name()); !os.IsNotExist(err) {
		t.Error("package not removed")
	}
	got, _ := bst.Server(nv.ID)
	if got.Name != "Valheim Keller" || got.Vars["server_name"] != "Midgard" || got.CreatedBy != "ai" || len(got.Tasks) != 1 ||
		got.Invite == nil || got.Invite.Token != sv.Invite.Token || !got.Created.Equal(sv.Created.Round(0)) && got.Created.Sub(sv.Created).Abs() > time.Second {
		t.Errorf("server: %+v", got)
	}
	if b, err := os.ReadFile(filepath.Join(b.ServerDir(nv.ID), "saves", "worlds_local", "Midgard.db")); err != nil || string(b) != "welt" {
		t.Errorf("world: %q %v", b, err)
	}
	if _, err := os.Stat(filepath.Join(b.historyDir(nv.ID), "2026-10-14.csv")); err != nil {
		t.Error("history missing")
	}
	if _, err := os.Stat(b.statsFile(nv.ID)); err != nil {
		t.Error("stats missing")
	}
	evs := b.Events(nv.ID)
	var texts []string
	for _, e := range evs {
		texts = append(texts, e.Text)
	}
	all := strings.Join(texts, "|")
	if !strings.Contains(all, "Alter Eintrag") || !strings.Contains(all, "Umgezogen von") {
		t.Errorf("events: %s", all)
	}

	// a broken package creates nothing
	g, _ := b.MoveFile()
	_, _ = g.WriteString("kaputt")
	g.Close()
	if _, err := b.ImportMove(g.Name(), "david", false); err == nil {
		t.Error("broken package accepted")
	}
	if n := len(bst.Servers()); n != 1 {
		t.Errorf("%d servers after broken import", n)
	}
}
