package auth

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

const rfcSecret = "GEZDGNBVGY3TQOJQGEZDGNBVGY3TQOJQ" // "12345678901234567890"

func TestTOTPVectors(t *testing.T) {
	// RFC 6238 appendix B (SHA-1), last six digits
	for unix, want := range map[int64]string{59: "287082", 1111111109: "081804", 1234567890: "005924", 2000000000: "279037"} {
		got, err := totpCode(rfcSecret, unix/30)
		if err != nil || got != want {
			t.Errorf("%d: %s, erwartet %s (%v)", unix, got, want, err)
		}
	}
}

func TestCheckTOTP(t *testing.T) {
	defer func() { now = time.Now }()
	now = func() time.Time { return time.Unix(1111111109, 0) }
	st := checkTOTP(rfcSecret, "081 804", 0)
	if st != 1111111109/30 {
		t.Fatalf("Code nicht angenommen: %d", st)
	}
	if checkTOTP(rfcSecret, "081804", st) != 0 {
		t.Fatal("Code zweimal angenommen")
	}
	prev, _ := totpCode(rfcSecret, st-1)
	if checkTOTP(rfcSecret, prev, 0) == 0 {
		t.Fatal("Code von vor 30 s abgelehnt")
	}
	old, _ := totpCode(rfcSecret, st-3)
	if checkTOTP(rfcSecret, old, 0) != 0 {
		t.Fatal("alter Code angenommen")
	}
	if checkTOTP(rfcSecret, "12345", 0) != 0 || checkTOTP(rfcSecret, "abcdef", 0) != 0 {
		t.Fatal("Unsinn angenommen")
	}
}

func req(ip string) *http.Request {
	r := httptest.NewRequest("POST", "/api/login", nil)
	r.RemoteAddr = ip + ":1234"
	return r
}

func withCookie(tok string) *http.Request {
	r := httptest.NewRequest("GET", "/api/state", nil)
	r.AddCookie(&http.Cookie{Name: CookieName, Value: tok})
	return r
}

func newAuth(t *testing.T) (*Auth, *store.Store) {
	st, err := store.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	a := New(st)
	if err := a.Setup("chef", "geheim-geheim"); err != nil {
		t.Fatal(err)
	}
	return a, st
}

func TestLoginTwoFactor(t *testing.T) {
	a, st := newAuth(t)
	res, err := a.Login(req("10.0.0.1"), "Chef", "geheim-geheim")
	if err != nil || res.Token == "" || res.Ticket != "" {
		t.Fatalf("%+v %v", res, err)
	}
	// the token is not stored in plain text
	st.Read(func(d *store.Data) {
		if _, ok := d.Sessions[res.Token]; ok {
			t.Error("Sitzung im Klartext gespeichert")
		}
	})
	if u, ok := a.Current(withCookie(res.Token)); !ok || u.Name != "chef" || u.Role != Admin {
		t.Fatalf("Sitzung: %+v %v", u, ok)
	}

	// switch on 2FA
	secret, uri := a.BeginTOTP("chef")
	if !strings.HasPrefix(uri, "otpauth://totp/Gamepanel:chef?") || !strings.Contains(uri, "secret="+secret) {
		t.Fatal(uri)
	}
	if _, err := a.EnableTOTP("chef", "000000"); err == nil {
		t.Fatal("falscher Code angenommen")
	}
	defer func() { now = time.Now }()
	clock := time.Unix(1_800_000_000, 0)
	now = func() time.Time { return clock }
	code, _ := totpCode(secret, clock.Unix()/30)
	codes, err := a.EnableTOTP("chef", code)
	if err != nil || len(codes) != 10 {
		t.Fatalf("%v %v", codes, err)
	}

	// password alone is not enough any more
	res, err = a.Login(req("10.0.0.1"), "chef", "geheim-geheim")
	if err != nil || res.Token != "" || res.Ticket == "" {
		t.Fatalf("%+v %v", res, err)
	}
	if _, _, err := a.LoginCode(req("10.0.0.1"), res.Ticket, code); err == nil {
		t.Fatal("bereits benutzter Code angenommen")
	}
	clock = clock.Add(30 * time.Second)
	code, _ = totpCode(secret, clock.Unix()/30)
	r2, rec, err := a.LoginCode(req("10.0.0.1"), res.Ticket, code)
	if err != nil || r2.Token == "" || rec {
		t.Fatalf("%+v %v", r2, err)
	}
	if _, _, err := a.LoginCode(req("10.0.0.1"), res.Ticket, code); err != ErrTicket {
		t.Fatalf("Ticket zweimal benutzt: %v", err)
	}

	// a recovery code works once
	res, _ = a.Login(req("10.0.0.2"), "chef", "geheim-geheim")
	r3, rec, err := a.LoginCode(req("10.0.0.2"), res.Ticket, strings.ToUpper(codes[3]))
	if err != nil || !rec || r3.Token == "" {
		t.Fatalf("Wiederherstellungscode: %v %v", rec, err)
	}
	res, _ = a.Login(req("10.0.0.2"), "chef", "geheim-geheim")
	if _, _, err := a.LoginCode(req("10.0.0.2"), res.Ticket, codes[3]); err == nil {
		t.Fatal("Wiederherstellungscode zweimal angenommen")
	}
	if v, _ := a.View("chef"); v.Recovery != 9 || !v.TwoFactor {
		t.Fatalf("%+v", v)
	}

	// five wrong codes end the ticket
	res, _ = a.Login(req("10.0.0.3"), "chef", "geheim-geheim")
	for i := 0; i < 4; i++ {
		_, _, _ = a.LoginCode(req("10.0.0."+string(rune('4'+i))), res.Ticket, "000000")
	}
	_, _, _ = a.LoginCode(req("10.0.0.9"), res.Ticket, "000000")
	clock = clock.Add(30 * time.Second)
	code, _ = totpCode(secret, clock.Unix()/30)
	if _, _, err := a.LoginCode(req("10.0.1.1"), res.Ticket, code); err != ErrTicket {
		t.Fatalf("Ticket nach 5 Fehlversuchen noch gültig: %v", err)
	}

	// many IPs together: at most 10 wrong codes per account in 15 minutes
	for i := 0; i < 6; i++ {
		res, _ = a.Login(req("10.3.0."+string(rune('1'+i))), "chef", "geheim-geheim")
		_, _, _ = a.LoginCode(req("10.3.0."+string(rune('1'+i))), res.Ticket, "000000")
	}
	res, _ = a.Login(req("10.4.0.1"), "chef", "geheim-geheim")
	clock = clock.Add(30 * time.Second)
	code, _ = totpCode(secret, clock.Unix()/30)
	if _, _, err := a.LoginCode(req("10.4.0.1"), res.Ticket, code); err != ErrCodeLock {
		t.Fatalf("keine Kontosperre nach vielen falschen Codes: %v", err)
	}
	a.mu.Lock()
	delete(a.failures, "code:chef")
	a.mu.Unlock()

	if err := a.DisableTOTP("chef", "falsch-falsch"); err == nil {
		t.Fatal("ohne Passwort abgeschaltet")
	}
	if err := a.DisableTOTP("chef", "geheim-geheim"); err != nil {
		t.Fatal(err)
	}
	if res, _ := a.Login(req("10.0.2.1"), "chef", "geheim-geheim"); res.Token == "" {
		t.Fatal("nach Abschalten noch Code verlangt")
	}
}

func TestThrottle(t *testing.T) {
	a, _ := newAuth(t)
	for i := 0; i < 5; i++ {
		if _, err := a.Login(req("10.1.0.1"), "chef", "falsch"); err != ErrBadLogin {
			t.Fatal(err)
		}
	}
	if _, err := a.Login(req("10.1.0.1"), "chef", "geheim-geheim"); err != ErrThrottle {
		t.Fatalf("nicht gebremst: %v", err)
	}
	if _, err := a.Login(req("10.1.0.2"), "chef", "geheim-geheim"); err != nil {
		t.Fatalf("andere IP gebremst: %v", err)
	}
}

func TestUsers(t *testing.T) {
	a, _ := newAuth(t)
	pw := "passwort-123"
	op, srv := Operator, []string{"mc", "mc", " "}
	if err := a.CreateUser(UserChange{Name: "Max", Password: &pw, Role: &op, Servers: &srv}); err != nil {
		t.Fatal(err)
	}
	if err := a.CreateUser(UserChange{Name: "max", Password: &pw}); err == nil {
		t.Fatal("doppelter Name angenommen")
	}
	if err := a.CreateUser(UserChange{Name: "x y", Password: &pw}); err == nil {
		t.Fatal("Leerzeichen im Namen angenommen")
	}
	short := "kurz"
	if err := a.CreateUser(UserChange{Name: "kurz", Password: &short}); err != ErrWeak {
		t.Fatal(err)
	}
	u, _ := a.Get("max")
	if u.Role != Operator || len(u.Servers) != 1 || !CanSee(u, "mc") || CanSee(u, "ark") {
		t.Fatalf("%+v", u)
	}

	res, _ := a.Login(req("10.2.0.1"), "max", pw)
	if _, ok := a.Current(withCookie(res.Token)); !ok {
		t.Fatal("Max nicht angemeldet")
	}
	yes := true
	if err := a.UpdateUser("max", UserChange{Disabled: &yes}); err != nil {
		t.Fatal(err)
	}
	if _, ok := a.Current(withCookie(res.Token)); ok {
		t.Fatal("gesperrter Benutzer noch angemeldet")
	}
	if _, err := a.Login(req("10.2.0.1"), "max", pw); err == nil {
		t.Fatal("gesperrter Benutzer kann sich anmelden")
	}

	// the last admin stays
	viewer := Viewer
	if err := a.UpdateUser("chef", UserChange{Role: &viewer}); err != ErrLastAdmin {
		t.Fatalf("letzter Admin herabgestuft: %v", err)
	}
	if u, _ := a.Get("chef"); u.Role != Admin {
		t.Fatalf("Rolle trotz Fehler geändert: %s", u.Role)
	}
	if err := a.DeleteUser("chef"); err != ErrLastAdmin {
		t.Fatalf("letzter Admin gelöscht: %v", err)
	}
	adm := Admin
	no := false
	if err := a.UpdateUser("max", UserChange{Role: &adm, Disabled: &no}); err != nil {
		t.Fatal(err)
	}
	if u, _ := a.Get("max"); len(u.Servers) != 0 {
		t.Fatal("Admin mit Server-Beschränkung")
	}
	if err := a.DeleteUser("chef"); err != nil {
		t.Fatal(err)
	}
	if len(a.Users()) != 1 {
		t.Fatal(a.Users())
	}
}

func TestLegacySession(t *testing.T) {
	a, st := newAuth(t)
	_ = st.Update(func(d *store.Data) error {
		d.Sessions["alt"] = store.Session{User: "chef", Expires: time.Now().Add(time.Hour)}
		return nil
	})
	if _, ok := a.Current(withCookie("alt")); !ok {
		t.Fatal("alte Sitzung abgelehnt")
	}
	st.Read(func(d *store.Data) {
		if _, ok := d.Sessions["alt"]; ok {
			t.Error("alte Sitzung nicht umgestellt")
		}
		if _, ok := d.Sessions[tokenKey("alt")]; !ok {
			t.Error("umgestellte Sitzung fehlt")
		}
	})
	if _, ok := a.Current(withCookie("alt")); !ok {
		t.Fatal("umgestellte Sitzung abgelehnt")
	}
}
