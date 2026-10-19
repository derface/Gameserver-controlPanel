package api

import (
	"errors"
	"net/http"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func (a *API) routesMacros(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/macros", a.need(auth.Viewer, a.macrosGet))
	mux.HandleFunc("PUT /api/servers/{id}/macros", a.need(auth.Operator, a.macrosPut))
	mux.HandleFunc("POST /api/servers/{id}/macros/{mid}/run", a.need(auth.Operator, a.macroRun))
}

func (a *API) macrosGet(w http.ResponseWriter, r *http.Request) {
	sv, ok := a.Store.Server(r.PathValue("id"))
	if !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	}
	list := sv.Macros
	if list == nil {
		list = []store.Macro{}
	}
	writeJSON(w, 200, map[string]any{"macros": list})
}

func (a *API) macrosPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Macros []store.Macro `json:"macros"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	list, err := a.Mgr.SetMacros(r.PathValue("id"), in.Macros)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if list == nil {
		list = []store.Macro{}
	}
	writeJSON(w, 200, map[string]any{"macros": list})
}

func (a *API) macroRun(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sv, _ := a.Store.Server(id)
	if mc, ok := manager.FindMacro(sv, r.PathValue("mid")); ok {
		Note(r, mc.Name)
	}
	if err := a.Mgr.RunMacro(id, r.PathValue("mid"), a.Auth.User(r)); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 202, map[string]bool{"ok": true})
}
