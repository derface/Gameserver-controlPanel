package api

import (
	"net"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/proxy"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Public status page: which servers run, who plays, how to join. No login;
// only servers the admin ticked are shown, never secrets.

func (a *API) routesStatus(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/public/status", a.publicStatus)
	mux.HandleFunc("GET /api/status-page", a.need(auth.Admin, a.statusCfgGet))
	mux.HandleFunc("PUT /api/status-page", a.need(auth.Admin, a.statusCfgPut))
}

func (a *API) statusCfg() store.StatusConfig {
	var c store.StatusConfig
	a.Store.Read(func(d *store.Data) { c = d.Settings.Status })
	return c
}

type publicServer struct {
	ID        string   `json:"-"`
	Name      string   `json:"name"`
	Game      string   `json:"game"`
	State     string   `json:"state"` // online | startet | aus | fehler
	Players   int      `json:"players"`
	Max       int      `json:"max"`
	Names     []string `json:"names,omitempty"`
	Address   string   `json:"address,omitempty"`
	LANAddr   string   `json:"lan_address,omitempty"` // only for visitors from the home network
	SteamLink string   `json:"steam_link,omitempty"`
	Hint      string   `json:"hint,omitempty"`
	Note      string   `json:"note,omitempty"`
}

var statusCache struct {
	sync.Mutex
	at   [2]time.Time
	body [2]map[string]any // [0] internet, [1] home network
}

// fromLAN says whether the visitor sits in the home network. Behind a
// proxy the panel only believes it when the proxy is trusted.
func fromLAN(r *http.Request) bool {
	if r.Header.Get("X-Forwarded-For") != "" && !proxy.Trusted(r) {
		return false
	}
	ip := net.ParseIP(auth.ClientIP(r))
	return ip != nil && (ip.IsPrivate() || ip.IsLoopback())
}

func (a *API) publicStatus(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	c := a.statusCfg()
	if !c.Enabled {
		fail(w, 404, errNotFound)
		return
	}
	statusCache.Lock()
	defer statusCache.Unlock()
	k := 0
	if fromLAN(r) {
		k = 1
	}
	if time.Since(statusCache.at[k]) < 15*time.Second && statusCache.body[k] != nil {
		writeJSON(w, 200, statusCache.body[k])
		return
	}
	host := ""
	a.Store.Read(func(d *store.Data) { host = d.Settings.PublicHost })
	if host == "" {
		host = hostOnly(r.Host)
		if c.Host != "" && strings.EqualFold(host, c.Host) {
			host = ""
		}
	}
	list := a.publicServers(func(id string) (bool, string) {
		e, ok := c.Servers[id]
		return ok && e.Show, e.Note
	}, c.ShowNames, host)
	if lan := manager.LANIP(); k == 1 && lan != "" {
		for i := range list {
			if list[i].State == "online" {
				list[i].LANAddr, _, _ = a.Mgr.JoinInfo(list[i].ID, lan)
			}
		}
	}
	title := c.Title
	if title == "" {
		title = "Unsere Gameserver"
	}
	statusCache.body[k] = map[string]any{"title": title, "note": c.Note, "servers": list, "updated": time.Now()}
	statusCache.at[k] = time.Now()
	writeJSON(w, 200, statusCache.body[k])
}

// publicServers lists the servers pick() lets through, as players see them.
func (a *API) publicServers(pick func(id string) (bool, string), names bool, host string) []publicServer {
	list := []publicServer{}
	for _, s := range a.Mgr.List() {
		show, note := pick(s.ID)
		if !show {
			continue
		}
		p := publicServer{ID: s.ID, Name: s.Name, Game: s.TemplateName, Players: max(s.Players, 0), Max: s.MaxPlayers, Note: note}
		switch s.Status {
		case "running":
			p.State = "online"
		case "starting", "installing", "preparing":
			p.State = "startet"
		case "error":
			p.State = "fehler"
		default:
			p.State = "aus"
		}
		if p.State == "online" {
			if names {
				p.Names = a.Mgr.PublicPlayers(s.ID)
				if len(p.Names) > p.Players {
					p.Players = len(p.Names)
				}
			}
			if host != "" {
				p.Address, p.SteamLink, p.Hint = a.Mgr.JoinInfo(s.ID, host)
			}
		}
		list = append(list, p)
	}
	return list
}

func (a *API) statusCfgGet(w http.ResponseWriter, r *http.Request) {
	c := a.statusCfg()
	type row struct {
		ID   string `json:"id"`
		Name string `json:"name"`
		Game string `json:"game"`
		Show bool   `json:"show"`
		Note string `json:"note"`
	}
	rows := []row{}
	for _, s := range a.Mgr.List() {
		e := c.Servers[s.ID]
		rows = append(rows, row{s.ID, s.Name, s.TemplateName, e.Show, e.Note})
	}
	writeJSON(w, 200, map[string]any{"config": c, "servers": rows})
}

func (a *API) statusCfgPut(w http.ResponseWriter, r *http.Request) {
	var in store.StatusConfig
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	in.Host = strings.ToLower(strings.TrimSpace(in.Host))
	in.Title = strings.TrimSpace(in.Title)
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Title) > 80 || len(in.Note) > 500 {
		fail(w, 400, errTooLong)
		return
	}
	for id, e := range in.Servers {
		if _, ok := a.Store.Server(id); !ok {
			delete(in.Servers, id)
			continue
		}
		e.Note = strings.TrimSpace(e.Note)
		if len(e.Note) > 200 {
			e.Note = e.Note[:200]
		}
		in.Servers[id] = e
	}
	_ = a.Store.Update(func(d *store.Data) error { d.Settings.Status = in; return nil })
	statusCache.Lock()
	statusCache.body = [2]map[string]any{}
	statusCache.Unlock()
	a.statusCfgGet(w, r)
}

func hostOnly(h string) string {
	if x, _, err := net.SplitHostPort(h); err == nil {
		return x
	}
	return h
}

// statusHost serves only the status page under its own host name, so the
// panel's login is not reachable through it.
func (a *API) statusHost(next http.Handler) http.Handler {
	public := map[string]bool{"/status.html": true, "/status.js": true, "/status.css": true, "/api/public/status": true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := a.statusCfg()
		if c.Enabled && c.Host != "" && strings.EqualFold(hostOnly(r.Host), c.Host) {
			switch {
			case r.URL.Path == "/" || r.URL.Path == "/status":
				r.URL.Path = "/status.html"
			case public[r.URL.Path] || strings.HasPrefix(r.URL.Path, "/img/"):
			default:
				http.NotFound(w, r)
				return
			}
		} else if r.URL.Path == "/status" {
			if !c.Enabled {
				http.NotFound(w, r)
				return
			}
			r.URL.Path = "/status.html"
		}
		next.ServeHTTP(w, r)
	})
}
