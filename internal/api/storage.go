package api

import (
	"context"
	"errors"
	"net/http"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
)

func (a *API) routesStorage(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/storage", a.need(auth.Admin, a.storageGet))
	mux.HandleFunc("POST /api/storage/prune", a.need(auth.Admin, a.storagePrune))
	mux.HandleFunc("DELETE /api/storage/orphan/{id}", a.need(auth.Admin, a.storageOrphan))
}

func (a *API) storageGet(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	writeJSON(w, 200, a.Mgr.Storage(r.URL.Query().Get("refresh") == "1"))
}

func (a *API) storagePrune(w http.ResponseWriter, r *http.Request) {
	var in struct {
		What string `json:"what"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, in.What)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	n, err := a.Mgr.PruneDocker(ctx, in.What)
	if err != nil {
		fail(w, 400, errors.New("Aufräumen fehlgeschlagen: "+err.Error()))
		return
	}
	writeJSON(w, 200, map[string]int64{"freed": n})
}

func (a *API) storageOrphan(w http.ResponseWriter, r *http.Request) {
	Note(r, r.PathValue("id"))
	if err := a.Mgr.DeleteOrphan(r.PathValue("id"), r.URL.Query().Get("kind")); err != nil {
		fail(w, 400, errors.New("Ordner kann nicht gelöscht werden"))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
