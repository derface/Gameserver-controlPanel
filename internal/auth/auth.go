// Package auth handles the admin accounts, password hashing and sessions.
package auth

import (
	"crypto/pbkdf2"
	"crypto/rand"
	"crypto/sha256"
	"crypto/subtle"
	"encoding/hex"
	"errors"
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

const (
	CookieName  = "gp_session"
	sessionTTL  = 14 * 24 * time.Hour
	iterations  = 210_000
	minPassword = 10
)

var (
	ErrExists   = errors.New("Es gibt bereits ein Konto. Bitte anmelden.")
	ErrWeak     = errors.New("Das Passwort braucht mindestens 10 Zeichen.")
	ErrBadLogin = errors.New("Benutzername oder Passwort stimmt nicht.")
	ErrThrottle = errors.New("Zu viele Fehlversuche. Bitte in einer Minute erneut versuchen.")
)

type Auth struct {
	st       *store.Store
	mu       sync.Mutex
	failures map[string][]time.Time
}

func New(st *store.Store) *Auth {
	return &Auth{st: st, failures: map[string][]time.Time{}}
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

func (a *Auth) HasUsers() bool {
	has := false
	a.st.Read(func(d *store.Data) { has = len(d.Users) > 0 })
	return has
}

// Setup creates the first admin account. It only works while no account exists.
func (a *Auth) Setup(name, password string) error {
	name = strings.TrimSpace(name)
	if name == "" {
		return errors.New("Bitte einen Benutzernamen angeben.")
	}
	if len(password) < minPassword {
		return ErrWeak
	}
	salt := make([]byte, 16)
	_, _ = rand.Read(salt)
	h, err := hash(password, salt)
	if err != nil {
		return err
	}
	return a.st.Update(func(d *store.Data) error {
		if len(d.Users) > 0 {
			return ErrExists
		}
		d.Users = append(d.Users, store.User{Name: name, Salt: hex.EncodeToString(salt), Hash: h, Created: time.Now()})
		return nil
	})
}

func clientIP(r *http.Request) string {
	host, _, err := net.SplitHostPort(r.RemoteAddr)
	if err != nil {
		return r.RemoteAddr
	}
	return host
}

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

func (a *Auth) fail(ip string) {
	a.mu.Lock()
	a.failures[ip] = append(a.failures[ip], time.Now())
	a.mu.Unlock()
}

// Login checks credentials and returns a new session token.
func (a *Auth) Login(r *http.Request, name, password string) (string, error) {
	ip := clientIP(r)
	if a.throttled(ip) {
		return "", ErrThrottle
	}
	var u *store.User
	a.st.Read(func(d *store.Data) {
		for i := range d.Users {
			if strings.EqualFold(d.Users[i].Name, strings.TrimSpace(name)) {
				c := d.Users[i]
				u = &c
			}
		}
	})
	if u == nil {
		a.fail(ip)
		return "", ErrBadLogin
	}
	salt, _ := hex.DecodeString(u.Salt)
	h, err := hash(password, salt)
	if err != nil || subtle.ConstantTimeCompare([]byte(h), []byte(u.Hash)) != 1 {
		a.fail(ip)
		return "", ErrBadLogin
	}
	tok := randHex(32)
	err = a.st.Update(func(d *store.Data) error {
		now := time.Now()
		for k, s := range d.Sessions {
			if s.Expires.Before(now) {
				delete(d.Sessions, k)
			}
		}
		d.Sessions[tok] = store.Session{User: u.Name, Expires: now.Add(sessionTTL)}
		return nil
	})
	return tok, err
}

func (a *Auth) Logout(tok string) {
	_ = a.st.Update(func(d *store.Data) error {
		delete(d.Sessions, tok)
		return nil
	})
}

// User returns the logged-in user for a request, or "".
func (a *Auth) User(r *http.Request) string {
	c, err := r.Cookie(CookieName)
	if err != nil || c.Value == "" {
		return ""
	}
	user := ""
	a.st.Read(func(d *store.Data) {
		if s, ok := d.Sessions[c.Value]; ok && s.Expires.After(time.Now()) {
			user = s.User
		}
	})
	return user
}

func SetCookie(w http.ResponseWriter, r *http.Request, tok string, maxAge int) {
	http.SetCookie(w, &http.Cookie{
		Name:     CookieName,
		Value:    tok,
		Path:     "/",
		MaxAge:   maxAge,
		HttpOnly: true,
		Secure:   r.TLS != nil || r.Header.Get("X-Forwarded-Proto") == "https",
		SameSite: http.SameSiteStrictMode,
	})
}

func SessionMaxAge() int { return int(sessionTTL.Seconds()) }
