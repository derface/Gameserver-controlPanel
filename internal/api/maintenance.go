package api

import (
	"net/http"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
)

func (a *API) routesMaintenance(mux *http.ServeMux) {
	mux.HandleFunc("PUT /api/servers/{id}/maintenance", a.need(auth.Operator, a.maintenancePut))
	mux.HandleFunc("DELETE /api/servers/{id}/maintenance", a.need(auth.Operator, a.maintenanceDelete))
}

func (a *API) maintenancePut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Until      time.Time `json:"until"`
		Note       string    `json:"note"`
		ClosePorts bool      `json:"close_ports"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, in.Note)
	if err := a.Mgr.SetMaintenance(r.PathValue("id"), in.Until, in.Note, in.ClosePorts, a.Auth.User(r)); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) maintenanceDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.Mgr.EndMaintenance(r.PathValue("id"), " von "+a.Auth.User(r)); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
