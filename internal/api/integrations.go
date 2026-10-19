package api

import (
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// API keys for scripts and home automation, and webhooks the panel calls
// on events. Both are managed by admins only.

func (a *API) routesIntegrations(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/apikeys", a.need(auth.Admin, a.apiKeysGet))
	mux.HandleFunc("POST /api/apikeys", a.need(auth.Admin, a.apiKeyCreate))
	mux.HandleFunc("DELETE /api/apikeys/{kid}", a.need(auth.Admin, a.apiKeyDelete))
	mux.HandleFunc("GET /api/webhooks", a.need(auth.Admin, a.webhooksGet))
	mux.HandleFunc("POST /api/webhooks", a.need(auth.Admin, a.webhookCreate))
	mux.HandleFunc("PUT /api/webhooks/{wid}", a.need(auth.Admin, a.webhookPut))
	mux.HandleFunc("DELETE /api/webhooks/{wid}", a.need(auth.Admin, a.webhookDelete))
	mux.HandleFunc("POST /api/webhooks/{wid}/test", a.need(auth.Admin, a.webhookTest))
}

func (a *API) apiKeysGet(w http.ResponseWriter, r *http.Request) {
	list := []store.APIKey{}
	a.Store.Read(func(d *store.Data) {
		for _, k := range d.APIKeys {
			k.Hash = ""
			list = append(list, k)
		}
	})
	writeJSON(w, 200, map[string]any{"keys": list})
}

// cleanServers keeps the ids of servers that exist (here or on a node).
func (a *API) cleanServers(in []string) []string {
	var out []string
	seen := map[string]bool{}
	for _, id := range in {
		if id != "" && !seen[id] && a.fleetHas(id) {
			seen[id] = true
			out = append(out, id)
		}
	}
	return out
}

func (a *API) apiKeyCreate(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Name    string   `json:"name"`
		Role    string   `json:"role"`
		Servers []string `json:"servers"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	name := cleanText(in.Name, 40)
	if name == "" {
		fail(w, 400, errors.New("Bitte einen Namen angeben, z. B. „Home Assistant“"))
		return
	}
	servers := a.cleanServers(in.Servers)
	if len(in.Servers) > 0 && len(servers) == 0 {
		fail(w, 400, errors.New("Keiner der gewählten Server existiert"))
		return
	}
	key, k, err := a.Auth.NewAPIKey(name, in.Role, servers, a.Auth.User(r))
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, name)
	k.Hash = ""
	writeJSON(w, 201, map[string]any{"key": key, "item": k})
}

func (a *API) apiKeyDelete(w http.ResponseWriter, r *http.Request) {
	err := a.Store.Update(func(d *store.Data) error {
		for i, k := range d.APIKeys {
			if k.ID == r.PathValue("kid") {
				Note(r, k.Name)
				d.APIKeys = append(d.APIKeys[:i], d.APIKeys[i+1:]...)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		fail(w, 404, err)
		return
	}
	a.apiKeysGet(w, r)
}

func (a *API) webhooksGet(w http.ResponseWriter, r *http.Request) {
	list := []store.Webhook{}
	a.Store.Read(func(d *store.Data) { list = append(list, d.Webhooks...) })
	type ev struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	evs := []ev{}
	for _, e := range manager.WebhookEvents() {
		evs = append(evs, ev{e.Key, e.Label})
	}
	writeJSON(w, 200, map[string]any{"webhooks": list, "events": evs})
}

type webhookIn struct {
	Name      string   `json:"name"`
	URL       string   `json:"url"`
	Events    []string `json:"events"`
	Servers   []string `json:"servers"`
	Enabled   *bool    `json:"enabled"`
	NewSecret bool     `json:"new_secret"`
}

func (a *API) webhookCreate(w http.ResponseWriter, r *http.Request) {
	var in webhookIn
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	h := store.Webhook{ID: newToken()[:10], Name: in.Name, URL: in.URL, Events: in.Events, Servers: a.cleanServers(in.Servers),
		Enabled: in.Enabled == nil || *in.Enabled, Created: time.Now()}
	if err := manager.ValidateWebhook(&h); err != nil {
		fail(w, 400, err)
		return
	}
	err := a.Store.Update(func(d *store.Data) error {
		if len(d.Webhooks) >= 20 {
			return errors.New("Höchstens 20 Webhooks")
		}
		d.Webhooks = append(d.Webhooks, h)
		return nil
	})
	if err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, h.Name)
	a.webhooksGet(w, r)
}

func (a *API) webhookPut(w http.ResponseWriter, r *http.Request) {
	var in webhookIn
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	servers := a.cleanServers(in.Servers)
	err := a.Store.Update(func(d *store.Data) error {
		for i := range d.Webhooks {
			h := d.Webhooks[i]
			if h.ID != r.PathValue("wid") {
				continue
			}
			changedURL := strings.TrimSpace(in.URL) != h.URL
			h.Name, h.URL, h.Events, h.Servers = in.Name, in.URL, in.Events, servers
			if in.Enabled != nil {
				h.Enabled = *in.Enabled
			}
			if in.NewSecret {
				h.Secret = ""
			}
			if err := manager.ValidateWebhook(&h); err != nil {
				return err
			}
			if changedURL || in.Enabled != nil && *in.Enabled {
				h.Fails = 0
			}
			Note(r, h.Name)
			d.Webhooks[i] = h
			return nil
		}
		return errNotFound
	})
	if err != nil {
		fail(w, 400, err)
		return
	}
	a.webhooksGet(w, r)
}

func (a *API) webhookDelete(w http.ResponseWriter, r *http.Request) {
	err := a.Store.Update(func(d *store.Data) error {
		for i, h := range d.Webhooks {
			if h.ID == r.PathValue("wid") {
				Note(r, h.Name)
				d.Webhooks = append(d.Webhooks[:i], d.Webhooks[i+1:]...)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		fail(w, 404, err)
		return
	}
	a.webhooksGet(w, r)
}

func (a *API) webhookTest(w http.ResponseWriter, r *http.Request) {
	var h store.Webhook
	found := false
	a.Store.Read(func(d *store.Data) {
		for _, x := range d.Webhooks {
			if x.ID == r.PathValue("wid") {
				h, found = x, true
			}
		}
	})
	if !found {
		fail(w, 404, errNotFound)
		return
	}
	if a.Mgr == nil {
		fail(w, 503, errors.New("nicht bereit"))
		return
	}
	Note(r, h.Name)
	if err := a.Mgr.TestWebhook(h); err != nil {
		fail(w, 400, errors.New("Test fehlgeschlagen: "+err.Error()))
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
