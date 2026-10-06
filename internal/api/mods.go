package api

import (
	"context"
	"errors"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/mods"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

func (a *API) routesMods(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/mods", a.guard(a.modsGet))
	mux.HandleFunc("GET /api/servers/{id}/mods/search", a.guard(a.modsSearch))
	mux.HandleFunc("PUT /api/servers/{id}/mods", a.guard(a.modsPut))
}

type modState struct {
	Supported   bool           `json:"supported"`
	Reason      string         `json:"reason,omitempty"`
	ProjectType string         `json:"project_type,omitempty"`
	Loader      string         `json:"loader,omitempty"`
	GameVersion string         `json:"game_version,omitempty"`
	Slugs       []string       `json:"slugs"`
	Installed   []mods.Project `json:"installed"`
	LookupError string         `json:"lookup_error,omitempty"`
	Running     bool           `json:"running"`
}

func (a *API) modContext(id string) (store.Server, templates.Template, mods.Target, error) {
	sv, ok := a.Store.Server(id)
	if !ok {
		return sv, templates.Template{}, mods.Target{}, errors.New("Server nicht gefunden")
	}
	tpl, ok := a.Mgr.Templates().Get(sv.Template)
	if !ok || tpl.Mods == nil || tpl.Mods.Source != "modrinth" {
		return sv, tpl, mods.Target{}, errors.New("Für dieses Spiel gibt es im Panel noch keine Mod-Verwaltung")
	}
	t, ok := mods.TargetFor(sv.Vars[tpl.Mods.TypeVar], sv.Vars[tpl.Mods.VersionVar])
	if !ok {
		return sv, tpl, t, errors.New("Die Server-Art " + sv.Vars[tpl.Mods.TypeVar] + " unterstützt keine Mods oder Plugins. Stelle unter Einstellungen auf Paper (Plugins) oder Fabric, NeoForge oder Forge (Mods) um")
	}
	return sv, tpl, t, nil
}

func (a *API) modState(ctx context.Context, id string) modState {
	st := modState{Slugs: []string{}, Installed: []mods.Project{}, Running: a.Mgr.Info(mustServer(a, id)).Status == "running"}
	sv, tpl, t, err := a.modContext(id)
	if err != nil {
		st.Reason = err.Error()
		if tpl.Mods != nil {
			st.Slugs = mods.ParseList(sv.Vars[tpl.Mods.Var])
		}
		return st
	}
	st.Supported, st.ProjectType, st.Loader, st.GameVersion = true, t.ProjectType, t.Loader, t.GameVersion
	st.Slugs = mods.ParseList(sv.Vars[tpl.Mods.Var])
	if len(st.Slugs) > 0 {
		lctx, cancel := context.WithTimeout(ctx, 10*time.Second)
		defer cancel()
		ps, err := mods.Lookup(lctx, st.Slugs)
		if err != nil {
			st.LookupError = err.Error()
		} else {
			st.Installed = ps
		}
	}
	return st
}

func mustServer(a *API, id string) store.Server {
	sv, _ := a.Store.Server(id)
	return sv
}

func (a *API) modsGet(w http.ResponseWriter, r *http.Request) {
	if _, ok := a.Store.Server(r.PathValue("id")); !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	}
	writeJSON(w, 200, a.modState(r.Context(), r.PathValue("id")))
}

func (a *API) modsSearch(w http.ResponseWriter, r *http.Request) {
	_, _, t, err := a.modContext(r.PathValue("id"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	q := r.URL.Query().Get("q")
	if len(q) > 100 {
		q = q[:100]
	}
	off, _ := strconv.Atoi(r.URL.Query().Get("offset"))
	ctx, cancel := context.WithTimeout(r.Context(), 15*time.Second)
	defer cancel()
	hits, total, err := mods.Search(ctx, t, q, off)
	if err != nil {
		fail(w, 502, err)
		return
	}
	writeJSON(w, 200, map[string]any{"hits": hits, "total": total})
}

func (a *API) modsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Slugs []string `json:"slugs"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	id := r.PathValue("id")
	sv, tpl, _, err := a.modContext(id)
	if err != nil && (tpl.Mods == nil || len(req.Slugs) > 0) {
		fail(w, 400, err) // removing mods is always allowed, adding needs a supported type
		return
	}
	if len(req.Slugs) > 200 {
		fail(w, 400, errors.New("Höchstens 200 Mods"))
		return
	}
	var clean []string
	seen := map[string]bool{}
	for _, s := range req.Slugs {
		s = strings.TrimSpace(s)
		if !mods.ValidSlug(s) {
			fail(w, 400, errors.New("Ungültiger Mod-Name: "+s))
			return
		}
		if !seen[strings.ToLower(s)] {
			seen[strings.ToLower(s)] = true
			clean = append(clean, s)
		}
	}
	old := mods.ParseList(sv.Vars[tpl.Mods.Var])
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
	defer cancel()
	if _, err := a.Mgr.ApplySettings(ctx, id, map[string]string{tpl.Mods.Var: strings.Join(clean, ",")}, nil); err != nil {
		fail(w, 400, err)
		return
	}
	var added, removed []string
	oldSet := map[string]bool{}
	for _, s := range old {
		oldSet[strings.ToLower(s)] = true
	}
	for _, s := range clean {
		if !oldSet[strings.ToLower(s)] {
			added = append(added, s)
		}
		delete(oldSet, strings.ToLower(s))
	}
	for _, s := range old {
		if oldSet[strings.ToLower(s)] {
			removed = append(removed, s)
		}
	}
	if len(added)+len(removed) > 0 {
		msg := "Mods geändert von " + a.Auth.User(r) + ":"
		if len(added) > 0 {
			msg += " hinzu " + strings.Join(added, ", ")
		}
		if len(removed) > 0 {
			msg += " entfernt " + strings.Join(removed, ", ")
		}
		a.Mgr.Event(id, "info", msg)
	}
	writeJSON(w, 200, a.modState(r.Context(), id))
}
