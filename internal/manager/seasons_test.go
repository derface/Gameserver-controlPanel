package manager

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestSeasons(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "en", Name: "Enshrouded", Template: "enshrouded", MemoryMB: 4096, Vars: map[string]string{}, Created: time.Now().Add(-30 * 24 * time.Hour)})
		return nil
	})
	dir := m.ServerDir("en")
	_ = os.MkdirAll(filepath.Join(dir, "server", "savegame"), 0o755)
	_ = os.WriteFile(filepath.Join(dir, "server", "savegame", "3ad85aea"), []byte("world"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "server", "enshrouded_server.json"), []byte(`{"name":"x"}`), 0o644)
	m.recordPlayers("en", time.Now().Add(-time.Hour), 1, []string{"Bjorn"})
	m.recordPlayers("en", time.Now().Add(-59*time.Minute), 1, []string{"Bjorn"})

	c, err := m.WorldCandidates("en")
	if err != nil || strings.Join(c, ",") != "server/savegame" {
		t.Fatalf("%v %v", c, err)
	}
	if _, err := m.checkWipe("en", []string{"../etc"}); err == nil {
		t.Fatal("path outside accepted")
	}
	if err := m.newSeason(context.Background(), "en", "Frühling", []string{"server/savegame"}, "david"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "server", "savegame")); !os.IsNotExist(err) {
		t.Fatal("world not reset")
	}
	if _, err := os.Stat(filepath.Join(dir, "server", "enshrouded_server.json")); err != nil {
		t.Fatal("config deleted")
	}
	v, _ := m.Seasons("en")
	if v.Current.N != 2 || v.Current.Name != "Frühling" || len(v.Past) != 1 || v.Past[0].Backup == "" || len(v.Past[0].Top) != 1 || v.Past[0].Top[0].Name != "Bjorn" {
		t.Fatalf("%+v", v)
	}
	bs, _ := m.ListBackups("en")
	if len(bs) != 1 || bs[0].Kind != KindSeason {
		t.Fatalf("%+v", bs)
	}
	// play time counts from zero in the new season
	m.recordPlayers("en", time.Now(), 1, []string{"Bjorn"})
	if v, _ = m.Seasons("en"); len(v.Standings) != 1 || v.Standings[0].Seconds != 60 {
		t.Fatalf("%+v", v.Standings)
	}
	// planning
	if err := m.PlanSeason("en", time.Now().Add(time.Minute), "x", []string{"server/savegame"}); err == nil {
		t.Fatal("too soon accepted")
	}
	_ = os.MkdirAll(filepath.Join(dir, "server", "savegame"), 0o755)
	if err := m.PlanSeason("en", time.Now().Add(2*time.Hour), "Sommer", []string{"server/savegame"}); err != nil {
		t.Fatal(err)
	}
	m.checkSeasons(time.Now().Add(2*time.Hour - 30*time.Minute))
	m.checkSeasons(time.Now().Add(2*time.Hour - 9*time.Minute))
	sv, _ := st.Server("en")
	if sv.Seasons.Plan == nil || sv.Seasons.Plan.Warned != 10 {
		t.Fatalf("%+v", sv.Seasons.Plan)
	}
}
