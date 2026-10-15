package manager

import (
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestHistory(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "s", Name: "S", Template: "valheim", MemoryMB: 4096, Vars: map[string]string{}})
		return nil
	})
	now := time.Now().Truncate(time.Minute)
	// 3 hours: running with high RAM, then 1 hour stopped
	for i := 240; i > 0; i-- {
		ts := now.Add(-time.Duration(i) * time.Minute)
		if i > 60 {
			m.appendHistory("s", ts, "r,40.0,3800,4096,2")
		} else {
			m.appendHistory("s", ts, "s,,,,-1")
		}
	}
	v := m.History("s", 24*time.Hour)
	if v.Peak.MemMB != 3800 || v.Peak.Players != 2 || v.Peak.CPU != 40 {
		t.Fatalf("peak %+v", v.Peak)
	}
	var withCPU, up int
	for _, p := range v.Points {
		if p.CPU != nil {
			withCPU++
		}
		if p.Up > 0.99 {
			up++
		}
	}
	if withCPU < 34 || withCPU > 38 || up < 34 {
		t.Fatalf("buckets %d %d of %d", withCPU, up, len(v.Points))
	}
	if len(v.Notes) == 0 {
		t.Fatal("no RAM note")
	}
	days, pct := m.Uptime("s", 30)
	if len(days) != 30 || pct < 74 || pct > 76 {
		t.Fatalf("%d days, %.1f %%", len(days), pct)
	}
}
