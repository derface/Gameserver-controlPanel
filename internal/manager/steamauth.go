package manager

// Steam account for games whose dedicated server cannot be downloaded
// anonymously (Arma 3, DayZ, Killing Floor …). The panel never stores the
// password: SteamCMD logs in once (with the Steam Guard code if asked) and
// keeps its own login cache in <data>/steam-auth, which is mounted into the
// containers of those games as /home/steam/Steam. They then log in with the
// account name only.

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// SteamState is shown in the settings.
type SteamState struct {
	User    string   `json:"user,omitempty"`      // logged-in account
	State   string   `json:"state"`               // none | working | need_code | need_app | ok | error
	Pending string   `json:"pending,omitempty"`   // account of a login in progress
	Code    string   `json:"code_kind,omitempty"` // email | app
	Message string   `json:"message,omitempty"`
	Log     []string `json:"log,omitempty"` // last SteamCMD lines of the attempt
}

type steamLogin struct {
	mu    sync.Mutex
	st    SteamState
	pass  string // only in memory, only until the login is done (max. 10 min)
	until time.Time
	run   bool
}

var steamUserRe = regexp.MustCompile(`^[A-Za-z0-9_.@-]{2,64}$`)

const steamLoginContainer = "gp-steamlogin"

func (m *Manager) SteamAuthDir() string { return filepath.Join(m.dataDir, "steam-auth") }

func (m *Manager) steamUser() string {
	var u string
	m.st.Read(func(d *store.Data) { u = d.Settings.SteamUser })
	return u
}

// Steam returns the account state.
func (m *Manager) Steam() SteamState {
	m.sl.mu.Lock()
	defer m.sl.mu.Unlock()
	st := m.sl.st
	st.User = m.steamUser()
	if st.State == "" {
		st.State = "none"
		if st.User != "" {
			st.State = "ok"
		}
	}
	return st
}

// SteamLogin starts a login in the background. For the second step (Steam
// Guard) the password may be empty: the one from the first step is reused.
func (m *Manager) SteamLogin(user, pass, code string) error {
	user, code = strings.TrimSpace(user), strings.TrimSpace(code)
	if !steamUserRe.MatchString(user) {
		return errors.New("Ungültiger Steam-Kontoname")
	}
	if strings.ContainsAny(pass, "\n\r\x00") || len(pass) > 128 {
		return errors.New("Ungültiges Passwort")
	}
	if code != "" && !regexp.MustCompile(`^[A-Za-z0-9]{4,8}$`).MatchString(code) {
		return errors.New("Der Steam-Guard-Code besteht aus 5 Zeichen")
	}
	m.sl.mu.Lock()
	defer m.sl.mu.Unlock()
	if m.sl.run {
		return errors.New("Die Anmeldung läuft schon")
	}
	if pass == "" {
		if m.sl.pass == "" || m.sl.st.Pending != user || time.Now().After(m.sl.until) {
			return errors.New("Bitte das Passwort noch einmal eingeben")
		}
		pass = m.sl.pass
	}
	m.sl.pass, m.sl.until, m.sl.run = pass, time.Now().Add(10*time.Minute), true
	m.sl.st = SteamState{State: "working", Pending: user, Message: "Anmeldung bei Steam …"}
	go m.runSteamLogin(user, pass, code)
	return nil
}

func (m *Manager) setSteam(f func(s *SteamState)) {
	m.sl.mu.Lock()
	f(&m.sl.st)
	m.sl.mu.Unlock()
}

func (m *Manager) runSteamLogin(user, pass, code string) {
	defer func() {
		m.sl.mu.Lock()
		m.sl.run = false
		if m.sl.st.State != "need_code" && m.sl.st.State != "need_app" {
			m.sl.pass = "" // only kept while a Steam Guard step is outstanding
		}
		m.sl.mu.Unlock()
	}()
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	defer cancel()
	fail := func(msg string) { m.setSteam(func(s *SteamState) { s.State, s.Message = "error", msg }) }

	if err := m.ensureImage(ctx, "steam-login", templates.Template{Kind: "steam"}); err != nil {
		m.clearJob("steam-login")
		fail("Steam-Laufzeit: " + err.Error())
		return
	}
	m.clearJob("steam-login")
	dir := m.SteamAuthDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		fail(err.Error())
		return
	}
	_ = os.Chown(dir, containerUID, containerUID)

	args := []string{"+login", user, pass}
	if code != "" {
		args = append(args, code)
	}
	args = append(args, "+quit")
	_ = m.dk.Remove(ctx, steamLoginContainer)
	cid, err := m.dk.Create(ctx, steamLoginContainer, docker.ContainerConfig{
		Image:      SteamImage,
		Entrypoint: []string{"/opt/steamcmd/steamcmd.sh"},
		Cmd:        args,
		Labels:     map[string]string{"gamepanel.role": "steam-login"},
		HostConfig: docker.HostConfig{Binds: []string{dir + ":/home/steam/Steam"}, RestartPolicy: docker.RestartPolicy{Name: "no"}},
	})
	if err != nil {
		fail(err.Error())
		return
	}
	defer func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer rcancel()
		_ = m.dk.Remove(rctx, cid)
	}()
	if err := m.dk.Start(ctx, cid); err != nil {
		fail(err.Error())
		return
	}
	rc, err := m.dk.Logs(ctx, cid, true, -1, 0)
	if err != nil {
		fail(err.Error())
		return
	}
	defer rc.Close()
	res := watchSteamLogin(rc, func() {
		m.setSteam(func(s *SteamState) {
			s.State, s.Code = "need_app", "app"
			s.Message = "Bitte die Anmeldung in der Steam-App auf dem Handy bestätigen."
		})
	})
	switch res.state {
	case "ok":
		_ = m.st.Update(func(d *store.Data) error { d.Settings.SteamUser = user; return nil })
		m.setSteam(func(s *SteamState) { *s = SteamState{State: "ok", Message: "Angemeldet als " + user} })
	case "need_code":
		m.setSteam(func(s *SteamState) {
			s.State, s.Code, s.Pending, s.Message, s.Log = "need_code", res.kind, user, res.msg, res.log
		})
	default:
		fail(res.msg)
		m.setSteam(func(s *SteamState) { s.Log = res.log })
	}
}

type loginResult struct {
	state, kind, msg string
	log              []string
}

// watchSteamLogin reads SteamCMD's output and decides how the login ended.
// It returns as soon as the outcome is clear (SteamCMD waits forever at a
// code prompt when stdin is closed).
func watchSteamLogin(r io.Reader, onApp func()) loginResult {
	var all []string
	res := watchSteamLines(r, onApp, &all)
	res.log = all
	return res
}

// steamLogLine hides the password and keeps a line for the log view.
var steamSecret = regexp.MustCompile(`(?i)(\+login\s+\S+\s+)\S+`)

func watchSteamLines(r io.Reader, onApp func(), all *[]string) loginResult {
	sc := bufio.NewScanner(r)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	var last []string
	for sc.Scan() {
		l := strings.TrimSpace(sc.Text())
		if l == "" {
			continue
		}
		last = append(last, l)
		*all = append(*all, steamSecret.ReplaceAllString(l, "${1}••••"))
		if len(*all) > 40 {
			*all = (*all)[1:]
		}
		if len(last) > 6 {
			last = last[1:]
		}
		low := strings.ToLower(l)
		switch {
		case strings.Contains(low, "invalid password") || strings.Contains(low, "invalidpassword"):
			return loginResult{"error", "", "Steam meldet: falsches Passwort oder falscher Kontoname.", nil}
		case strings.Contains(low, "rate limit") || strings.Contains(low, "ratelimit"):
			return loginResult{"error", "", "Steam meldet: zu viele Anmeldeversuche. Bitte etwa 30 Minuten warten.", nil}
		case strings.Contains(low, "invalidloginauthcode") || strings.Contains(low, "twofactorcodemismatch") || strings.Contains(low, "invalid login auth code"):
			return loginResult{"need_code", codeKind(low), "Der Code war falsch oder abgelaufen. Bitte den aktuellen Code eingeben.", nil}
		case strings.Contains(low, "two-factor") || strings.Contains(low, "two factor") || strings.Contains(low, "mobile authenticator"):
			return loginResult{"need_code", "app", "Bitte den Code aus der Steam-App (Steam Guard) eingeben.", nil}
		case strings.Contains(low, "steam guard code") || strings.Contains(low, "accountlogondenied") || strings.Contains(low, "account logon denied") || strings.Contains(low, "check your email"):
			return loginResult{"need_code", "email", "Steam verlangt einen Steam-Guard-Code. Er kommt per E-Mail an die Adresse des Kontos (auch im Spam nachsehen, kann ein paar Minuten dauern). Ist die Steam-App mit Steam Guard eingerichtet, steht er stattdessen dort.", nil}
		case strings.Contains(low, "steam mobile app") || strings.Contains(low, "confirm the login"):
			onApp() // keeps waiting: SteamCMD continues once the user taps "approve"
		case strings.Contains(low, "waiting for user info...ok") || strings.Contains(low, "logged in ok"):
			return loginResult{"ok", "", "", nil}
		case strings.Contains(low, "failed") && strings.Contains(low, "login"):
			return loginResult{"error", "", "Anmeldung fehlgeschlagen: " + l, nil}
		}
	}
	msg := "Anmeldung ohne Ergebnis beendet"
	if len(last) > 0 {
		msg += ": " + last[len(last)-1]
	}
	return loginResult{"error", "", msg, nil}
}

func codeKind(low string) string {
	if strings.Contains(low, "twofactor") {
		return "app"
	}
	return "email"
}

// SteamLogout forgets the account and SteamCMD's login cache.
func (m *Manager) SteamLogout() error {
	m.sl.mu.Lock()
	defer m.sl.mu.Unlock()
	if m.sl.run {
		return errors.New("Die Anmeldung läuft gerade")
	}
	m.sl.st, m.sl.pass = SteamState{}, ""
	dir := m.SteamAuthDir()
	entries, _ := os.ReadDir(dir)
	for _, e := range entries {
		_ = os.RemoveAll(filepath.Join(dir, e.Name()))
	}
	return m.st.Update(func(d *store.Data) error { d.Settings.SteamUser = ""; return nil })
}
