package manager

import (
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestStatsInsights(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "s", Name: "S", Template: "valheim", MemoryMB: 4096, Vars: map[string]string{},
			Tasks: []store.Task{{ID: "t", Action: "restart", At: "20:00", Enabled: true}}})
		return nil
	})
	start := time.Now().Add(-10 * 24 * time.Hour).Truncate(time.Hour)
	for ts := start; ts.Before(time.Now()); ts = ts.Add(10 * time.Minute) {
		c, names := 0, []string{}
		if h := ts.Hour(); h >= 18 && h <= 22 {
			c, names = 2, []string{"Anna", "Bob"}
		}
		m.recordPlayers("s", ts, c, names)
	}
	v, err := m.Stats("s")
	if err != nil {
		t.Fatal(err)
	}
	all := strings.Join(v.Insights, "\n")
	if v.Days < 9 || !strings.Contains(all, "nie jemand online") || !strings.Contains(all, "um 20:00 trifft oft Spieler") {
		t.Fatalf("%d %s", v.Days, all)
	}
	if len(v.Players) != 2 || v.Players[0].Seconds == 0 || v.Players[0].Sessions < 9 {
		t.Fatalf("%+v", v.Players)
	}
	wd := int(time.Now().Add(-24 * time.Hour).Weekday())
	if v.Heat[wd][20] != 2 || v.Heat[wd][3] != 0 || v.HeatMax != 2 {
		t.Fatalf("%v", v.Heat[wd])
	}
}
