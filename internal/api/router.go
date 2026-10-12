package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func (a *API) routesRouter(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/router", a.need(auth.Admin, a.routerGet))
	mux.HandleFunc("PUT /api/router", a.need(auth.Admin, a.routerPut))
	mux.HandleFunc("POST /api/router/check", a.need(auth.Admin, a.routerCheck))
	mux.HandleFunc("PUT /api/servers/{id}/router", a.need(auth.Admin, a.serverRouterPut))
	mux.HandleFunc("POST /api/servers/{id}/reach", a.need(auth.Operator, a.serverReach))
}

// serverReach checks whether players can reach the server from the internet.
func (a *API) serverReach(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	ctx, cancel := context.WithTimeout(r.Context(), 45*time.Second)
	defer cancel()
	rep, err := a.Mgr.CheckReach(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, rep)
}

func (a *API) routerGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.Mgr.RouterStatus())
}

func (a *API) syncRouter(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	writeJSON(w, 200, a.Mgr.SyncRouter(ctx))
}

func (a *API) routerPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if err := a.Store.Update(func(d *store.Data) error { d.Settings.UPnP = in.Enabled; return nil }); err != nil {
		fail(w, 500, err)
		return
	}
	Note(r, map[bool]string{true: "eingeschaltet", false: "ausgeschaltet"}[in.Enabled])
	a.syncRouter(w, r)
}

func (a *API) routerCheck(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	a.syncRouter(w, r)
}

func (a *API) serverRouterPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Open bool `json:"open"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	id := r.PathValue("id")
	if err := a.Store.ModifyServer(id, func(sv *store.Server) error { sv.NoRouter = !in.Open; return nil }); err != nil {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	}
	Note(r, map[bool]string{true: "Ports öffnen", false: "Ports nicht öffnen"}[in.Open])
	ctx, cancel := context.WithTimeout(r.Context(), 70*time.Second)
	defer cancel()
	a.Mgr.SyncRouter(ctx)
	sv, _ := a.Store.Server(id)
	writeJSON(w, 200, a.Mgr.Info(sv))
}
