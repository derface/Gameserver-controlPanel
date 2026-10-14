package api

import (
	"context"
	"net/http"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
)

func (a *API) routesPlayers(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/players", a.need(auth.Viewer, a.playersGet))
	mux.HandleFunc("POST /api/servers/{id}/players", a.need(auth.Operator, a.playersAction))
	mux.HandleFunc("GET /api/servers/{id}/stats", a.need(auth.Viewer, a.statsGet))
	mux.HandleFunc("GET /api/insights", a.need(auth.Viewer, a.insightsGet))
}

func (a *API) playersGet(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	v, err := a.Mgr.Players(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *API) playersAction(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Action string `json:"action"`
		Name   string `json:"name"`
		List   string `json:"list"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	msg, err := a.Mgr.PlayerAction(ctx, r.PathValue("id"), in.Action, in.Name, in.List)
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, in.Action+" "+in.Name)
	writeJSON(w, 200, map[string]string{"message": msg})
}

func (a *API) statsGet(w http.ResponseWriter, r *http.Request) {
	v, err := a.Mgr.Stats(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *API) insightsGet(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	out := []map[string]string{}
	for _, x := range a.Mgr.Insights() {
		if auth.CanSee(u, x["server"]) {
			out = append(out, x)
		}
	}
	writeJSON(w, 200, out)
}
