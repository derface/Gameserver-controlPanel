package manager

import (
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestAIChangeEnshrouded(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "en", Name: "Enshrouded01", Template: "enshrouded", MemoryMB: 8192, Vars: map[string]string{}})
		return nil
	})
	cat, err := m.SettingsCatalog("en")
	if err != nil || !strings.Contains(cat, "gameSettings.experienceCombatFactor") || !strings.Contains(cat, "wirkt nur wenn gameSettingsPreset=Custom") {
		t.Fatalf("%v\n%s", err, cat)
	}
	if strings.Contains(strings.ToLower(cat), "password\" |") {
		t.Fatal("Passwort im Katalog")
	}
	ch, notes, err := m.CheckChanges("en", nil, map[string]map[string]string{"0": {
		"gameSettingsPreset":                         "Custom",
		"gameSettings.experienceCombatFactor":        "5",
		"gameSettings.resourceDropStackAmountFactor": "2",
		"gibtsnicht": "1",
	}})
	if err != nil {
		t.Fatal(err)
	}
	got := map[string]string{}
	for _, c := range ch {
		got[c.Key] = c.New
	}
	if got["gameSettings.experienceCombatFactor"] != "2" || got["gameSettingsPreset"] != "Custom" || len(notes) != 2 {
		t.Fatalf("%+v %v", ch, notes)
	}
}
