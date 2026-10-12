package api

import (
	"context"
	"net/http"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
)

// AI diagnosis of a single server.
func (a *API) routesDiagnose(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/diagnose", a.need(auth.Viewer, a.diagList))
	mux.HandleFunc("POST /api/servers/{id}/diagnose", a.need(auth.Operator, a.diagRun))
}

func (a *API) diagList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, map[string]any{"diagnoses": a.Mgr.Diagnoses(r.PathValue("id")), "ai": a.License().Edition.AI})
}

func (a *API) diagRun(w http.ResponseWriter, r *http.Request) {
	if !a.aiAllowed(w) {
		return
	}
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
