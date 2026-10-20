// Package api exposes the panel's HTTP API and serves the web UI.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/derface/Gameserver-controlPanel/internal/notify"
	"io/fs"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/ai"
	"github.com/derface/Gameserver-controlPanel/internal/audit"
	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/hoststats"
	"github.com/derface/Gameserver-controlPanel/internal/https"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/nodes"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

type API struct {
	Auth    *auth.Auth
	Store   *store.Store
	Mgr     *manager.Manager
	Host    *hoststats.Sampler
	Docker  *docker.Client
	Web     fs.FS
	Version string
	Audit   *audit.Log
	TLS     *https.Manager
	Nodes   *nodes.Pool // other machines this panel controls
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
	if _, ok := w.(*enWriter); ok {
		v = localize(v)
	}
	_ = json.NewEncoder(w).Encode(v)
}

func fail(w http.ResponseWriter, code int, err error) {
	writeJSON(w, code, map[string]string{"error": err.Error()})
}

func decode(r *http.Request, v any) error {
	dec := json.NewDecoder(http.MaxBytesReader(nil, r.Body, 1<<20))
	if err := dec.Decode(v); err != nil {
		return errors.New("Ungültige Anfrage")
	}
	return nil
}

func (a *API) csrfOnly(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Panel") != "1" {
			fail(w, http.StatusForbidden, errors.New("Anfrage abgelehnt"))
			return
		}
		h(w, r)
	}
}

func (a *API) Handler() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /api/state", a.state)
	mux.HandleFunc("POST /api/setup", a.csrfOnly(a.setup))
	mux.HandleFunc("POST /api/login", a.csrfOnly(a.login))
	mux.HandleFunc("POST /api/logout", a.csrfOnly(a.logout))

	mux.HandleFunc("GET /api/host", a.need(auth.Viewer, a.host))
	a.nodeRoutes(mux)
	mux.HandleFunc("GET /api/templates", a.need(auth.Viewer, a.templates))
	mux.HandleFunc("GET /api/servers", a.need(auth.Viewer, a.servers))
	mux.HandleFunc("POST /api/servers", a.need(auth.Admin, a.create))
	mux.HandleFunc("GET /api/servers/{id}", a.need(auth.Viewer, a.server))
	mux.HandleFunc("PATCH /api/servers/{id}", a.need(auth.Operator, a.update))
	mux.HandleFunc("DELETE /api/servers/{id}", a.need(auth.Admin, a.remove))
	mux.HandleFunc("POST /api/servers/{id}/{action}", a.need(auth.Operator, a.action))
	mux.HandleFunc("GET /api/servers/{id}/console", a.need(auth.Viewer, a.console))

	a.routesManage(mux)
	a.routesMods(mux)
	a.routesSecurity(mux)
	a.routesRouter(mux)
	a.routesAMPImport(mux)
	a.routesDiagnose(mux)
	a.routesModList(mux)
	a.routesWorkshop(mux)

	mux.HandleFunc("GET /api/settings", a.need(auth.Admin, a.getSettings))
	mux.HandleFunc("PUT /api/settings", a.need(auth.Admin, a.putSettings))
	mux.HandleFunc("POST /api/ai/plan", a.need(auth.Admin, a.aiPlan))
	mux.HandleFunc("POST /api/ai/test", a.need(auth.Admin, a.aiTest))
	a.routesPanelUpdate(mux)
	a.routesPlayers(mux)
	a.routesStatus(mux)
	a.routesFleet(mux)
	a.routesMove(mux)
	a.routesMacros(mux)
	a.routesWizard(mux)
	a.routesWishes(mux)
	a.routesIntegrations(mux)
	a.routesWidget(mux)
	a.routesPush(mux)
	a.routesSmart(mux)
	a.routesStreaming(mux)
	a.routesGameEvents(mux)
	a.routesDiscord(mux)
	a.routesGeo(mux)
	a.routesHistory(mux)
	a.routesStorage(mux)
	a.routesMaintenance(mux)
	a.routesInvite(mux)
	a.routesCalendar(mux)
	a.routesModpack(mux)
	a.routesSeasons(mux)
	mux.HandleFunc("GET /api/notify", a.need(auth.Admin, a.notifyGet))
	mux.HandleFunc("PUT /api/notify", a.need(auth.Admin, a.notifyPut))
	mux.HandleFunc("POST /api/notify/test", a.need(auth.Admin, a.notifyTest))

	files := http.FileServer(http.FS(a.Web))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") || strings.HasPrefix(r.URL.Path, "/import/") {
			fail(w, http.StatusNotFound, errors.New("Unbekannter Pfad"))
			return
		}
		if r.URL.Path != "/" {
			if _, err := fs.Stat(a.Web, strings.TrimPrefix(r.URL.Path, "/")); err != nil {
				r.URL.Path = "/"
			}
		}
		if r.URL.Path == "/" {
			w.Header().Set("Cache-Control", "no-cache")
		}
		files.ServeHTTP(w, r)
	})
	return secureHeaders(withLang(a.statusHost(mux)))
}

func secureHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https://cdn.modrinth.com https://steamuserimages-a.akamaihd.net https://images.steamusercontent.com https://*.steamstatic.com; connect-src 'self'")
		h.ServeHTTP(w, r)
	})
}

// ---------- session ----------

func (a *API) state(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	dockerErr := ""
	if err := a.Docker.Ping(ctx); err != nil {
		dockerErr = err.Error()
	}
	u, _ := a.Auth.Current(r)
	publicHost := ""
	if u.Name != "" {
		a.Store.Read(func(d *store.Data) { publicHost = d.Settings.PublicHost })
	}
	writeJSON(w, 200, map[string]any{
		"public_host":  publicHost,
		"setup_needed": !a.Auth.HasUsers(),
		"user":         u.Name,
		"role":         u.Role,
		"two_factor":   u.TOTP != "",
		"version":      a.Version,
		"docker_error": dockerErr,
		"lan_ip":       map[bool]string{true: manager.LANIP()}[u.Name != ""],
		"update":       map[bool]any{true: updateState(), false: nil}[u.Role == auth.Admin],
	})
}

type creds struct {
	Name     string `json:"name"`
	Password string `json:"password"`
}

func (a *API) setup(w http.ResponseWriter, r *http.Request) {
	var c creds
	if err := decode(r, &c); err != nil {
		fail(w, 400, err)
		return
	}
	if err := a.Auth.Setup(c.Name, c.Password); err != nil {
		fail(w, 400, err)
		return
	}
	a.logAuth(r, strings.TrimSpace(c.Name), "Erstes Admin-Konto angelegt", nil)
	a.login2(w, r, c)
}

func (a *API) login(w http.ResponseWriter, r *http.Request) {
	var c creds
	if err := decode(r, &c); err != nil {
		fail(w, 400, err)
		return
	}
	a.login2(w, r, c)
}

func (a *API) login2(w http.ResponseWriter, r *http.Request, c creds) {
	res, err := a.Auth.Login(r, c.Name, c.Password)
	if err != nil {
		code := 401
		if errors.Is(err, auth.ErrThrottle) {
			code = 429
		}
		user := res.User
		if user == "" {
			user = strings.TrimSpace(c.Name)
			if len(user) > 40 {
				user = user[:40]
			}
			user += " (unbekannt)"
		}
		a.logAuth(r, user, "Anmeldung fehlgeschlagen", err)
		fail(w, code, err)
		return
	}
	if res.Ticket != "" {
		writeJSON(w, 200, map[string]any{"need_code": true, "ticket": res.Ticket})
		return
	}
	a.logAuth(r, res.User, "Angemeldet", nil)
	auth.SetCookie(w, r, res.Token, auth.SessionMaxAge())
	writeJSON(w, 200, map[string]string{"user": res.User})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
	if u := a.Auth.User(r); u != "" {
		a.logAuth(r, u, "Abgemeldet", nil)
	}
	if c, err := r.Cookie(auth.CookieName); err == nil {
		a.Auth.Logout(c.Value)
	}
	auth.SetCookie(w, r, "", -1)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// ---------- data ----------

func (a *API) host(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.Host.Get())
}

func (a *API) templates(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.Mgr.Templates().All())
}

func (a *API) servers(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	list := a.Mgr.List()
	out := list[:0]
	for _, s := range list {
		if auth.CanSee(u, s.ID) {
			out = append(out, s)
		}
	}
	if a.Nodes == nil || u.ViaNode { // a node shows only its own servers
		writeJSON(w, 200, out)
		return
	}
	all := make([]any, 0, len(out))
	for _, s := range out {
		all = append(all, s)
	}
	for _, s := range a.Nodes.Servers() {
		if id, _ := s["id"].(string); auth.CanSee(u, id) {
			all = append(all, s)
		}
	}
	writeJSON(w, 200, all)
}

func (a *API) server(w http.ResponseWriter, r *http.Request) {
	sv, ok := a.Store.Server(r.PathValue("id"))
	if !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	}
	writeJSON(w, 200, a.Mgr.Info(sv))
}

func (a *API) create(w http.ResponseWriter, r *http.Request) {
	var req manager.CreateReq
	var extra struct {
		ByAI bool `json:"by_ai"`
	}
	body := map[string]json.RawMessage{}
	if err := decode(r, &body); err != nil {
		fail(w, 400, err)
		return
	}
	if nid := strings.Trim(string(body["node"]), `"`); nid != "" && nid != "null" && a.Nodes != nil {
		a.createOnNode(w, r, nid, body)
		return
	}
	raw, _ := json.Marshal(body)
	_ = json.Unmarshal(raw, &req)
	_ = json.Unmarshal(raw, &extra)
	if extra.ByAI {
		req.CreatedBy = "ai"
	}
	sv, err := a.Mgr.Create(req)
	if err != nil {
		fail(w, 400, err)
		return
	}
	by := a.Auth.User(r)
	if extra.ByAI {
		by += " mit dem KI-Assistenten"
	}
	a.Mgr.Event(sv.ID, "info", "Angelegt von "+by)
	writeJSON(w, 201, a.Mgr.Info(sv))
}

func (a *API) update(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var req struct {
		Name     *string           `json:"name"`
		MemoryMB *int              `json:"memory_mb"`
		Vars     map[string]string `json:"vars"`
		Update   *bool             `json:"update_on_start"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	sv, ok := a.Store.Server(id)
	if !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	}
	tpl, ok := a.Mgr.Templates().Get(sv.Template)
	if !ok {
		fail(w, 400, errors.New("Vorlage fehlt"))
		return
	}
	needRecreate := false
	if req.Name != nil && strings.TrimSpace(*req.Name) != "" {
		sv.Name = strings.TrimSpace(*req.Name)
	}
	if req.MemoryMB != nil && *req.MemoryMB != sv.MemoryMB {
		if *req.MemoryMB < tpl.MinMemoryMB {
			fail(w, 400, fmt.Errorf("%s braucht mindestens %d MB RAM", tpl.Name, tpl.MinMemoryMB))
			return
		}
		sv.MemoryMB = *req.MemoryMB
		needRecreate = true
	}
	if req.Update != nil && *req.Update != sv.UpdateOnRun {
		sv.UpdateOnRun = *req.Update
		needRecreate = true
	}
	if req.Vars != nil {
		merged := map[string]string{}
		for k, v := range sv.Vars {
			merged[k] = v
		}
		for k, v := range req.Vars {
			if v == "••••••" {
				continue // unchanged secret
			}
			merged[k] = v
		}
		vars, err := tpl.ResolveVars(merged)
		if err != nil {
			fail(w, 400, err)
			return
		}
		sv.Vars = vars
		needRecreate = true
	}
	if err := a.Store.ModifyServer(id, func(cur *store.Server) error {
		cur.Name, cur.MemoryMB, cur.UpdateOnRun, cur.Vars = sv.Name, sv.MemoryMB, sv.UpdateOnRun, sv.Vars
		return nil
	}); err != nil {
		fail(w, 500, err)
		return
	}
	if needRecreate {
		wasRunning := a.Mgr.Info(sv).Status != "stopped"
		a.Mgr.Recreate(id, wasRunning)
	}
	writeJSON(w, 200, a.Mgr.Info(sv))
}

func (a *API) remove(w http.ResponseWriter, r *http.Request) {
	if err := a.Mgr.Delete(r.Context(), r.PathValue("id"), r.URL.Query().Get("wipe") == "1"); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) action(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	var err error
	switch r.PathValue("action") {
	case "start":
		err = a.Mgr.Start(r.Context(), id)
	case "stop":
		err = a.Mgr.Stop(r.Context(), id)
	case "restart":
		err = a.Mgr.Restart(r.Context(), id)
	case "recreate":
		a.Mgr.Recreate(id, true)
	case "update":
		var msg string
		msg, err = a.Mgr.UpdateServer(r.Context(), id)
		if err == nil {
			a.Mgr.EventKind(id, "info", "update", "Update von "+a.Auth.User(r)+": "+msg)
		}
	case "command":
		var req struct {
			Command string `json:"command"`
		}
		if err := decode(r, &req); err != nil {
			fail(w, 400, err)
			return
		}
		Note(r, req.Command)
		out, err := a.Mgr.Command(r.Context(), id, req.Command)
		if err != nil {
			fail(w, 400, err)
			return
		}
		writeJSON(w, 200, map[string]string{"output": out})
		return
	default:
		fail(w, 404, errors.New("Unbekannte Aktion"))
		return
	}
	if err != nil {
		fail(w, 400, err)
		return
	}
	if txt := map[string]string{"start": "Gestartet", "stop": "Gestoppt", "restart": "Neu gestartet", "recreate": "Neu angelegt"}[r.PathValue("action")]; txt != "" {
		a.Mgr.EventKind(id, "info", "startstop", txt+" von "+a.Auth.User(r))
	}
	sv, _ := a.Store.Server(id)
	writeJSON(w, 200, a.Mgr.Info(sv))
}

// console streams container logs as Server-Sent Events.
func (a *API) console(w http.ResponseWriter, r *http.Request) {
	fl, ok := w.(http.Flusher)
	if !ok {
		fail(w, 500, errors.New("Streaming nicht möglich"))
		return
	}
	rc, err := a.Mgr.Logs(r.Context(), r.PathValue("id"), true, 200)
	if err != nil {
		fail(w, 400, err)
		return
	}
	defer rc.Close()
	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-store")
	w.Header().Set("X-Accel-Buffering", "no")
	lines := make(chan string, 64)
	go func() {
		defer close(lines)
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			select {
			case lines <- sc.Text():
			case <-r.Context().Done():
				return
			}
		}
	}()
	ping := time.NewTicker(20 * time.Second)
	defer ping.Stop()
	fmt.Fprint(w, ": verbunden\n\n")
	fl.Flush()
	for {
		select {
		case <-r.Context().Done():
			return
		case <-ping.C:
			fmt.Fprint(w, ": ping\n\n")
			fl.Flush()
		case l, ok := <-lines:
			if !ok {
				fmt.Fprint(w, "event: end\ndata: Der Server wurde beendet.\n\n")
				fl.Flush()
				return
			}
			l = cleanLine(l)
			if l == "" || strings.Contains(l, "Thread RCON Client") {
				continue // leere Zeilen und das Rauschen unserer eigenen RCON-Befehle
			}
			fmt.Fprintf(w, "data: %s\n\n", l)
			fl.Flush()
		}
	}
}

var ansi = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[()][A-Za-z0-9]|\x1b`)

// cleanLine removes terminal colour codes and carriage returns.
func cleanLine(l string) string {
	l = ansi.ReplaceAllString(l, "")
	if i := strings.LastIndex(l, "\r"); i >= 0 {
		if rest := l[i+1:]; strings.TrimSpace(rest) != "" {
			l = rest
		} else {
			l = strings.ReplaceAll(l, "\r", "")
		}
	}
	return strings.TrimRight(l, " \t")
}

// ---------- settings, ai ----------

func mask(s string) string {
	if len(s) < 8 {
		if s == "" {
			return ""
		}
		return "••••"
	}
	return s[:4] + "••••" + s[len(s)-4:]
}

func (a *API) getSettings(w http.ResponseWriter, r *http.Request) {
	var s store.Settings
	a.Store.Read(func(d *store.Data) { s = d.Settings })
	s.AnthropicKey = mask(s.AnthropicKey)
	s.OpenAIKey = mask(s.OpenAIKey)
	s.SteamAPIKey = mask(s.SteamAPIKey)
	s.Notify = maskNotify(s.Notify)
	writeJSON(w, 200, map[string]any{"settings": s, "default_model": ai.DefaultModel(s.AIProvider)})
}

func (a *API) putSettings(w http.ResponseWriter, r *http.Request) {
	var in store.Settings
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	switch in.AIProvider {
	case "", "anthropic", "openai", "ollama":
	default:
		fail(w, 400, errors.New("Unbekannter KI-Anbieter"))
		return
	}
	err := a.Store.Update(func(d *store.Data) error {
		s := &d.Settings
		s.AIProvider = in.AIProvider
		s.AIModel = strings.TrimSpace(in.AIModel)
		s.OllamaURL = strings.TrimSpace(in.OllamaURL)
		s.PublicHost = strings.TrimSpace(in.PublicHost)
		s.AIAutoDiagnose = in.AIAutoDiagnose
		if in.AnthropicKey != "" && !strings.Contains(in.AnthropicKey, "••") {
			s.AnthropicKey = strings.TrimSpace(in.AnthropicKey)
		}
		if in.OpenAIKey != "" && !strings.Contains(in.OpenAIKey, "••") {
			s.OpenAIKey = strings.TrimSpace(in.OpenAIKey)
		}

		return nil
	})
	if err != nil {
		fail(w, 500, err)
		return
	}
	a.getSettings(w, r)
}

func (a *API) aiContext() ai.Context {
	h := a.Host.Get()
	c := ai.Context{TotalMemMB: int(h.MemTotalMB)}
	reserved := 0
	for _, s := range a.Mgr.List() {
		c.Servers = append(c.Servers, fmt.Sprintf("id=%s: %s (%s, %s, %d MB RAM)", s.ID, s.Name, s.TemplateName, s.Status, s.MemoryMB))
		reserved += s.MemoryMB
	}
	c.FreeMemMB = int(h.MemTotalMB) - reserved - 1024
	if c.FreeMemMB < 0 {
		c.FreeMemMB = 0
	}
	return c
}

func (a *API) aiPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Prompt  string   `json:"prompt"`
		History []string `json:"history"`
		Server  string   `json:"server"` // optional: the server the chat is about
		TZ      int      `json:"tz"`     // browser offset to UTC in minutes
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	var s store.Settings
	a.Store.Read(func(d *store.Data) { s = d.Settings })
	ctx, cancel := context.WithTimeout(r.Context(), 120*time.Second)
	defer cancel()
	c := a.aiContext()
	if len(in.History) > 12 {
		in.History = in.History[len(in.History)-12:]
	}
	c.History = in.History
	if in.TZ >= -14*60 && in.TZ <= 14*60 {
		c.Now = time.Now().In(time.FixedZone("Nutzer", in.TZ*60)).Truncate(time.Minute)
	}
	focus := in.Server
	if focus == "" {
		focus = a.focusServer(in.Prompt, in.History)
	}
	if focus != "" {
		if cat, err := a.Mgr.SettingsCatalog(focus); err == nil {
			c.Focus = cat
		}
	}
	p, err := ai.MakePlan(ctx, s, a.Mgr.Templates(), c, in.Prompt)
	if err != nil {
		fail(w, 400, err)
		return
	}
	resp := map[string]any{"plan": p}
	if p.Action == "event" {
		a.aiEventPreview(w, p, resp)
		return
	}
	if p.Action == "change" {
		sv, ok := a.Store.Server(p.Server)
		if !ok {
			fail(w, 400, errors.New("Die KI meinte einen Server, den es nicht gibt. Bitte den Namen nennen"))
			return
		}
		files := map[string]map[string]string{}
		for k, v := range p.Files {
			files[k] = v
		}
		changes, notes, err := a.Mgr.CheckChanges(p.Server, p.Vars, files)
		if err != nil {
			fail(w, 400, err)
			return
		}
		if p.MemoryMB > 0 && p.MemoryMB != sv.MemoryMB {
			if tpl, ok := a.Mgr.Templates().Get(sv.Template); ok && p.MemoryMB < tpl.MinMemoryMB {
				p.MemoryMB = tpl.MinMemoryMB
			}
			changes = append(changes, manager.SettingChange{Where: "memory", Key: "memory_mb", Label: "Arbeitsspeicher (MB)", Old: strconv.Itoa(sv.MemoryMB), New: strconv.Itoa(p.MemoryMB)})
		}
		if changes == nil {
			changes = []manager.SettingChange{}
		}
		resp["changes"], resp["notes"], resp["server"] = changes, notes, a.Mgr.Info(sv)
		writeJSON(w, 200, resp)
		return
	}
	if p.Question == "" {
		if tpl, ok := a.Mgr.Templates().Get(p.Template); ok {
			resp["template"] = tpl
			if _, _, _, err := a.Mgr.Validate(manager.CreateReq{Template: p.Template, Name: p.Name, Vars: p.Vars, MemoryMB: p.MemoryMB}); err != nil {
				resp["warning"] = err.Error()
			}
		}
	}
	writeJSON(w, 200, resp)
}

func (a *API) aiTest(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	var s store.Settings
	a.Store.Read(func(d *store.Data) { s = d.Settings })
	ctx, cancel := context.WithTimeout(r.Context(), 40*time.Second)
	defer cancel()
	if err := ai.Test(ctx, s); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// focusServer guesses which existing server a chat is about: a server name
// or game name in the newest message first, then in the history.
func (a *API) focusServer(prompt string, history []string) string {
	list := a.Mgr.List()
	texts := []string{prompt}
	for i := len(history) - 1; i >= 0; i-- {
		texts = append(texts, history[i])
	}
	norm := func(x string) string { return strings.ToLower(strings.Join(strings.Fields(x), " ")) }
	for _, t := range texts {
		t = norm(t)
		for _, sv := range list {
			if n := norm(sv.Name); len(n) >= 3 && strings.Contains(t, n) {
				return sv.ID
			}
		}
		var hit []string
		for _, sv := range list {
			game := norm(strings.TrimSuffix(strings.TrimSuffix(sv.TemplateName, " (Legacy)"), " (Linux Wine)"))
			first := strings.Fields(game)
			if game != "" && (strings.Contains(t, game) || (len(first) > 0 && len(first[0]) >= 5 && strings.Contains(t, first[0]))) {
				hit = append(hit, sv.ID)
			}
		}
		if len(hit) == 1 {
			return hit[0]
		}
		if len(hit) > 1 {
			return ""
		}
	}
	return ""
}

// ---------- notifications ----------

// maskNotify hides the secret part of the webhook and the ntfy token.
func maskNotify(c notify.Config) notify.Config {
	if c.Discord != "" {
		if i := strings.LastIndex(c.Discord, "/"); i > 0 {
			c.Discord = c.Discord[:i+1] + "••••"
		}
	}
	c.NtfyToken = mask(c.NtfyToken)
	return c
}

func (a *API) notifyGet(w http.ResponseWriter, r *http.Request) {
	var c notify.Config
	a.Store.Read(func(d *store.Data) { c = d.Settings.Notify })
	writeJSON(w, 200, map[string]any{"config": maskNotify(c), "kinds": notify.Kinds, "defaults": notify.DefaultKinds})
}

func (a *API) notifyPut(w http.ResponseWriter, r *http.Request) {
	var in notify.Config
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	var cur notify.Config
	a.Store.Read(func(d *store.Data) { cur = d.Settings.Notify })
	// unchanged masked values keep the stored secret
	if strings.HasSuffix(in.Discord, "••••") {
		in.Discord = cur.Discord
	}
	if strings.Contains(in.NtfyToken, "••••") {
		in.NtfyToken = cur.NtfyToken
	}
	if in.Kinds == nil {
		in.Kinds = []string{}
	}
	if err := in.Validate(); err != nil {
		fail(w, 400, err)
		return
	}
	_ = a.Store.Update(func(d *store.Data) error { d.Settings.Notify = in; return nil })
	a.notifyGet(w, r)
}

func (a *API) notifyTest(w http.ResponseWriter, r *http.Request) {
	var c notify.Config
	a.Store.Read(func(d *store.Data) { c = d.Settings.Notify })
	if !c.Enabled() {
		fail(w, 400, errors.New("Erst Discord oder ntfy eintragen und speichern"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := notify.Send(ctx, c, notify.Message{Title: "Bytewise Panel", Text: "Testnachricht von " + a.Auth.User(r) + ": Benachrichtigungen kommen an.", Level: "info"}); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

var (
	errNotFound = errors.New("Nicht gefunden")
	errTooLong  = errors.New("Text zu lang")
)
