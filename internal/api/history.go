package api

import (
	"net/http"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
)

// Performance history for charts.

func (a *API) routesHistory(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/history", a.need(auth.Viewer, a.historyServer))
	mux.HandleFunc("GET /api/host/history", a.need(auth.Viewer, a.historyHost))
}

func spanOf(r *http.Request) time.Duration {
	switch r.URL.Query().Get("span") {
	case "7d":
		return 7 * 24 * time.Hour
	case "30d":
		return 30 * 24 * time.Hour
	}
	return 24 * time.Hour
}

func (a *API) historyServer(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	id := r.PathValue("id")
	if _, ok := a.Store.Server(id); !ok {
		fail(w, 404, errNotFound)
		return
	}
	writeJSON(w, 200, a.Mgr.History(id, spanOf(r)))
}

func (a *API) historyHost(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	writeJSON(w, 200, a.Mgr.History(manager.HostID, spanOf(r)))
}
