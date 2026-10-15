package api

import (
	"context"
	"net/http"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
)

// Player origin map.

func (a *API) routesGeo(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/geo", a.need(auth.Viewer, a.geoServer))
	mux.HandleFunc("GET /api/geo", a.need(auth.Viewer, a.geoAll))
	mux.HandleFunc("PUT /api/geo", a.need(auth.Admin, a.geoPut))
	mux.HandleFunc("POST /api/geo/update", a.need(auth.Admin, a.geoUpdate))
}

func (a *API) geoServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.Store.Server(id); !ok {
		fail(w, 404, errNotFound)
		return
	}
	writeJSON(w, 200, a.geoView(r, []string{id}))
}

func (a *API) geoAll(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var ids []string
	for _, s := range a.Store.Servers() {
		if auth.CanSee(u, s.ID) {
			ids = append(ids, s.ID)
		}
	}
	writeJSON(w, 200, a.geoView(r, ids))
}

// geoView hides setup details from non-admins.
func (a *API) geoView(r *http.Request, ids []string) manager.GeoView {
	v := a.Mgr.Geo(ids)
	if userOf(r).Role != auth.Admin {
		v.SourceErr = ""
		v.DB.Err = ""
		v.Debug = nil
	}
	return v
}

func (a *API) geoPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if err := a.Mgr.SetGeo(in.Enabled); err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"enabled": in.Enabled})
}

func (a *API) geoUpdate(w http.ResponseWriter, r *http.Request) {
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
		defer cancel()
		_ = a.Mgr.GeoDB().Update(ctx, true)
	}()
	writeJSON(w, 200, map[string]bool{"ok": true})
}
