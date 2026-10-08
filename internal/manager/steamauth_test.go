package manager

import (
	"strings"
	"testing"
)

func TestWatchSteamLogin(t *testing.T) {
	head := "Redirecting stderr to '/home/steam/Steam/logs/stderr.txt'\nLoading Steam API...OK\n"
	cases := []struct {
		out, state, kind string
	}{
		{"Logging in user 'max' [U:1:0] to Steam Public...OK\nWaiting for client config...OK\nWaiting for user info...OK\n", "ok", ""},
		{"Logging in user 'max' to Steam Public...\nThis computer has not been authenticated for your account using Steam Guard.\nPlease check your email for the message from Steam, and enter the Steam Guard\n code from that message.\nSteam Guard code:", "need_code", "email"},
		{"Logging in user 'max' to Steam Public...\nTwo-factor code:", "need_code", "app"},
		{"Logging in user 'max' to Steam Public...FAILED (Invalid Password)\n", "error", ""},
		{"Logging in user 'max' to Steam Public...FAILED (Rate Limit Exceeded)\n", "error", ""},
		{"ERROR (Invalid Login Auth Code)\n", "need_code", "email"},
		{"Logging in user 'max' to Steam Public...FAILED (TwoFactorCodeMismatch)\n", "need_code", "app"},
		{"Unloading Steam API...OK\n", "error", ""},
	}
	for i, c := range cases {
		app := false
		r := watchSteamLogin(strings.NewReader(head+c.out), func() { app = true })
		if r.state != c.state || r.kind != c.kind || app {
			t.Errorf("Fall %d: %+v", i, r)
		}
		if r.state != "ok" && r.msg == "" {
			t.Errorf("Fall %d ohne Meldung", i)
		}
	}
	app := false
	r := watchSteamLogin(strings.NewReader(head+"Please confirm the login in the Steam Mobile app on your phone.\nWaiting for confirmation...\nWaiting for user info...OK\n"), func() { app = true })
	if !app || r.state != "ok" {
		t.Fatalf("App-Bestätigung: %+v %v", r, app)
	}
}

func TestSteamLoginValidation(t *testing.T) {
	m, _ := settingsMgr(t)
	for _, c := range [][3]string{{"a", "pw", ""}, {"max mustermann", "pw", ""}, {"max", "pw\nx", ""}, {"max", "pw", "12;34"}} {
		if m.SteamLogin(c[0], c[1], c[2]) == nil {
			t.Fatalf("%v angenommen", c)
		}
	}
	if err := m.SteamLogin("max", "", "ABCDE"); err == nil || !strings.Contains(err.Error(), "Passwort") {
		t.Fatalf("Code ohne vorherige Anmeldung: %v", err)
	}
	if m.Steam().State != "none" {
		t.Fatal(m.Steam())
	}
}
