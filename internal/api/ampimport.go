package api

import (
	"errors"
	"net/http"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/audit"
	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
)

// Moving servers over from CubeCoders AMP.
func (a *API) routesAMPImport(mux *http.ServeMux) {
	// the AMP machine fetches the script and uploads with a one-time token
	mux.HandleFunc("GET /import/amp.py", a.ampScript)
	mux.HandleFunc("POST /import/amp", a.ampUpload)
	mux.HandleFunc("POST /api/import/amp/token", a.need(auth.Admin, a.ampToken))
	mux.HandleFunc("GET /api/import/amp", a.need(auth.Admin, a.ampList))
	mux.HandleFunc("DELETE /api/import/amp/{iid}", a.need(auth.Admin, a.ampDelete))
	mux.HandleFunc("POST /api/import/amp/{iid}/create", a.need(auth.Admin, a.ampCreate))
}

func (a *API) ampScript(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/x-python; charset=utf-8")
	w.Header().Set("Cache-Control", "no-store")
	_, _ = w.Write(manager.AMPExportScript())
}

func (a *API) ampUpload(w http.ResponseWriter, r *http.Request) {
	tok := r.URL.Query().Get("token")
	if !a.Mgr.AMPTokenValid(tok) {
		fail(w, http.StatusForbidden, errors.New("Der Schlüssel ist ungültig oder abgelaufen. Im Panel einen neuen Befehl holen"))
		return
	}
	inst := r.URL.Query().Get("instance")
	it, err := a.Mgr.ReceiveAMP(r.Body, inst)
	e := audit.Entry{User: "AMP-Export", IP: auth.ClientIP(r), Action: "AMP-Instanz empfangen", Target: inst, OK: err == nil}
	if err != nil {
		e.Error = err.Error()
	} else {
		e.Detail = it.Game
	}
	if a.Audit != nil {
		a.Audit.Add(e)
	}
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 201, it)
}

func (a *API) ampToken(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	t, exp := a.Mgr.NewAMPToken()
	writeJSON(w, 200, map[string]any{"token": t, "expires": exp.Format(time.RFC3339)})
}

func (a *API) ampList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.Mgr.AMPImports())
}

func (a *API) ampDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.Mgr.DeleteAMPImport(r.PathValue("iid")); err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) ampCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name string `json:"name"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	sv, err := a.Mgr.ImportAMP(r.PathValue("iid"), in.Name, "amp-import")
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, sv.Name)
	a.Mgr.Event(sv.ID, "info", "Angelegt von "+a.Auth.User(r)+" (Übernahme aus AMP)")
	writeJSON(w, 201, a.Mgr.Info(sv))
}
