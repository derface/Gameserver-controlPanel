package manager

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// sample copies an example config from gameconfig/testdata with its
// placeholders filled in.
func sample(t *testing.T, m *Manager, st *store.Store, id, tpl, rel, file string) {
	t.Helper()
	b, err := os.ReadFile(filepath.Join("../gameconfig/testdata/samples", file))
	if err != nil {
		t.Fatal(err)
	}
	b = regexp.MustCompile(`\{\{([A-Za-z0-9_$.]+)\}\}`).ReplaceAllFunc(b, func(m []byte) []byte {
		n := strings.ToLower(string(m))
		switch {
		case strings.Contains(n, "name") || strings.Contains(n, "password") || strings.Contains(n, "description") || strings.Contains(n, "ip") || strings.Contains(n, "url") || strings.Contains(n, "region") || strings.Contains(n, "tags"):
			return []byte("text")
		case strings.HasPrefix(n, "{{b") || strings.Contains(n, "enable"):
			return []byte("False")
		}
		return []byte("2")
	})
	_ = st.SaveServer(store.Server{ID: id, Name: id, Template: tpl, Vars: map[string]string{}})
	writeFile(t, filepath.Join(m.ServerDir(id), rel), string(b))
}

func cards(v SettingsView) map[string]SettingValue {
	out := map[string]SettingValue{}
	for _, f := range v.Files {
		for _, s := range f.Settings {
			out[f.File+"|"+s.Key] = s
		}
	}
	return out
}

func TestAutoSettings(t *testing.T) {
	m, st := settingsMgr(t)
	pal := "server/Pal/Saved/Config/LinuxServer/PalWorldSettings.ini"
	sample(t, m, st, "pal", "palworld", pal, "palworldtemplatesettings.ini")
	sample(t, m, st, "abi", "abiotic", "server/AbioticFactor/Saved/Config/WindowsServer/SandboxSettings.ini", "abiotic-factorsandboxsettings.ini")
	sample(t, m, st, "css", "lgsm-css", "server/cstrike/cfg/server.cfg", "counter-strike-sourceserver.cfg")

	// Palworld: every option becomes a card, ports and IP stay hidden, AMP describes them
	v, _ := m.Settings("pal")
	c := cards(v)
	f := "/" + pal + "|"
	if len(c) < 50 {
		t.Fatalf("nur %d Kacheln", len(c))
	}
	for _, k := range []string{"PublicPort", "RCONPort", "PublicIP"} {
		if _, ok := c[f+k]; ok {
			t.Errorf("%s wird angezeigt", k)
		}
	}
	if s := c[f+"bEnableInvaderEnemy"]; s.Type != "bool" || s.Help == "" || s.Group == "" || s.True != "True" {
		t.Errorf("Raids: %+v", s)
	}
	if s := c[f+"ServerName"]; s.Help == "" || s.Group == "" { // template setting got AMP's description
		t.Errorf("Servername: %+v", s)
	}
	for _, s := range c {
		if s.Group == "" {
			t.Errorf("ohne Reiter: %s", s.Key)
		}
	}
	// saving an automatic card writes the file
	if _, err := m.ApplySettings(context.Background(), "pal", nil, map[int]map[string]string{0: {"bEnableInvaderEnemy": "true", "GuildPlayerMaxNum": "7"}}); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(m.ServerDir("pal"), pal))
	if !strings.Contains(string(b), "bEnableInvaderEnemy=True") || !strings.Contains(string(b), "GuildPlayerMaxNum=7") {
		t.Fatalf("nicht gespeichert:\n%s", b)
	}

	// Abiotic Factor: no config in the template, the file AMP names is found
	v, _ = m.Settings("abi")
	if len(v.Files) != 1 || len(v.Files[0].Settings) < 20 || !strings.HasSuffix(v.Files[0].File, "SandboxSettings.ini") {
		t.Fatalf("Abiotic: %+v", v.Files)
	}

	// Counter-Strike: Source: server.cfg does not repeat what panel.cfg already has
	v, _ = m.Settings("css")
	c = cards(v)
	if _, ok := c["/server/cstrike/cfg/server.cfg|hostname"]; ok {
		t.Error("hostname doppelt")
	}
	if len(v.Files) != 2 || len(v.Files[1].Settings) < 20 {
		t.Fatalf("CS:S: %d Dateien", len(v.Files))
	}
}

func TestHumanize(t *testing.T) {
	for in, want := range map[string]string{"bEnablePlayerToPlayerDamage": "Enable Player To Player Damage", "max_players": "Max Players",
		"PalEggDefaultHatchingTime": "Pal Egg Default Hatching Time", "sv_lan": "Sv Lan", "RCONEnabled": "RCON Enabled", "bIsPvP": "Is PvP", "bEnablePvEDamage": "Enable PvE Damage"} {
		if got := humanize(in); got != want {
			t.Errorf("%s → %q", in, got)
		}
	}
	for in, want := range map[string]bool{"PublicPort": true, "queryPort": true, "ip": true, "saveDirectory": true, "Port": true,
		"Shipments": false, "ServerName": false, "Support": false, "IPBanList": false} {
		if managedName(in) != want {
			t.Errorf("managedName(%s) = %v", in, !want)
		}
	}
	if prettySection("/Script/Pal.PalGameWorldSettings") != "Pal Game World Settings" || prettySection("userGroups.1") != "User Groups 2" {
		t.Error(prettySection("/Script/Pal.PalGameWorldSettings"))
	}
}

func TestSettingsBackup(t *testing.T) {
	m, st := settingsMgr(t)
	pal := "server/Pal/Saved/Config/LinuxServer/PalWorldSettings.ini"
	sample(t, m, st, "pal", "palworld", pal, "palworldtemplatesettings.ini")
	orig, _ := os.ReadFile(filepath.Join(m.ServerDir("pal"), pal))
	ctx := context.Background()
	for i := 0; i < 7; i++ {
		if _, err := m.ApplySettings(ctx, "pal", nil, map[int]map[string]string{0: {"GuildPlayerMaxNum": strconv.Itoa(10 + i)}}); err != nil {
			t.Fatal(err)
		}
	}
	list, err := m.SettingsBackups("pal")
	if err != nil || len(list) != keepSettingsBackups || len(list[0].Changed) == 0 {
		t.Fatalf("%d Stände: %+v %v", len(list), list, err)
	}
	// the oldest kept one is the state after the second save
	if err := m.RestoreSettings(ctx, "pal", list[len(list)-1].ID); err != nil {
		t.Fatal(err)
	}
	b, _ := os.ReadFile(filepath.Join(m.ServerDir("pal"), pal))
	if !strings.Contains(string(b), "GuildPlayerMaxNum=11,") {
		t.Fatalf("falscher Stand: %s", b[:200])
	}
	// the restore itself can be undone
	list, _ = m.SettingsBackups("pal")
	if !strings.HasPrefix(list[0].Changed[0], "Wiederherstellung") {
		t.Fatalf("%+v", list[0])
	}
	_ = orig
	if err := m.RestoreSettings(ctx, "pal", "../../etc/passwd"); err == nil {
		t.Fatal("Pfad angenommen")
	}
}
