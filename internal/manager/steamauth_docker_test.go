package manager

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// TestSteamLoginDocker runs the login against the fake SteamCMD in the fake
// runtime image (testdata/fake/README.md).
func TestSteamLoginDocker(t *testing.T) {
	if os.Getenv("GP_DOCKER_TEST") == "" {
		t.Skip("GP_DOCKER_TEST nicht gesetzt")
	}
	dir := t.TempDir()
	st, _ := store.Open(dir)
	tpls, _ := templates.Load("")
	m := New(st, docker.New("/var/run/docker.sock"), tpls, dir)
	wait := func() SteamState {
		t.Helper()
		end := time.Now().Add(time.Minute)
		for time.Now().Before(end) {
			s := m.Steam()
			m.sl.mu.Lock()
			running := m.sl.run
			m.sl.mu.Unlock()
			if !running && s.State != "working" {
				return s
			}
			time.Sleep(300 * time.Millisecond)
		}
		t.Fatal("Anmeldung hängt")
		return SteamState{}
	}
	if err := m.SteamLogin("max", "falsch", ""); err != nil {
		t.Fatal(err)
	}
	if s := wait(); s.State != "error" || !strings.Contains(s.Message, "Passwort") {
		t.Fatalf("falsches Passwort: %+v", s)
	}
	_ = m.SteamLogin("max", "richtig", "")
	if s := wait(); s.State != "need_code" || s.Code != "email" {
		t.Fatalf("Steam Guard: %+v", s)
	}
	_ = m.SteamLogin("max", "", "XXXXX")
	if s := wait(); s.State != "need_code" || !strings.Contains(s.Message, "falsch") {
		t.Fatalf("falscher Code: %+v", s)
	}
	if err := m.SteamLogin("max", "", "AB12C"); err != nil {
		t.Fatal(err)
	}
	if s := wait(); s.State != "ok" || s.User != "max" {
		t.Fatalf("Anmeldung: %+v", s)
	}
	if b, err := os.ReadFile(filepath.Join(m.SteamAuthDir(), "config.vdf")); err != nil || string(b) != "cached max" {
		t.Fatalf("Anmeldung nicht gespeichert: %q %v", b, err)
	}
	m.sl.mu.Lock()
	if m.sl.pass != "" {
		t.Error("Passwort bleibt im Speicher")
	}
	m.sl.mu.Unlock()

	// a template that needs the account gets it
	tpl, _ := tpls.Get("lgsm-arma3")
	cfg := m.containerConfig(store.Server{ID: "a", Vars: map[string]string{}, Ports: []store.PortMap{{Host: 2302}, {Host: 2303}, {Host: 2304}, {Host: 2305}, {Host: 2306}}}, tpl)
	env := strings.Join(cfg.Env, " ")
	if !strings.Contains(env, "GP_STEAM_USER=max") || !strings.Contains(strings.Join(cfg.HostConfig.Binds, " "), "steam-auth:/home/steam/Steam") {
		t.Fatalf("Container: %v %v", cfg.Env, cfg.HostConfig.Binds)
	}
	valheim, _ := tpls.Get("valheim")
	cfg = m.containerConfig(store.Server{ID: "v", Vars: map[string]string{}, Ports: []store.PortMap{{Host: 2456}, {Host: 2457}}}, valheim)
	if strings.Contains(strings.Join(cfg.Env, " "), "GP_STEAM_USER=max") || len(cfg.HostConfig.Binds) != 1 {
		t.Fatal("anonymes Spiel bekommt das Konto")
	}
	if lc := cfg.HostConfig.LogConfig; lc == nil || lc.Config["max-size"] != "20m" || lc.Config["max-file"] != "3" || cfg.Labels["gamepanel.logs"] != logLimitTag {
		t.Fatalf("Log-Grenze fehlt: %+v %v", lc, cfg.Labels)
	}

	if err := m.SteamLogout(); err != nil {
		t.Fatal(err)
	}
	if s := m.Steam(); s.User != "" || s.State != "none" {
		t.Fatalf("Abmelden: %+v", s)
	}
	if es, _ := os.ReadDir(m.SteamAuthDir()); len(es) != 0 {
		t.Fatal("Anmeldedaten nicht gelöscht")
	}
}
