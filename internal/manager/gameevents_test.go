package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestGameEventLifecycle(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "en", Name: "E", Template: "enshrouded", MemoryMB: 8192, Vars: map[string]string{}})
		return nil
	})
	cfg := filepath.Join(m.ServerDir("en"), "server", "enshrouded_server.json")
	writeFile(t, cfg, `{"name":"E","gameSettingsPreset":"Default","gameSettings":{"experienceCombatFactor":1,"resourceDropStackAmountFactor":1}}`)
	now := time.Now()
	ev, _, err := m.PlanEvent("en", store.GameEvent{Name: "Doppel-XP", Start: now.Add(-time.Second), End: now.Add(time.Hour), Weekly: true},
		nil, map[string]map[string]string{"0": {"gameSettingsPreset": "Custom", "experienceCombatFactor": "2"}})
	if err != nil || len(ev.Changes) != 2 {
		t.Fatalf("%v %+v", err, ev)
	}
	m.checkEvents(t.Context(), now)
	waitFor(t, func() bool { sv, _ := st.Server("en"); return sv.GameEvents[0].State == "aktiv" })
	b, _ := os.ReadFile(cfg)
	if !strings.Contains(string(b), `"experienceCombatFactor": 2`) || !strings.Contains(string(b), `"Custom"`) {
		t.Fatalf("nicht angewendet: %s", b)
	}
	// time is up: old values back, next week planned
	m.checkEvents(t.Context(), now.Add(2*time.Hour))
	waitFor(t, func() bool { sv, _ := st.Server("en"); return sv.GameEvents[0].State == "geplant" })
	b, _ = os.ReadFile(cfg)
	if !strings.Contains(string(b), `"experienceCombatFactor": 1`) || !strings.Contains(string(b), `"Default"`) {
		t.Fatalf("nicht zurückgesetzt: %s", b)
	}
	sv, _ := st.Server("en")
	// same wall clock a week later (across a DST switch that is 167 or 169 hours)
	if !sv.GameEvents[0].Start.Equal(ev.Start.AddDate(0, 0, 7)) {
		d := sv.GameEvents[0].Start.Sub(ev.Start)
		t.Fatalf("nicht auf nächste Woche gelegt: %v", d)
	}
	if err := m.DeleteEvent(t.Context(), "en", ev.ID); err != nil {
		t.Fatal(err)
	}
	if _, _, err := m.PlanEvent("en", store.GameEvent{Start: now, End: now.Add(-time.Hour)}, nil, nil); err == nil {
		t.Fatal("Ende vor Beginn angenommen")
	}
}

func waitFor(t *testing.T, ok func() bool) {
	t.Helper()
	for i := 0; i < 500; i++ {
		if ok() {
			return
		}
		time.Sleep(30 * time.Millisecond)
	}
	t.Fatal("Zeitüberschreitung")
}
