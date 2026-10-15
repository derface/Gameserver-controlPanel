package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
)

func (a *API) routesSeasons(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/seasons", a.need(auth.Viewer, a.seasonsGet))
	mux.HandleFunc("POST /api/servers/{id}/seasons", a.need(auth.Admin, a.seasonsStart))
	mux.HandleFunc("PUT /api/servers/{id}/seasons/plan", a.need(auth.Admin, a.seasonsPlan))
}

func (a *API) seasonsGet(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	v, err := a.Mgr.Seasons(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, v)
}

type seasonIn struct {
	Name    string    `json:"name"`
	Wipe    []string  `json:"wipe"`
	At      time.Time `json:"at"`
	Confirm string    `json:"confirm"` // the server's name, typed by hand
}

func (a *API) seasonsStart(w http.ResponseWriter, r *http.Request) {
	var in seasonIn
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	sv, ok := a.Store.Server(r.PathValue("id"))
	if !ok {
		fail(w, 404, errNotFound)
		return
	}
	if !strings.EqualFold(strings.TrimSpace(in.Confirm), sv.Name) {
		fail(w, 400, errors.New("Zur Sicherheit bitte den Servernamen genau eintippen"))
		return
	}
	Note(r, in.Name+": "+strings.Join(in.Wipe, ", "))
	if err := a.Mgr.StartSeason(sv.ID, in.Name, in.Wipe, a.Auth.User(r)); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) seasonsPlan(w http.ResponseWriter, r *http.Request) {
	var in seasonIn
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	sv, ok := a.Store.Server(r.PathValue("id"))
	if !ok {
		fail(w, 404, errNotFound)
		return
	}
	if !in.At.IsZero() && !strings.EqualFold(strings.TrimSpace(in.Confirm), sv.Name) {
		fail(w, 400, errors.New("Zur Sicherheit bitte den Servernamen genau eintippen"))
		return
	}
	Note(r, in.Name)
	if err := a.Mgr.PlanSeason(sv.ID, in.At, in.Name, in.Wipe); err != nil {
		fail(w, 400, err)
		return
	}
	v, _ := a.Mgr.Seasons(sv.ID)
	writeJSON(w, 200, v)
}
