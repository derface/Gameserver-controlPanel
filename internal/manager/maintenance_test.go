package manager

import (
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestMaintenance(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "s", Name: "S", Template: "valheim", MemoryMB: 4096, Vars: map[string]string{}})
		return nil
	})
	if err := m.SetMaintenance("s", time.Now().Add(-time.Hour), "", false, "d"); err == nil {
		t.Fatal("past end accepted")
	}
	until := time.Now().Add(2 * time.Hour)
	if err := m.SetMaintenance("s", until, "Update", true, "d"); err != nil {
		t.Fatal(err)
	}
	sv, _ := st.Server("s")
	if !sv.InMaintenance(time.Now()) || sv.InMaintenance(until.Add(time.Minute)) {
		t.Fatal("window wrong")
	}
	m.checkMaintenance(time.Now())
	if sv, _ = st.Server("s"); sv.Maintenance == nil {
		t.Fatal("ended too early")
	}
	m.checkMaintenance(until.Add(time.Minute))
	if sv, _ = st.Server("s"); sv.Maintenance != nil {
		t.Fatal("not ended")
	}
	// open-ended
	_ = m.SetMaintenance("s", time.Time{}, "", false, "d")
	sv, _ = st.Server("s")
	if !sv.InMaintenance(time.Now().Add(100 * time.Hour)) {
		t.Fatal("open-ended maintenance ended")
	}
}
