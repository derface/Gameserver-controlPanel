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
