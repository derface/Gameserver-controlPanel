package api

import (
	"context"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/ai"
	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func (a *API) routesGameEvents(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/game-events", a.need(auth.Viewer, a.gameEventsGet))
	mux.HandleFunc("POST /api/servers/{id}/game-events", a.need(auth.Operator, a.gameEventsPost))
	mux.HandleFunc("DELETE /api/servers/{id}/game-events/{eid}", a.need(auth.Operator, a.gameEventsDelete))
}

func (a *API) gameEventsGet(w http.ResponseWriter, r *http.Request) {
	sv, ok := a.Store.Server(r.PathValue("id"))
	if !ok {
		fail(w, 404, errNotFound)
		return
	}
	evs := sv.GameEvents
	if evs == nil {
		evs = []store.GameEvent{}
	}
	writeJSON(w, 200, map[string]any{"events": evs, "can_broadcast": a.canBroadcast(sv.Template)})
}

func (a *API) canBroadcast(tplID string) bool {
	tpl, ok := a.Mgr.Templates().Get(tplID)
	return ok && tpl.Broadcast != ""
}

func (a *API) gameEventsPost(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name   string                       `json:"name"`
		Start  time.Time                    `json:"start"`
		End    time.Time                    `json:"end"`
		Weekly bool                         `json:"weekly"`
		Vars   map[string]string            `json:"vars"`
		Files  map[string]map[string]string `json:"files"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	id := r.PathValue("id")
	ev, notes, err := a.Mgr.PlanEvent(id, store.GameEvent{Name: in.Name, Start: in.Start, End: in.End, Weekly: in.Weekly}, in.Vars, in.Files)
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, ev.Name)
	writeJSON(w, 200, map[string]any{"event": ev, "notes": notes})
}

func (a *API) gameEventsDelete(w http.ResponseWriter, r *http.Request) {
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	if err := a.Mgr.DeleteEvent(ctx, r.PathValue("id"), r.PathValue("eid")); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// aiEventPreview checks an event the AI proposed. Nothing is saved: the UI
// shows the values and the period and posts to game-events when confirmed.
func (a *API) aiEventPreview(w http.ResponseWriter, p ai.Plan, resp map[string]any) {
	sv, ok := a.Store.Server(p.Server)
	if !ok {
		fail(w, 400, errors.New("Die KI meinte einen Server, den es nicht gibt. Bitte den Namen nennen"))
		return
	}
	start, err1 := time.Parse(time.RFC3339, p.Start)
	end, err2 := time.Parse(time.RFC3339, p.End)
	if err1 != nil || err2 != nil || !end.After(start) {
		fail(w, 400, errors.New("Die KI hat keinen gültigen Zeitraum genannt. Bitte Beginn und Ende dazu schreiben"))
		return
	}
	if now := time.Now(); start.Before(now) {
		start = now.Add(time.Minute).Truncate(time.Minute)
		if !end.After(start) {
			fail(w, 400, errors.New("Der genannte Zeitraum ist schon vorbei"))
			return
		}
	}
	files := map[string]map[string]string{}
	for k, v := range p.Files {
		files[k] = v
	}
	changes, notes, err := a.Mgr.CheckChanges(p.Server, p.Vars, files)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if changes == nil {
		changes = []manager.SettingChange{}
	}
	if strings.TrimSpace(p.Name) == "" {
		p.Name = "Event"
	}
	resp["plan"] = p
	resp["changes"], resp["notes"], resp["server"] = changes, notes, a.Mgr.Info(sv)
	resp["event"] = map[string]any{"name": p.Name, "start": start, "end": end, "weekly": p.Weekly}
	writeJSON(w, 200, resp)
}
