package api

import (
	"context"
	"errors"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
)

// AI diagnosis of a single server.
func (a *API) routesDiagnose(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/diagnose", a.need(auth.Viewer, a.diagList))
	mux.HandleFunc("POST /api/servers/{id}/diagnose", a.need(auth.Operator, a.diagRun))
}

func (a *API) diagList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"diagnoses": a.Mgr.Diagnoses(r.PathValue("id")), "ai": true})
}

func (a *API) diagRun(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	d, err := a.Mgr.Diagnose(ctx, r.PathValue("id"), "")
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, d.Severity+": "+d.Summary)
	writeJSON(w, 200, d)
}

// Installed and loaded mods of a server.
func (a *API) routesModList(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/modlist", a.need(auth.Viewer, a.modList))
	mux.HandleFunc("POST /api/servers/{id}/modlist/toggle", a.need(auth.Operator, a.modToggle))
}

func (a *API) modList(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	inv, err := a.Mgr.ModList(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, inv)
}

func (a *API) modToggle(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
		On  bool   `json:"on"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	msg, err := a.Mgr.ToggleMod(ctx, r.PathValue("id"), in.Key, in.On)
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, in.Key+map[bool]string{true: " an", false: " aus"}[in.On])
	writeJSON(w, 200, map[string]string{"message": msg})
}

// Steam Workshop search and install.
func (a *API) routesWorkshop(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/workshop", a.need(auth.Operator, a.wsSearch))
	mux.HandleFunc("POST /api/servers/{id}/workshop/install", a.need(auth.Operator, a.wsInstall))
	mux.HandleFunc("POST /api/servers/{id}/workshop/remove", a.need(auth.Operator, a.wsRemove))
	mux.HandleFunc("GET /api/servers/{id}/workshop/info", a.need(auth.Viewer, a.wsInfo))
	mux.HandleFunc("POST /api/servers/{id}/workshop/advice", a.need(auth.Operator, a.wsAdvice))
	mux.HandleFunc("PUT /api/settings/steam-api-key", a.need(auth.Admin, a.wsKey))
}

func (a *API) wsInfo(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	in, err := a.Mgr.WorkshopInfoFor(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]any{"info": in, "ai": true})
}

func (a *API) wsAdvice(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	adv, err := a.Mgr.WorkshopAdvice(ctx, r.PathValue("id"), in.ID)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, adv)
}

func (a *API) wsSearch(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	q := r.URL.Query()
	page, _ := strconv.Atoi(q.Get("page"))
	var b42 *bool
	if v := q.Get("b42"); v != "" {
		x := v == "1"
		b42 = &x
	}
	res, err := a.Mgr.WorkshopSearch(ctx, r.PathValue("id"), q.Get("q"), page, b42)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, res)
}

func (a *API) wsInstall(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID     string   `json:"id"`
		ModIDs []string `json:"mod_ids"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	msg, err := a.Mgr.WorkshopInstall(ctx, r.PathValue("id"), in.ID, in.ModIDs)
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, "Workshop "+in.ID)
	writeJSON(w, 200, map[string]string{"message": msg})
}

func (a *API) wsRemove(w http.ResponseWriter, r *http.Request) {
	var in struct {
		ID string `json:"id"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 3*time.Minute)
	defer cancel()
	msg, err := a.Mgr.WorkshopRemove(ctx, r.PathValue("id"), in.ID)
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, "Workshop "+in.ID)
	writeJSON(w, 200, map[string]string{"message": msg})
}

func (a *API) wsKey(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Key string `json:"key"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	k := strings.TrimSpace(in.Key)
	if k != "" && !regexp.MustCompile(`^[0-9A-Fa-f]{32}$`).MatchString(k) {
		fail(w, 400, errors.New("Ein Steam-Web-API-Schlüssel hat 32 Zeichen (0-9, A-F)"))
		return
	}
	_ = a.Store.Update(func(d *store.Data) error { d.Settings.SteamAPIKey = k; return nil })
	Note(r, map[bool]string{true: "gesetzt", false: "entfernt"}[k != ""])
	writeJSON(w, 200, map[string]string{"key": mask(k)})
}
