package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Client mods for players and mod updates for admins.

func (a *API) routesModpack(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/mod-updates", a.need(auth.Viewer, a.modUpdates))
	mux.HandleFunc("GET /api/servers/{id}/client-mods", a.need(auth.Viewer, a.clientMods))
	mux.HandleFunc("GET /api/public/mods/{key}", a.publicMods)
	mux.HandleFunc("GET /api/public/modpack/{file}", a.publicModpack)
}

func (a *API) modUpdates(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	id := r.PathValue("id")
	if _, ok := a.Store.Server(id); !ok {
		fail(w, 404, errNotFound)
		return
	}
	if r.URL.Query().Get("check") == "1" || a.Mgr.PendingModUpdates(id) == nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
		defer cancel()
		writeJSON(w, 200, a.Mgr.CheckModUpdates(ctx, id))
		return
	}
	writeJSON(w, 200, a.Mgr.PendingModUpdates(id))
}

func (a *API) clientMods(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	cm, err := a.Mgr.ClientMods(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, cm)
}

// publicServerFor resolves a public key: an invite token, or "s-<id>" for a
// server shown on the status page.
func (a *API) publicServerFor(key string) (store.Server, bool) {
	if id, ok := strings.CutPrefix(key, "s-"); ok {
		c := a.statusCfg()
		if e, shown := c.Servers[id]; c.Enabled && shown && e.Show {
			return a.Store.Server(id)
		}
		return store.Server{}, false
	}
	if len(key) < 16 {
		return store.Server{}, false
	}
	for _, s := range a.Store.Servers() {
		if s.Invite != nil && s.Invite.Token == key {
			return s, true
		}
	}
	return store.Server{}, false
}

func (a *API) publicMods(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	sv, ok := a.publicServerFor(r.PathValue("key"))
	if !ok {
		if key := r.PathValue("key"); !strings.HasPrefix(key, "s-") && a.publicFromNodes(w, r, "/api/public/mods/"+key, "") {
			return
		} else if strings.HasPrefix(key, "s-") && a.remoteMods(w, r, key) {
			return
		}
		fail(w, 404, errNotFound)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	cm, err := a.Mgr.ClientMods(ctx, sv.ID)
	if err != nil {
		fail(w, 400, err)
		return
	}
	dl := ""
	if cm.Download && a.statusCfg().ModDownload {
		dl = "/api/public/modpack/" + r.PathValue("key") + ".zip"
	}
	writeJSON(w, 200, map[string]any{"mods": cm.Mods, "note": cm.Note, "download": dl})
}

var modpackSlots = make(chan struct{}, 2)

func (a *API) publicModpack(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	key := strings.TrimSuffix(r.PathValue("file"), ".zip")
	sv, ok := a.publicServerFor(key)
	if !ok && !strings.HasPrefix(key, "s-") && a.publicFromNodes(w, r, "/api/public/mods/"+key, "/api/public/modpack/"+key+".zip") {
		return
	}
	if !ok && strings.HasPrefix(key, "s-") && a.remoteModpack(w, r, key) {
		return
	}
	if !ok || !a.statusCfg().ModDownload {
		http.NotFound(w, r)
		return
	}
	select {
	case modpackSlots <- struct{}{}:
		defer func() { <-modpackSlots }()
	default:
		fail(w, 429, errors.New("Gerade laden schon zwei Leute das Mod-Paket. Bitte gleich nochmal versuchen."))
		return
	}
	name := manager.SafeFileName(sv.Name) + "-mods.zip"
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	_ = a.Mgr.WriteModPack(ctx, sv.ID, w)
}
