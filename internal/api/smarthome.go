package api

import (
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strings"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Smart home settings: Hue bridge, Home Assistant and the rules.

func (a *API) routesSmart(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/smarthome", a.need(auth.Admin, a.smartGet))
	mux.HandleFunc("POST /api/smarthome/hue/discover", a.need(auth.Admin, a.smartHueDiscover))
	mux.HandleFunc("POST /api/smarthome/hue/pair", a.need(auth.Admin, a.smartHuePair))
	mux.HandleFunc("DELETE /api/smarthome/hue", a.need(auth.Admin, a.smartHueDelete))
	mux.HandleFunc("PUT /api/smarthome/ha", a.need(auth.Admin, a.smartHAPut))
	mux.HandleFunc("DELETE /api/smarthome/ha", a.need(auth.Admin, a.smartHADelete))
	mux.HandleFunc("GET /api/smarthome/devices", a.need(auth.Admin, a.smartDevices))
	mux.HandleFunc("PUT /api/smarthome/rules", a.need(auth.Admin, a.smartRulesPut))
	mux.HandleFunc("POST /api/smarthome/rules/{rid}/test", a.need(auth.Admin, a.smartRuleTest))
}

func (a *API) smartCfg() store.SmartHome {
	var c store.SmartHome
	a.Store.Read(func(d *store.Data) {
		c = d.Smart
		c.Rules = append([]store.SmartRule(nil), d.Smart.Rules...)
	})
	return c
}

func (a *API) smartGet(w http.ResponseWriter, r *http.Request) {
	c := a.smartCfg()
	if c.Rules == nil {
		c.Rules = []store.SmartRule{}
	}
	type opt struct {
		Key   string `json:"key"`
		Label string `json:"label"`
	}
	trig := []opt{}
	for _, t := range manager.SmartTriggers() {
		trig = append(trig, opt{t.Key, t.Label})
	}
	writeJSON(w, 200, map[string]any{
		"hue":      map[string]any{"bridge": c.HueBridge, "paired": c.HueUser != ""},
		"ha":       map[string]any{"url": c.HAURL, "token_set": c.HAToken != ""},
		"rules":    c.Rules,
		"triggers": trig,
	})
}

// cleanHost accepts an IP or host name, optionally with port.
func cleanHost(s string) (string, error) {
	s = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(strings.TrimSpace(s), "http://"), "https://"), "/")
	u, err := url.Parse("http://" + s)
	if s == "" || err != nil || u.Host != s || u.User != nil {
		return "", errors.New("Bitte die IP-Adresse der Hue-Bridge angeben, z. B. 192.168.1.40")
	}
	return s, nil
}

func (a *API) smartHueDiscover(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	ips, err := manager.HueDiscover()
	if err != nil {
		fail(w, 400, errors.New("Suche fehlgeschlagen, bitte die IP von Hand eintragen (steht in der Hue-App unter Einstellungen → Bridges)"))
		return
	}
	if ips == nil {
		ips = []string{}
	}
	writeJSON(w, 200, map[string]any{"bridges": ips})
}

func (a *API) smartHuePair(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Bridge string `json:"bridge"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	host, err := cleanHost(in.Bridge)
	if err != nil {
		fail(w, 400, err)
		return
	}
	user, err := manager.HuePair(host)
	if err != nil {
		fail(w, 400, err)
		return
	}
	_ = a.Store.Update(func(d *store.Data) error {
		d.Smart.HueBridge, d.Smart.HueUser = host, user
		return nil
	})
	Note(r, host)
	a.smartGet(w, r)
}

func (a *API) smartHueDelete(w http.ResponseWriter, r *http.Request) {
	_ = a.Store.Update(func(d *store.Data) error {
		d.Smart.HueBridge, d.Smart.HueUser = "", ""
		return nil
	})
	a.smartGet(w, r)
}

func (a *API) smartHAPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		URL   string `json:"url"`
		Token string `json:"token"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	u, err := url.Parse(strings.TrimSuffix(strings.TrimSpace(in.URL), "/"))
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || (u.Path != "" && u.Path != "/") {
		fail(w, 400, errors.New("Adresse bitte wie http://homeassistant.local:8123 angeben"))
		return
	}
	cfg := a.smartCfg()
	cfg.HAURL = u.Scheme + "://" + u.Host
	if t := strings.TrimSpace(in.Token); t != "" {
		cfg.HAToken = t
	}
	if err := manager.HACheck(cfg); err != nil {
		fail(w, 400, err)
		return
	}
	_ = a.Store.Update(func(d *store.Data) error {
		d.Smart.HAURL, d.Smart.HAToken = cfg.HAURL, cfg.HAToken
		return nil
	})
	Note(r, cfg.HAURL)
	a.smartGet(w, r)
}

func (a *API) smartHADelete(w http.ResponseWriter, r *http.Request) {
	_ = a.Store.Update(func(d *store.Data) error {
		d.Smart.HAURL, d.Smart.HAToken = "", ""
		return nil
	})
	a.smartGet(w, r)
}

func (a *API) smartDevices(w http.ResponseWriter, r *http.Request) {
	cfg := a.smartCfg()
	out := map[string]any{"hue": []manager.SmartLight{}, "ha": []manager.SmartLight{}}
	if cfg.HueUser != "" {
		if l, err := manager.HueLights(cfg); err != nil {
			out["hue_error"] = err.Error()
		} else {
			out["hue"] = l
		}
	}
	if cfg.HAToken != "" {
		if l, err := manager.HAEntities(cfg); err != nil {
			out["ha_error"] = err.Error()
		} else {
			out["ha"] = l
		}
	}
	writeJSON(w, 200, out)
}

func (a *API) smartRulesPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rules []store.SmartRule `json:"rules"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if len(in.Rules) > 50 {
		fail(w, 400, errors.New("Höchstens 50 Regeln"))
		return
	}
	for i := range in.Rules {
		in.Rules[i].Servers = a.cleanServers(in.Rules[i].Servers)
		if err := manager.ValidateSmartRule(&in.Rules[i]); err != nil {
			fail(w, 400, fmt.Errorf("Regel %d: %w", i+1, err))
			return
		}
	}
	_ = a.Store.Update(func(d *store.Data) error {
		old := map[string]store.SmartRule{}
		for _, x := range d.Smart.Rules {
			old[x.ID] = x
		}
		for i := range in.Rules {
			if o, ok := old[in.Rules[i].ID]; ok && in.Rules[i].ID != "" {
				in.Rules[i].LastAt, in.Rules[i].LastErr = o.LastAt, o.LastErr
			} else {
				in.Rules[i].ID = newToken()[:10]
			}
		}
		d.Smart.Rules = in.Rules
		return nil
	})
	a.smartGet(w, r)
}

func (a *API) smartRuleTest(w http.ResponseWriter, r *http.Request) {
	var rule store.SmartRule
	found := false
	for _, x := range a.smartCfg().Rules {
		if x.ID == r.PathValue("rid") {
			rule, found = x, true
		}
	}
	if !found {
		fail(w, 404, errNotFound)
		return
	}
	noAudit(r)
	if err := a.Mgr.RunSmartRule(rule); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
