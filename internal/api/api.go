// Package api exposes the panel's HTTP API and serves the web UI.
package api

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/ai"
	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/hoststats"
	"github.com/derface/Gameserver-controlPanel/internal/license"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
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
}

func (a *API) License() license.Status {
	key := ""
	a.Store.Read(func(d *store.Data) { key = d.License })
	return license.Check(key)
}

func writeJSON(w http.ResponseWriter, code int, v any) {
	w.Header().Set("Content-Type", "application/json; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	w.WriteHeader(code)
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

// guard requires a session and, for changing requests, the X-Panel header
// (browsers cannot send it cross-site without CORS, which we never allow).
func (a *API) guard(h http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodGet && r.Header.Get("X-Panel") != "1" {
			fail(w, http.StatusForbidden, errors.New("Anfrage abgelehnt"))
			return
		}
		if a.Auth.User(r) == "" {
			fail(w, http.StatusUnauthorized, errors.New("Bitte anmelden"))
			return
		}
		h(w, r)
	}
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

	mux.HandleFunc("GET /api/host", a.guard(a.host))
	mux.HandleFunc("GET /api/templates", a.guard(a.templates))
	mux.HandleFunc("GET /api/servers", a.guard(a.servers))
	mux.HandleFunc("POST /api/servers", a.guard(a.create))
	mux.HandleFunc("GET /api/servers/{id}", a.guard(a.server))
	mux.HandleFunc("PATCH /api/servers/{id}", a.guard(a.update))
	mux.HandleFunc("DELETE /api/servers/{id}", a.guard(a.remove))
	mux.HandleFunc("POST /api/servers/{id}/{action}", a.guard(a.action))
	mux.HandleFunc("GET /api/servers/{id}/console", a.guard(a.console))

	a.routesManage(mux)
	a.routesMods(mux)

	mux.HandleFunc("GET /api/settings", a.guard(a.getSettings))
	mux.HandleFunc("PUT /api/settings", a.guard(a.putSettings))
	mux.HandleFunc("PUT /api/license", a.guard(a.putLicense))
	mux.HandleFunc("POST /api/ai/plan", a.guard(a.aiPlan))
	mux.HandleFunc("POST /api/ai/test", a.guard(a.aiTest))

	files := http.FileServer(http.FS(a.Web))
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if strings.HasPrefix(r.URL.Path, "/api/") {
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
	return secureHeaders(mux)
}

func secureHeaders(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Content-Type-Options", "nosniff")
		w.Header().Set("X-Frame-Options", "DENY")
		w.Header().Set("Referrer-Policy", "same-origin")
		w.Header().Set("Content-Security-Policy", "default-src 'self'; style-src 'self' 'unsafe-inline' https://fonts.googleapis.com; font-src 'self' https://fonts.gstatic.com; img-src 'self' data: https://cdn.modrinth.com; connect-src 'self'")
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
	writeJSON(w, 200, map[string]any{
		"setup_needed": !a.Auth.HasUsers(),
		"user":         a.Auth.User(r),
		"version":      a.Version,
		"docker_error": dockerErr,
		"license":      a.License(),
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
	tok, err := a.Auth.Login(r, c.Name, c.Password)
	if err != nil {
		code := 401
		if errors.Is(err, auth.ErrThrottle) {
			code = 429
		}
		fail(w, code, err)
		return
	}
	auth.SetCookie(w, r, tok, auth.SessionMaxAge())
	writeJSON(w, 200, map[string]string{"user": strings.TrimSpace(c.Name)})
}

func (a *API) logout(w http.ResponseWriter, r *http.Request) {
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
	writeJSON(w, 200, a.Mgr.List())
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
			a.Mgr.Event(id, "info", "Update von "+a.Auth.User(r)+": "+msg)
		}
	case "command":
		var req struct {
			Command string `json:"command"`
		}
		if err := decode(r, &req); err != nil {
			fail(w, 400, err)
			return
		}
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
		a.Mgr.Event(id, "info", txt+" von "+a.Auth.User(r))
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

// ---------- settings, license, ai ----------

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

func (a *API) putLicense(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	key := strings.TrimSpace(in.Key)
	if key != "" {
		if _, err := license.Verify(key); err != nil {
			fail(w, 400, err)
			return
		}
	}
	_ = a.Store.Update(func(d *store.Data) error { d.License = key; return nil })
	writeJSON(w, 200, a.License())
}

func (a *API) aiContext() ai.Context {
	h := a.Host.Get()
	c := ai.Context{TotalMemMB: int(h.MemTotalMB)}
	reserved := 0
	for _, s := range a.Mgr.List() {
		c.Servers = append(c.Servers, fmt.Sprintf("%s (%s, %s)", s.Name, s.TemplateName, s.Status))
		reserved += s.MemoryMB
	}
	c.FreeMemMB = int(h.MemTotalMB) - reserved - 1024
	if c.FreeMemMB < 0 {
		c.FreeMemMB = 0
	}
	return c
}

func (a *API) aiAllowed(w http.ResponseWriter) bool {
	if !a.License().Edition.AI {
		fail(w, 403, errors.New("Der KI-Assistent ist in deiner Edition nicht enthalten"))
		return false
	}
	return true
}

func (a *API) aiPlan(w http.ResponseWriter, r *http.Request) {
	if !a.aiAllowed(w) {
		return
	}
	var in struct {
		Prompt string `json:"prompt"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	var s store.Settings
	a.Store.Read(func(d *store.Data) { s = d.Settings })
	ctx, cancel := context.WithTimeout(r.Context(), 100*time.Second)
	defer cancel()
	p, err := ai.MakePlan(ctx, s, a.Mgr.Templates(), a.aiContext(), in.Prompt)
	if err != nil {
		fail(w, 400, err)
		return
	}
	resp := map[string]any{"plan": p}
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
