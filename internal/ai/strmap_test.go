package ai

import (
	"encoding/json"
	"testing"
)

func TestStrMap(t *testing.T) {
	var p Plan
	err := json.Unmarshal([]byte(`{"action":"change","server":"x","vars":{"max_players":16,"pvp":true},"files":{"0":{"gameSettingsPreset":"Custom","gameSettings":{"experienceCombatFactor":2}}}}`), &p)
	if err != nil {
		t.Fatal(err)
	}
	if p.Vars["max_players"] != "16" || p.Vars["pvp"] != "true" || p.Files["0"]["gameSettings.experienceCombatFactor"] != "2" || p.Files["0"]["gameSettingsPreset"] != "Custom" {
		t.Fatalf("%+v", p)
	}
}
