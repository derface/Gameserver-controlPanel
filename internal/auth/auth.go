// Package auth handles the panel accounts: passwords, roles, two-factor
// login and sessions.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/base64"
	"encoding/hex"
	"errors"
	"github.com/derface/Gameserver-controlPanel/internal/proxy"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

const (
	CookieName  = "gp_session"
	sessionTTL  = 14 * 24 * time.Hour
	ticketTTL   = 5 * time.Minute
	iterations  = 210_000
	minPassword = 10
	Issuer      = "Bytewise Panel"
)

// Roles, from weakest to strongest.
const (
	Viewer   = "viewer"   // sieht Server, Konsole und Verlauf
	Operator = "operator" // bedient Server: Start/Stopp, Befehle, Backups, Dateien, Einstellungen
	Admin    = "admin"    // alles, auch Server anlegen/löschen, Panel-Einstellungen und Benutzer
)

func Rank(role string) int {
	switch role {
	case Admin:
		return 3
	case Operator:
		return 2
	case Viewer:
		return 1
	}
	return 0
}

var (
	ErrExists    = errors.New("Es gibt bereits ein Konto. Bitte anmelden.")
	ErrWeak      = errors.New("Das Passwort braucht mindestens 10 Zeichen.")
	ErrBadLogin  = errors.New("Benutzername oder Passwort stimmt nicht.")
	ErrBadCode   = errors.New("Der Code stimmt nicht.")
	ErrTicket    = errors.New("Die Anmeldung ist abgelaufen. Bitte noch einmal mit Passwort anmelden.")
	ErrThrottle  = errors.New("Zu viele Fehlversuche. Bitte in einer Minute erneut versuchen.")
	ErrCodeLock  = errors.New("Zu viele falsche Codes für dieses Konto. Bitte in 15 Minuten erneut versuchen.")
	ErrNoUser    = errors.New("Benutzer nicht gefunden.")
	ErrLastAdmin = errors.New("Das ist der letzte aktive Admin. Lege zuerst einen weiteren Admin an.")
)

type ticket struct {
	user     string
	expires  time.Time
	attempts int
}

type Auth struct {
	st       *store.Store
	mu       sync.Mutex
	failures map[string][]time.Time
	tickets  map[string]*ticket // password ok, waiting for the 2FA code
	pending  map[string]string  // user → TOTP secret being set up
}

func New(st *store.Store) *Auth {
	return &Auth{st: st, failures: map[string][]time.Time{}, tickets: map[string]*ticket{}, pending: map[string]string{}}
}

func hash(password string, salt []byte) (string, error) {
	k, err := pbkdf2.Key(sha256.New, password, salt, iterations, 32)
	if err != nil {
		return "", err
	}
	return hex.EncodeToString(k), nil
}

func randHex(n int) string {
	b := make([]byte, n)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func tokenKey(tok string) string {
	s := sha256.Sum256([]byte(tok))
	return hex.EncodeToString(s[:])
}

func newPassword(password string) (salt, h string, err error) {
	if len(password) < minPassword {
		return "", "", ErrWeak
	}
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	h, err = hash(password, b)
	return hex.EncodeToString(b), h, err
}

func checkPassword(u store.User, password string) bool {
	salt, _ := hex.DecodeString(u.Salt)
	h, err := hash(password, salt)
	return err == nil && subtle.ConstantTimeCompare([]byte(h), []byte(u.Hash)) == 1
}

var nameRe = regexp.MustCompile(`^[\p{L}\p{N}._@-]{1,40}$`)

func validName(name string) error {
	if !nameRe.MatchString(name) {
		return errors.New("Benutzernamen bitte nur aus Buchstaben, Ziffern und . _ - @ (höchstens 40 Zeichen).")
	}
	return nil
}

func (a *Auth) HasUsers() bool {
	has := false
	a.st.Read(func(d *store.Data) { has = len(d.Users) > 0 })
	return has
}

func findUser(d *store.Data, name string) int {
	for i := range d.Users {
		if strings.EqualFold(d.Users[i].Name, strings.TrimSpace(name)) {
			return i
		}
	}
	return -1
}

// Get returns a copy of a user.
func (a *Auth) Get(name string) (store.User, bool) {
	var u store.User
	ok := false
	a.st.Read(func(d *store.Data) {
		if i := findUser(d, name); i >= 0 {
			u, ok = copyUser(d.Users[i]), true
		}
	})
	return u, ok
}

func copyUser(u store.User) store.User {
	u.Servers = slices.Clone(u.Servers)
	u.Recovery = slices.Clone(u.Recovery)
	return u
}

// Setup creates the first admin account. It only works while no account exists.
func (a *Auth) Setup(name, password string) error {
	name = strings.TrimSpace(name)
	if err := validName(name); err != nil {
		return err
	}
	salt, h, err := newPassword(password)
	if err != nil {
		return err
	}
	return a.st.Update(func(d *store.Data) error {
		if len(d.Users) > 0 {
			return ErrExists
		}
		d.Users = append(d.Users, store.User{Name: name, Salt: salt, Hash: h, Created: time.Now(), Role: Admin})
		return nil
	})
}

// ClientIP is the browser's address (behind a trusted reverse proxy the
// forwarded one).
func ClientIP(r *http.Request) string { return proxy.ClientIP(r) }

func (a *Auth) throttled(ip string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	cut := time.Now().Add(-time.Minute)
	keep := a.failures[ip][:0]
	for _, t := range a.failures[ip] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	a.failures[ip] = keep
	return len(keep) >= 5
}

// codeLocked limits wrong 2FA codes per account (10 in 15 minutes), so many
// IPs together cannot guess codes for a stolen password.
func (a *Auth) codeLocked(user string) bool {
	a.mu.Lock()
	defer a.mu.Unlock()
	k := "code:" + strings.ToLower(user)
	cut := time.Now().Add(-15 * time.Minute)
	keep := a.failures[k][:0]
	for _, t := range a.failures[k] {
		if t.After(cut) {
			keep = append(keep, t)
		}
	}
	a.failures[k] = keep
	return len(keep) >= 10
}

func (a *Auth) fail(ip string) {
	a.mu.Lock()
	a.failures[ip] = append(a.failures[ip], time.Now())
	a.mu.Unlock()
}

// LoginResult is either a session token or, with two-factor login, a ticket
// for the second step.
type LoginResult struct {
	User   string
	Token  string
	Ticket string
}

// Login checks the password. Without 2FA it opens a session right away.
func (a *Auth) Login(r *http.Request, name, password string) (LoginResult, error) {
	ip := ClientIP(r)
	if a.throttled(ip) {
		return LoginResult{}, ErrThrottle
	}
	u, ok := a.Get(name)
	if !ok {
		// same work as a real check, so the answer time does not reveal names
		_, _ = hash(password, []byte("0123456789abcdef"))
		a.fail(ip)
		return LoginResult{}, ErrBadLogin
	}
	if !checkPassword(u, password) {
		a.fail(ip)
		return LoginResult{User: u.Name}, ErrBadLogin
	}
	if u.Disabled {
		return LoginResult{User: u.Name}, errors.New("Dieses Konto ist gesperrt.")
	}
	if u.TOTP != "" {
		t := randHex(24)
		a.mu.Lock()
		for k, x := range a.tickets {
			if time.Now().After(x.expires) {
				delete(a.tickets, k)
			}
		}
		a.tickets[t] = &ticket{user: u.Name, expires: time.Now().Add(ticketTTL)}
		a.mu.Unlock()
		return LoginResult{User: u.Name, Ticket: t}, nil
	}
	tok, err := a.newSession(r, u.Name)
	return LoginResult{User: u.Name, Token: tok}, err
}

// LoginCode is the second step: the code from the authenticator app or a
// recovery code.
func (a *Auth) LoginCode(r *http.Request, tk, code string) (LoginResult, bool, error) {
	ip := ClientIP(r)
	if a.throttled(ip) {
		return LoginResult{}, false, ErrThrottle
	}
	a.mu.Lock()
	t := a.tickets[tk]
	if t == nil || time.Now().After(t.expires) || t.attempts >= 5 {
		delete(a.tickets, tk)
		a.mu.Unlock()
		return LoginResult{}, false, ErrTicket
	}
	t.attempts++
	user := t.user
	a.mu.Unlock()
	if a.codeLocked(user) {
		return LoginResult{User: user}, false, ErrCodeLock
	}

	usedRecovery := false
	err := a.st.Update(func(d *store.Data) error {
		i := findUser(d, user)
		if i < 0 || d.Users[i].Disabled || d.Users[i].TOTP == "" {
			return ErrTicket
		}
		u := &d.Users[i]
		if st := checkTOTP(u.TOTP, code, u.TOTPLast); st > 0 {
			u.TOTPLast = st
			return nil
		}
		h := hashRecovery(code)
		for j, rc := range u.Recovery {
			if subtle.ConstantTimeCompare([]byte(rc), []byte(h)) == 1 {
				u.Recovery = slices.Delete(u.Recovery, j, j+1)
				usedRecovery = true
				return nil
			}
		}
		return ErrBadCode
	})
	if err != nil {
		if errors.Is(err, ErrBadCode) {
			a.fail(ip)
			a.fail("code:" + strings.ToLower(user))
		}
		return LoginResult{User: user}, false, err
	}
	a.mu.Lock()
	delete(a.tickets, tk)
	a.mu.Unlock()
	tok, err := a.newSession(r, user)
	return LoginResult{User: user, Token: tok}, usedRecovery, err
}

func (a *Auth) newSession(r *http.Request, user string) (string, error) {
	tok := randHex(32)
	agent := r.UserAgent()
	if len(agent) > 200 {
		agent = agent[:200]
	}
	err := a.st.Update(func(d *store.Data) error {
		now := time.Now()
		for k, s := range d.Sessions {
			if s.Expires.Before(now) {
				delete(d.Sessions, k)
			}
		}
		d.Sessions[tokenKey(tok)] = store.Session{User: user, Expires: now.Add(sessionTTL), Created: now, IP: ClientIP(r), Agent: agent}
		if i := findUser(d, user); i >= 0 {
			d.Users[i].LastLogin = now
		}
		return nil
	})
	return tok, err
}

func (a *Auth) Logout(tok string) {
	_ = a.st.Update(func(d *store.Data) error {
		delete(d.Sessions, tokenKey(tok))
		delete(d.Sessions, tok) // session from before 0.12
		return nil
	})
}

// EndSessions logs a user out everywhere, except the session keep (may be "").
func (a *Auth) EndSessions(user, keepTok string) int {
	keep := ""
	if keepTok != "" {
		keep = tokenKey(keepTok)
	}
	n := 0
	_ = a.st.Update(func(d *store.Data) error {
		for k, s := range d.Sessions {
			if strings.EqualFold(s.User, user) && k != keep {
				delete(d.Sessions, k)
				n++
			}
		}
		return nil
	})
	return n
}

// Current returns the logged-in, enabled user of a request.
func (a *Auth) Current(r *http.Request) (store.User, bool) {
	if h := r.Header.Get("Authorization"); strings.HasPrefix(h, "Bearer "+NodeKeyPrefix) {
		return a.nodeUser(r, strings.TrimPrefix(h, "Bearer "))
	} else if strings.HasPrefix(h, "Bearer "+APIKeyPrefix) {
		return a.apiUser(r, strings.TrimPrefix(h, "Bearer "))
	}
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return store.User{}, false
	}
	var u store.User
	ok, legacy := false, false
	a.st.Read(func(d *store.Data) {
		s, found := d.Sessions[tokenKey(c.Value)]
		if !found {
			s, found = d.Sessions[c.Value]
			legacy = found
		}
		if !found || !s.Expires.After(time.Now()) {
			return
		}
		if i := findUser(d, s.User); i >= 0 && !d.Users[i].Disabled {
			u, ok = copyUser(d.Users[i]), true
		}
	})
	if ok && legacy { // move a session from before 0.12 to the hashed key
		_ = a.st.Update(func(d *store.Data) error {
			if s, found := d.Sessions[c.Value]; found {
				delete(d.Sessions, c.Value)
				d.Sessions[tokenKey(c.Value)] = s
			}
			return nil
		})
	}
	return u, ok
}

// User returns the name of the logged-in user, or "".
func (a *Auth) User(r *http.Request) string {
	u, _ := a.Current(r)
	return u.Name
}

func SetCookie(w http.ResponseWriter, r *http.Request, tok string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    tok,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   proxy.HTTPS(r),
		SameSite: http.SameSiteStrictMode,
	})
}

func SessionMaxAge() int { return int(sessionTTL.Seconds()) }

// ---------- own account ----------

func (a *Auth) ChangePassword(name, old, password string) error {
	salt, h, err := newPassword(password)
	if err != nil {
		return err
	}
	return a.st.Update(func(d *store.Data) error {
		i := findUser(d, name)
		if i < 0 {
			return ErrNoUser
		}
		if !checkPassword(d.Users[i], old) {
			return errors.New("Das bisherige Passwort stimmt nicht.")
		}
		d.Users[i].Salt, d.Users[i].Hash = salt, h
		return nil
	})
}

// BeginTOTP creates a new secret for the user; it becomes active only after
// EnableTOTP confirmed a code from the app.
func (a *Auth) BeginTOTP(name string) (secret, uri string) {
	secret = newTOTPSecret()
	a.mu.Lock()
	a.pending[strings.ToLower(name)] = secret
	a.mu.Unlock()
	return secret, otpauthURL(Issuer, name, secret)
}

// EnableTOTP checks the first code and returns fresh recovery codes.
func (a *Auth) EnableTOTP(name, code string) ([]string, error) {
	a.mu.Lock()
	secret := a.pending[strings.ToLower(name)]
	a.mu.Unlock()
	if secret == "" {
		return nil, errors.New("Bitte die Einrichtung neu starten.")
	}
	st := checkTOTP(secret, code, 0)
	if st == 0 {
		return nil, ErrBadCode
	}
	plain, hashed := newRecoveryCodes(10)
	err := a.st.Update(func(d *store.Data) error {
		i := findUser(d, name)
		if i < 0 {
			return ErrNoUser
		}
		d.Users[i].TOTP, d.Users[i].TOTPLast, d.Users[i].Recovery = secret, st, hashed
		return nil
	})
	if err != nil {
		return nil, err
	}
	a.mu.Lock()
	delete(a.pending, strings.ToLower(name))
	a.mu.Unlock()
	return plain, nil
}

// DisableTOTP needs the password, so an open browser alone cannot switch it off.
func (a *Auth) DisableTOTP(name, password string) error {
	return a.st.Update(func(d *store.Data) error {
		i := findUser(d, name)
		if i < 0 {
			return ErrNoUser
		}
		if !checkPassword(d.Users[i], password) {
			return errors.New("Das Passwort stimmt nicht.")
		}
		d.Users[i].TOTP, d.Users[i].TOTPLast, d.Users[i].Recovery = "", 0, nil
		return nil
	})
}

// NewRecoveryCodes replaces the recovery codes (needs the password).
func (a *Auth) NewRecoveryCodes(name, password string) ([]string, error) {
	plain, hashed := newRecoveryCodes(10)
	err := a.st.Update(func(d *store.Data) error {
		i := findUser(d, name)
		if i < 0 {
			return ErrNoUser
		}
		if !checkPassword(d.Users[i], password) {
			return errors.New("Das Passwort stimmt nicht.")
		}
		if d.Users[i].TOTP == "" {
			return errors.New("Zwei-Faktor-Anmeldung ist nicht eingeschaltet.")
		}
		d.Users[i].Recovery = hashed
		return nil
	})
	return plain, err
}

// ---------- user management (admins) ----------

// UserChange describes a new user or changes to one; nil fields stay as they are.
type UserChange struct {
	Name      string    `json:"name"`
	Password  *string   `json:"password"`
	Role      *string   `json:"role"`
	Servers   *[]string `json:"servers"`
	Disabled  *bool     `json:"disabled"`
	Reset2FA  bool      `json:"reset_2fa"`
	EndLogins bool      `json:"end_sessions"`
}

func activeAdmins(d *store.Data) int {
	n := 0
	for _, u := range d.Users {
		if u.Role == Admin && !u.Disabled {
			n++
		}
	}
	return n
}

func cleanServers(in []string) []string {
	out := []string{}
	for _, s := range in {
		if s = strings.TrimSpace(s); s != "" && !slices.Contains(out, s) {
			out = append(out, s)
		}
	}
	return out
}

func (a *Auth) CreateUser(c UserChange) error {
	name := strings.TrimSpace(c.Name)
	if err := validName(name); err != nil {
		return err
	}
	if c.Password == nil {
		return ErrWeak
	}
	salt, h, err := newPassword(*c.Password)
	if err != nil {
		return err
	}
	u := store.User{Name: name, Salt: salt, Hash: h, Created: time.Now(), Role: Viewer}
	if c.Role != nil {
		if Rank(*c.Role) == 0 {
			return errors.New("Unbekannte Rolle")
		}
		u.Role = *c.Role
	}
	if c.Servers != nil && u.Role != Admin {
		u.Servers = cleanServers(*c.Servers)
	}
	return a.st.Update(func(d *store.Data) error {
		if findUser(d, name) >= 0 {
			return errors.New("Diesen Benutzernamen gibt es schon.")
		}
		d.Users = append(d.Users, u)
		return nil
	})
}

func (a *Auth) UpdateUser(name string, c UserChange) error {
	var salt, h string
	if c.Password != nil {
		var err error
		if salt, h, err = newPassword(*c.Password); err != nil {
			return err
		}
	}
	if c.Role != nil && Rank(*c.Role) == 0 {
		return errors.New("Unbekannte Rolle")
	}
	return a.st.Update(func(d *store.Data) error {
		i := findUser(d, name)
		if i < 0 {
			return ErrNoUser
		}
		// check everything before changing anything: on an error the store
		// does not save, but the data in memory would stay changed
		u := &d.Users[i]
		role, disabled := u.Role, u.Disabled
		if c.Role != nil {
			role = *c.Role
		}
		if c.Disabled != nil {
			disabled = *c.Disabled
		}
		if u.Role == Admin && !u.Disabled && (role != Admin || disabled) && activeAdmins(d) == 1 {
			return ErrLastAdmin
		}
		u.Role, u.Disabled = role, disabled
		if c.Servers != nil {
			u.Servers = cleanServers(*c.Servers)
		}
		if u.Role == Admin {
			u.Servers = nil
		}
		if c.Password != nil {
			u.Salt, u.Hash = salt, h
		}
		if c.Reset2FA {
			u.TOTP, u.TOTPLast, u.Recovery = "", 0, nil
		}
		if c.Password != nil || c.Reset2FA || c.EndLogins || u.Disabled {
			for k, s := range d.Sessions {
				if strings.EqualFold(s.User, u.Name) {
					delete(d.Sessions, k)
				}
			}
		}
		return nil
	})
}

func (a *Auth) DeleteUser(name string) error {
	return a.st.Update(func(d *store.Data) error {
		i := findUser(d, name)
		if i < 0 {
			return ErrNoUser
		}
		if d.Users[i].Role == Admin && !d.Users[i].Disabled && activeAdmins(d) == 1 {
			return ErrLastAdmin
		}
		user := d.Users[i].Name
		d.Users = slices.Delete(d.Users, i, i+1)
		for k, s := range d.Sessions {
			if strings.EqualFold(s.User, user) {
				delete(d.Sessions, k)
			}
		}
		return nil
	})
}

// UserView is what the panel shows about an account (never hashes or secrets).
type UserView struct {
	Name      string    `json:"name"`
	Role      string    `json:"role"`
	Servers   []string  `json:"servers"`
	Disabled  bool      `json:"disabled"`
	TwoFactor bool      `json:"two_factor"`
	Recovery  int       `json:"recovery_left"`
	Created   time.Time `json:"created"`
	LastLogin time.Time `json:"last_login"`
	Sessions  int       `json:"sessions"`
}

func (a *Auth) Users() []UserView {
	var out []UserView
	a.st.Read(func(d *store.Data) {
		for _, u := range d.Users {
			out = append(out, view(d, u))
		}
	})
	return out
}

func (a *Auth) View(name string) (UserView, bool) {
	var v UserView
	ok := false
	a.st.Read(func(d *store.Data) {
		if i := findUser(d, name); i >= 0 {
			v, ok = view(d, d.Users[i]), true
		}
	})
	return v, ok
}

func view(d *store.Data, u store.User) UserView {
	v := UserView{Name: u.Name, Role: u.Role, Servers: slices.Clone(u.Servers), Disabled: u.Disabled, TwoFactor: u.TOTP != "",
		Recovery: len(u.Recovery), Created: u.Created, LastLogin: u.LastLogin}
	if v.Servers == nil {
		v.Servers = []string{}
	}
	for _, s := range d.Sessions {
		if strings.EqualFold(s.User, u.Name) && s.Expires.After(time.Now()) {
			v.Sessions++
		}
	}
	return v
}

// CanSee reports whether u may work with the server id at all.
func CanSee(u store.User, id string) bool {
	return u.Role == Admin || len(u.Servers) == 0 || slices.Contains(u.Servers, id)
}

// NodeKeyPrefix starts every key another panel uses to control this one.
const NodeKeyPrefix = "gpn_"

// NewNodeKey creates a key for another panel. The key itself is returned
// once; only its hash is kept.
func (a *Auth) NewNodeKey(label string) (string, error) {
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", err
	}
	key := NodeKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	id := make([]byte, 4)
	_, _ = rand.Read(id)
	return key, a.st.Update(func(d *store.Data) error {
		d.NodeKeys = append(d.NodeKeys, store.NodeKey{ID: hex.EncodeToString(id), Label: label, Hash: tokenKey(key), Created: time.Now()})
		return nil
	})
}

// nodeUser lets a panel holding a node key act as admin. The name of the
// person behind it comes along in X-Node-User, for the protocol.
func (a *Auth) nodeUser(r *http.Request, key string) (store.User, bool) {
	h := tokenKey(key)
	found, label := false, ""
	a.st.Read(func(d *store.Data) {
		for _, k := range d.NodeKeys {
			if subtle.ConstantTimeCompare([]byte(k.Hash), []byte(h)) == 1 {
				found, label = true, k.Label
			}
		}
	})
	if !found {
		return store.User{}, false
	}
	_ = a.st.Update(func(d *store.Data) error {
		for i := range d.NodeKeys {
			if d.NodeKeys[i].Hash == h && time.Since(d.NodeKeys[i].LastUsed) > time.Minute {
				d.NodeKeys[i].LastUsed, d.NodeKeys[i].LastFrom = time.Now(), ClientIP(r)
			}
		}
		return nil
	})
	name := label
	if who := strings.Map(func(c rune) rune {
		if c < 32 || c == 127 {
			return -1
		}
		return c
	}, r.Header.Get("X-Node-User")); who != "" {
		if len(who) > 40 {
			who = who[:40]
		}
		name = who + " (über " + label + ")"
	}
	return store.User{Name: name, Role: Admin, Created: time.Now(), ViaNode: true}, true
}

// APIKeyPrefix starts every key for scripts and home automation.
const APIKeyPrefix = "gpk_"

// NewAPIKey creates a key with the rights of role (operator or viewer),
// optionally only for some servers. The key is returned once.
func (a *Auth) NewAPIKey(name, role string, servers []string, by string) (string, store.APIKey, error) {
	if role != Operator && role != Viewer {
		return "", store.APIKey{}, errors.New("Rolle: Betreiber oder Zuschauer")
	}
	b := make([]byte, 32)
	if _, err := rand.Read(b); err != nil {
		return "", store.APIKey{}, err
	}
	key := APIKeyPrefix + base64.RawURLEncoding.EncodeToString(b)
	id := make([]byte, 4)
	_, _ = rand.Read(id)
	k := store.APIKey{ID: hex.EncodeToString(id), Name: name, Hash: tokenKey(key), Hint: key[:8] + "…", Role: role,
		Servers: servers, Created: time.Now(), By: by}
	err := a.st.Update(func(d *store.Data) error {
		if len(d.APIKeys) >= 50 {
			return errors.New("Höchstens 50 API-Schlüssel")
		}
		d.APIKeys = append(d.APIKeys, k)
		return nil
	})
	return key, k, err
}

// apiUser turns an API key into a user with the key's role and servers.
func (a *Auth) apiUser(r *http.Request, key string) (store.User, bool) {
	h := tokenKey(key)
	var k store.APIKey
	found := false
	a.st.Read(func(d *store.Data) {
		for _, x := range d.APIKeys {
			if subtle.ConstantTimeCompare([]byte(x.Hash), []byte(h)) == 1 {
				k, found = x, true
				k.Servers = append([]string(nil), x.Servers...)
			}
		}
	})
	if !found {
		return store.User{}, false
	}
	if time.Since(k.LastUsed) > time.Minute {
		_ = a.st.Update(func(d *store.Data) error {
			for i := range d.APIKeys {
				if d.APIKeys[i].Hash == h {
					d.APIKeys[i].LastUsed, d.APIKeys[i].LastFrom = time.Now(), ClientIP(r)
				}
			}
			return nil
		})
	}
	return store.User{Name: "API: " + k.Name, Role: k.Role, Servers: k.Servers, Created: k.Created, ViaKey: true}, true
}
