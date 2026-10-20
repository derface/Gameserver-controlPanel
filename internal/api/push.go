package api

import (
	"errors"
	"net/http"
	"slices"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/push"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Push messages per device: every user switches them on for the browser or
// installed app they are using and picks the kinds of events.

func (a *API) routesPush(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/push", a.need(auth.Viewer, a.pushGet))
	mux.HandleFunc("POST /api/push", a.need(auth.Viewer, a.pushSubscribe))
	mux.HandleFunc("PATCH /api/push/{pid}", a.need(auth.Viewer, a.pushPatch))
	mux.HandleFunc("DELETE /api/push/{pid}", a.need(auth.Viewer, a.pushDelete))
	mux.HandleFunc("POST /api/push/{pid}/test", a.need(auth.Viewer, a.pushTest))
}

type pushSubView struct {
	ID      string    `json:"id"`
	Device  string    `json:"device"`
	Kinds   []string  `json:"kinds"`
	Created time.Time `json:"created"`
	LastOK  time.Time `json:"last_ok,omitempty"`
	LastErr string    `json:"last_err,omitempty"`
	// Endpoint lets the page recognise "this device"
	Endpoint string `json:"endpoint"`
}

func (a *API) pushGet(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	var keys store.PushKeys
	if a.Mgr != nil {
		var err error
		if keys, err = a.Mgr.PushKeys(); err != nil {
			fail(w, 500, err)
			return
		}
	}
	subs := []pushSubView{}
	a.Store.Read(func(d *store.Data) {
		for _, s := range d.PushSubs {
			if s.User == u.Name {
				subs = append(subs, pushSubView{ID: s.ID, Device: s.Device, Kinds: s.Kinds, Created: s.Created, LastOK: s.LastOK, LastErr: s.LastErr, Endpoint: s.Endpoint})
			}
		}
	})
	type kind struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	kinds := []kind{}
	for _, k := range manager.PushKinds() {
		kinds = append(kinds, kind{k.Key, k.Label})
	}
	writeJSON(w, 200, map[string]any{"public_key": keys.Public, "subs": subs, "kinds": kinds, "default_kinds": manager.DefaultPushKinds})
}

func cleanKinds(in []string) []string {
	var out []string
	for _, k := range manager.PushKinds() {
		if slices.Contains(in, k.Key) {
			out = append(out, k.Key)
		}
	}
	if out == nil {
		out = []string{}
	}
	return out
}

func (a *API) pushSubscribe(w http.ResponseWriter, r *http.Request) {
	u := userOf(r)
	if u.ViaKey || u.ViaNode {
		fail(w, 403, errors.New("Push gibt es nur für angemeldete Benutzer"))
		return
	}
	var in struct {
		Endpoint string   `json:"endpoint"`
		P256dh   string   `json:"p256dh"`
		Auth     string   `json:"auth"`
		Device   string   `json:"device"`
		Kinds    []string `json:"kinds"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	sub := push.Subscription{Endpoint: in.Endpoint, P256dh: in.P256dh, Auth: in.Auth}
	if err := sub.Check(); err != nil {
		fail(w, 400, err)
		return
	}
	kinds := manager.DefaultPushKinds
	if in.Kinds != nil {
		kinds = cleanKinds(in.Kinds)
	}
	dev := cleanText(in.Device, 40)
	if dev == "" {
		dev = "Gerät"
	}
	id := ""
	err := a.Store.Update(func(d *store.Data) error {
		for i, s := range d.PushSubs {
			if s.Endpoint == in.Endpoint { // same device again: refresh keys, keep choices
				d.PushSubs[i].User, d.PushSubs[i].P256dh, d.PushSubs[i].Auth, d.PushSubs[i].Device = u.Name, in.P256dh, in.Auth, dev
				id = s.ID
				return nil
			}
		}
		n := 0
		for _, s := range d.PushSubs {
			if s.User == u.Name {
				n++
			}
		}
		if n >= 10 {
			return errors.New("Höchstens 10 Geräte pro Benutzer")
		}
		id = newToken()[:10]
		d.PushSubs = append(d.PushSubs, store.PushSub{ID: id, User: u.Name, Device: dev, Endpoint: in.Endpoint, P256dh: in.P256dh, Auth: in.Auth, Kinds: kinds, Created: time.Now()})
		return nil
	})
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, dev)
	a.pushGet(w, r)
}

// ownSub runs f on a device of the current user (admins: any device).
func (a *API) ownSub(r *http.Request, f func(d *store.Data, i int) error) error {
	u := userOf(r)
	return a.Store.Update(func(d *store.Data) error {
		for i, s := range d.PushSubs {
			if s.ID == r.PathValue("pid") && (s.User == u.Name || u.Role == auth.Admin) {
				return f(d, i)
			}
		}
		return errNotFound
	})
}

func (a *API) pushPatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Kinds []string `json:"kinds"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if err := a.ownSub(r, func(d *store.Data, i int) error { d.PushSubs[i].Kinds = cleanKinds(in.Kinds); return nil }); err != nil {
		fail(w, 404, err)
		return
	}
	a.pushGet(w, r)
}

func (a *API) pushDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.ownSub(r, func(d *store.Data, i int) error {
		Note(r, d.PushSubs[i].Device)
		d.PushSubs = append(d.PushSubs[:i], d.PushSubs[i+1:]...)
		return nil
	}); err != nil {
		fail(w, 404, err)
		return
	}
	a.pushGet(w, r)
}

func (a *API) pushTest(w http.ResponseWriter, r *http.Request) {
	var sub store.PushSub
	if err := a.ownSub(r, func(d *store.Data, i int) error { sub = d.PushSubs[i]; return nil }); err != nil {
		fail(w, 404, err)
		return
	}
	noAudit(r)
	if err := a.Mgr.TestPush(sub); err != nil {
		fail(w, 400, errors.New("Test fehlgeschlagen: "+err.Error()))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
