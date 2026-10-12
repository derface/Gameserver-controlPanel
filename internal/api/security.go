package api

import (
	"bytes"
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/audit"
	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

type ctxKey int

const (
	keyUser ctxKey = iota
	keyNote
)

// note collects details a handler adds to its protocol entry.
type note struct {
	detail string
	skip   bool
}

func userOf(r *http.Request) store.User {
	u, _ := r.Context().Value(keyUser).(store.User)
	return u
}

// Note adds a detail (e.g. the console command) to the protocol entry of r.
func Note(r *http.Request, detail string) {
	if n, ok := r.Context().Value(keyNote).(*note); ok {
		if n.detail != "" {
			n.detail += "; "
		}
		n.detail += detail
	}
}

// noAudit keeps a request out of the protocol (tests, previews).
func noAudit(r *http.Request) {
	if n, ok := r.Context().Value(keyNote).(*note); ok {
		n.skip = true
	}
}

// recorder remembers the status and the start of an error answer.
type recorder struct {
	http.ResponseWriter
	code int
	body bytes.Buffer
}

func (w *recorder) WriteHeader(c int) {
	if w.code == 0 {
		w.code = c
	}
	w.ResponseWriter.WriteHeader(c)
}

func (w *recorder) Write(b []byte) (int, error) {
	if w.code == 0 {
		w.code = 200
	}
	if w.code >= 400 && w.body.Len() < 400 {
		w.body.Write(b)
	}
	return w.ResponseWriter.Write(b)
}

func (w *recorder) Flush() {
	if f, ok := w.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func (w *recorder) Unwrap() http.ResponseWriter { return w.ResponseWriter }

// errText pulls the message out of {"error": "..."}.
func errText(b []byte) string {
	s := string(b)
	if i := strings.Index(s, `"error":"`); i >= 0 {
		s = s[i+9:]
		if j := strings.Index(s, `"`); j >= 0 {
			s = s[:j]
		}
	}
	return s
}

// auditGET lists the reading requests that still belong in the protocol.
var auditGET = map[string]bool{
	"GET /api/servers/{id}/backups/{name}": true,
	"GET /api/servers/{id}/files/download": true,
	"GET /api/audit":                       false,
}

// need protects an API route: a session, at least the given role, the
// X-Panel header for changing requests (browsers cannot send it cross-site
// without CORS, which we never allow) and, for /servers/{id}/…, access to
// that server. Changing requests are written to the protocol.
func (a *API) need(role string, h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Header.Get("X-Panel") != "1" {
			fail(w, http.StatusForbidden, errors.New("Anfrage abgelehnt"))
			return
		}
		u, ok := a.Auth.Current(r)
		if !ok {
			fail(w, http.StatusUnauthorized, errors.New("Bitte anmelden"))
			return
		}
		id := r.PathValue("id")
		if id != "" && !auth.CanSee(u, id) {
			fail(w, http.StatusNotFound, errors.New("Server nicht gefunden"))
			return
		}
		if auth.Rank(u.Role) < auth.Rank(role) {
			if r.Method != http.MethodGet && a.Audit != nil {
				a.Audit.Add(audit.Entry{User: u.Name, IP: auth.ClientIP(r), Action: describe(r), Target: id, Error: "ohne Rechte versucht"})
			}
			fail(w, http.StatusForbidden, errors.New("Dafür fehlen dir die Rechte."))
			return
		}
		n := &note{}
		ctx := context.WithValue(context.WithValue(r.Context(), keyUser, u), keyNote, n)
		r = r.WithContext(ctx)
		if r.Method == http.MethodGet && !auditGET[r.Pattern] {
			h(w, r)
			return
		}
		target := ""
		if id != "" {
			if sv, ok := a.Store.Server(id); ok {
				target = sv.Name
			} else {
				target = id
			}
		}
		rec := &recorder{ResponseWriter: w}
		h(rec, r)
		if n.skip || a.Audit == nil {
			return
		}
		e := audit.Entry{User: u.Name, IP: auth.ClientIP(r), Action: describe(r), Target: target, Detail: n.detail, OK: rec.code < 400}
		if !e.OK {
			e.Error = errText(rec.body.Bytes())
		}
		if p := r.URL.Query().Get("path"); p != "" && strings.Contains(r.Pattern, "/files") {
			e.Detail = strings.TrimPrefix(e.Detail+"; "+p, "; ")
		}
		if nm := r.PathValue("name"); nm != "" {
			e.Detail = strings.TrimPrefix(e.Detail+"; "+nm, "; ")
		}
		a.Audit.Add(e)
	}
}

var actions = map[string]string{
	"POST /api/servers":                             "Server angelegt",
	"DELETE /api/import/amp/{iid}":                  "AMP-Export verworfen",
	"POST /api/servers/{id}/diagnose":               "Mit KI geprüft",
	"POST /api/servers/{id}/modlist/toggle":         "Mod geschaltet",
	"POST /api/import/amp/{iid}/create":             "Server aus AMP übernommen",
	"PATCH /api/servers/{id}":                       "Server geändert",
	"DELETE /api/servers/{id}":                      "Server gelöscht",
	"POST /api/servers/{id}/backups":                "Backup erstellt",
	"PUT /api/servers/{id}/backup-plan":             "Backup-Plan geändert",
	"GET /api/servers/{id}/backups/{name}":          "Backup heruntergeladen",
	"DELETE /api/servers/{id}/backups/{name}":       "Backup gelöscht",
	"POST /api/servers/{id}/backups/{name}/restore": "Backup wiederhergestellt",
	"POST /api/servers/{id}/backups/{name}/upload":  "Backup extern gesichert",
	"PUT /api/remote":                               "Externe Backups geändert",
	"PUT /api/servers/{id}/settings":                "Spiel-Einstellungen geändert",
	"PUT /api/servers/{id}/tasks":                   "Aufgaben geändert",
	"POST /api/servers/{id}/tasks/{tid}/run":        "Aufgabe von Hand ausgeführt",
	"PUT /api/servers/{id}/watch":                   "Überwachung geändert",
	"POST /api/steam/login":                         "Steam-Anmeldung",
	"DELETE /api/steam":                             "Steam-Konto abgemeldet",
	"POST /api/selftest":                            "Selbsttest gestartet",
	"DELETE /api/selftest":                          "Selbsttest abgebrochen",
	"DELETE /api/servers/{id}/files":                "Datei gelöscht",
	"PUT /api/servers/{id}/files/content":           "Datei bearbeitet",
	"GET /api/servers/{id}/files/download":          "Datei heruntergeladen",
	"POST /api/servers/{id}/files/upload":           "Datei hochgeladen",
	"POST /api/servers/{id}/files/mkdir":            "Ordner angelegt",
	"POST /api/servers/{id}/files/rename":           "Datei umbenannt",
	"PUT /api/servers/{id}/mods":                    "Mods geändert",
	"PUT /api/settings":                             "Panel-Einstellungen geändert",
	"PUT /api/license":                              "Lizenz geändert",
	"POST /api/users":                               "Benutzer angelegt",
	"PATCH /api/users/{name}":                       "Benutzer geändert",
	"DELETE /api/users/{name}":                      "Benutzer gelöscht",
	"PUT /api/me/password":                          "Eigenes Passwort geändert",
	"POST /api/me/2fa/setup":                        "Zwei-Faktor-Einrichtung begonnen",
	"POST /api/me/2fa/enable":                       "Zwei-Faktor-Anmeldung eingeschaltet",
	"POST /api/me/2fa/disable":                      "Zwei-Faktor-Anmeldung ausgeschaltet",
	"POST /api/me/2fa/recovery":                     "Neue Wiederherstellungscodes",
	"POST /api/me/logout-all":                       "Auf allen anderen Geräten abgemeldet",
	"PUT /api/https":                                "HTTPS geändert",
	"PUT /api/router":                               "Automatische Portfreigabe",
	"PUT /api/servers/{id}/router":                  "Portfreigabe des Servers geändert",
	"POST /api/servers/{id}/settings/restore":       "Einstellungen zurückgeholt",
	"POST /api/https/renew":                         "Zertifikat angefordert",
	"POST /api/servers/{id}/{action}#start":         "Server gestartet",
	"POST /api/servers/{id}/{action}#stop":          "Server gestoppt",
	"POST /api/servers/{id}/{action}#restart":       "Server neu gestartet",
	"POST /api/servers/{id}/{action}#recreate":      "Server neu angelegt",
	"POST /api/servers/{id}/{action}#update":        "Server aktualisiert",
	"POST /api/servers/{id}/{action}#command":       "Konsolenbefehl",
}

func describe(r *http.Request) string {
	p := r.Pattern
	if act := r.PathValue("action"); act != "" && strings.HasSuffix(p, "{action}") {
		if s := actions[p+"#"+act]; s != "" {
			return s
		}
		return "Aktion " + act
	}
	if s := actions[p]; s != "" {
		return s
	}
	return r.Method + " " + r.URL.Path
}

// ---------- routes ----------

func (a *API) routesSecurity(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/login/2fa", a.csrfOnly(a.login2FA))
	mux.HandleFunc("GET /api/me", a.need(auth.Viewer, a.me))
	mux.HandleFunc("PUT /api/me/password", a.need(auth.Viewer, a.mePassword))
	mux.HandleFunc("POST /api/me/2fa/setup", a.need(auth.Viewer, a.me2FASetup))
	mux.HandleFunc("POST /api/me/2fa/enable", a.need(auth.Viewer, a.me2FAEnable))
	mux.HandleFunc("POST /api/me/2fa/disable", a.need(auth.Viewer, a.me2FADisable))
	mux.HandleFunc("POST /api/me/2fa/recovery", a.need(auth.Viewer, a.me2FARecovery))
	mux.HandleFunc("POST /api/me/logout-all", a.need(auth.Viewer, a.meLogoutAll))
	mux.HandleFunc("GET /api/users", a.need(auth.Admin, a.usersGet))
	mux.HandleFunc("POST /api/users", a.need(auth.Admin, a.userCreate))
	mux.HandleFunc("PATCH /api/users/{name}", a.need(auth.Admin, a.userUpdate))
	mux.HandleFunc("DELETE /api/users/{name}", a.need(auth.Admin, a.userDelete))
	mux.HandleFunc("GET /api/audit", a.need(auth.Admin, a.auditGet))
	mux.HandleFunc("GET /api/https", a.need(auth.Admin, a.httpsGet))
	mux.HandleFunc("PUT /api/https", a.need(auth.Admin, a.httpsPut))
	mux.HandleFunc("POST /api/https/renew", a.need(auth.Admin, a.httpsRenew))
}

// logAuth writes login events, which happen before there is a session.
func (a *API) logAuth(r *http.Request, user, action string, err error) {
	if a.Audit == nil {
		return
	}
	e := audit.Entry{User: user, IP: auth.ClientIP(r), Action: action, OK: err == nil}
	if err != nil {
		e.Error = err.Error()
	}
	a.Audit.Add(e)
}

func (a *API) login2FA(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Ticket string `json:"ticket"`
		Code   string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	res, recovery, err := a.Auth.LoginCode(r, in.Ticket, in.Code)
	if err != nil {
		a.logAuth(r, res.User, "Anmeldung (zweiter Schritt)", err)
		code := 401
		if errors.Is(err, auth.ErrThrottle) || errors.Is(err, auth.ErrCodeLock) {
			code = 429
		}
		fail(w, code, err)
		return
	}
	act := "Angemeldet mit Zwei-Faktor-Code"
	if recovery {
		act = "Angemeldet mit Wiederherstellungscode"
	}
	a.logAuth(r, res.User, act, nil)
	auth.SetCookie(w, r, res.Token, auth.SessionMaxAge())
	v, _ := a.Auth.View(res.User)
	writeJSON(w, 200, map[string]any{"user": res.User, "used_recovery": recovery, "recovery_left": v.Recovery})
}

func (a *API) me(w http.ResponseWriter, r *http.Request) {
	v, _ := a.Auth.View(userOf(r).Name)
	writeJSON(w, 200, v)
}

func (a *API) mePassword(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Old string `json:"old"`
		New string `json:"new"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	u := userOf(r)
	if err := a.Auth.ChangePassword(u.Name, in.Old, in.New); err != nil {
		fail(w, 400, err)
		return
	}
	// other devices must log in with the new password
	tok := ""
	if c, err := r.Cookie(auth.CookieName); err == nil {
		tok = c.Value
	}
	n := a.Auth.EndSessions(u.Name, tok)
	writeJSON(w, 200, map[string]int{"ended": n})
}

func (a *API) me2FASetup(w http.ResponseWriter, r *http.Request) {
	secret, uri := a.Auth.BeginTOTP(userOf(r).Name)
	writeJSON(w, 200, map[string]string{"secret": secret, "uri": uri})
}

func (a *API) me2FAEnable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Code string `json:"code"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	codes, err := a.Auth.EnableTOTP(userOf(r).Name, in.Code)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"recovery": codes})
}

func (a *API) me2FADisable(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if err := a.Auth.DisableTOTP(userOf(r).Name, in.Password); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) me2FARecovery(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Password string `json:"password"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	codes, err := a.Auth.NewRecoveryCodes(userOf(r).Name, in.Password)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"recovery": codes})
}

func (a *API) meLogoutAll(w http.ResponseWriter, r *http.Request) {
	tok := ""
	if c, err := r.Cookie(auth.CookieName); err == nil {
		tok = c.Value
	}
	n := a.Auth.EndSessions(userOf(r).Name, tok)
	Note(r, strconv.Itoa(n)+" Sitzungen beendet")
	writeJSON(w, 200, map[string]int{"ended": n})
}

func (a *API) usersGet(w http.ResponseWriter, r *http.Request) {
	type srv struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	list := []srv{}
	for _, sv := range a.Store.Servers() {
		if sv.CreatedBy != "selftest" {
			list = append(list, srv{sv.ID, sv.Name})
		}
	}
	writeJSON(w, 200, map[string]any{"users": a.Auth.Users(), "servers": list, "me": userOf(r).Name})
}

var roleNames = map[string]string{auth.Admin: "Admin", auth.Operator: "Betreiber", auth.Viewer: "Zuschauer"}

func describeChange(c auth.UserChange) string {
	var parts []string
	if c.Role != nil {
		parts = append(parts, "Rolle "+roleNames[*c.Role])
	}
	if c.Servers != nil {
		if len(*c.Servers) == 0 {
			parts = append(parts, "alle Server")
		} else {
			parts = append(parts, strconv.Itoa(len(*c.Servers))+" Server")
		}
	}
	if c.Disabled != nil {
		parts = append(parts, map[bool]string{true: "gesperrt", false: "entsperrt"}[*c.Disabled])
	}
	if c.Password != nil {
		parts = append(parts, "Passwort neu gesetzt")
	}
	if c.Reset2FA {
		parts = append(parts, "Zwei-Faktor zurückgesetzt")
	}
	if c.EndLogins {
		parts = append(parts, "abgemeldet")
	}
	return strings.Join(parts, ", ")
}

func (a *API) userCreate(w http.ResponseWriter, r *http.Request) {
	var c auth.UserChange
	if err := decode(r, &c); err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, strings.TrimSpace(c.Name))
	if err := a.Auth.CreateUser(c); err != nil {
		fail(w, 400, err)
		return
	}
	c.Password = nil
	Note(r, describeChange(c))
	writeJSON(w, 201, map[string]bool{"ok": true})
}

func (a *API) userUpdate(w http.ResponseWriter, r *http.Request) {
	var c auth.UserChange
	if err := decode(r, &c); err != nil {
		fail(w, 400, err)
		return
	}
	name := r.PathValue("name")
	if strings.EqualFold(name, userOf(r).Name) && (c.Role != nil && *c.Role != auth.Admin || c.Disabled != nil && *c.Disabled) {
		fail(w, 400, errors.New("Du kannst dich nicht selbst herabstufen oder sperren."))
		return
	}
	if err := a.Auth.UpdateUser(name, c); err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, name+": "+describeChange(c))
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) userDelete(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if strings.EqualFold(name, userOf(r).Name) {
		fail(w, 400, errors.New("Du kannst dich nicht selbst löschen."))
		return
	}
	Note(r, name)
	if err := a.Auth.DeleteUser(name); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) auditGet(w http.ResponseWriter, r *http.Request) {
	if a.Audit == nil {
		writeJSON(w, 200, []audit.Entry{})
		return
	}
	q := strings.ToLower(strings.TrimSpace(r.URL.Query().Get("q")))
	user := strings.TrimSpace(r.URL.Query().Get("user"))
	failed := r.URL.Query().Get("failed") == "1"
	n, _ := strconv.Atoi(r.URL.Query().Get("limit"))
	if n <= 0 || n > 2000 {
		n = 300
	}
	var since time.Time
	if d, err := strconv.Atoi(r.URL.Query().Get("days")); err == nil && d > 0 {
		since = time.Now().AddDate(0, 0, -d)
	}
	writeJSON(w, 200, a.Audit.Recent(n, func(e audit.Entry) bool {
		if user != "" && !strings.EqualFold(e.User, user) || failed && e.OK || e.Time.Before(since) {
			return false
		}
		if q != "" {
			hay := strings.ToLower(e.User + " " + e.Action + " " + e.Target + " " + e.Detail + " " + e.Error + " " + e.IP)
			return strings.Contains(hay, q)
		}
		return true
	}))
}
