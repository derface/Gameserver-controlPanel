package api

import (
	"fmt"
	"math"
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

type publicSeason struct {
	N     int        `json:"n"`
	Name  string     `json:"name"`
	Since time.Time  `json:"since"`
	Next  *time.Time `json:"next,omitempty"` // planned new season
	Fame  []fame     `json:"fame,omitempty"` // past seasons, newest first
}

type fame struct {
	N     int       `json:"n"`
	Name  string    `json:"name"`
	Start time.Time `json:"start"`
	End   time.Time `json:"end"`
	Hours float64   `json:"hours"`
	Top   []string  `json:"top"` // "Name (12 Std.)"
}

func seasonOf(sv store.Server) *publicSeason {
	ss := sv.Seasons
	if ss == nil || (len(ss.Past) == 0 && ss.Plan == nil) {
		return nil
	}
	p := &publicSeason{N: ss.Current.N, Name: ss.Current.Name, Since: ss.Current.Start}
	if ss.Plan != nil {
		t := ss.Plan.At
		p.Next = &t
	}
	for i, s := range ss.Past {
		if i == 5 {
			break
		}
		f := fame{N: s.N, Name: s.Name, Start: s.Start, End: s.End, Hours: math.Round(s.Hours*10) / 10}
		for j, t := range s.Top {
			if j == 3 {
				break
			}
			f.Top = append(f.Top, fmt.Sprintf("%s (%d Std.)", t.Name, (t.Seconds+1800)/3600))
		}
		p.Fame = append(p.Fame, f)
	}
	return p
}

type publicServer struct {
	ID        string   `json:"-"`
	Name      string   `json:"name"`
	Game      string   `json:"game"`
	State     string   `json:"state"`           // online | startet | aus | fehler | wartung
	Until     string   `json:"until,omitempty"` // maintenance end, RFC3339
	Players   int      `json:"players"`
	Max       int      `json:"max"`
	Names     []string `json:"names,omitempty"`
	Address   string   `json:"address,omitempty"`
	LANAddr   string   `json:"lan_address,omitempty"` // only for visitors from the home network
	SteamLink string   `json:"steam_link,omitempty"`
	Hint      string   `json:"hint,omitempty"`
	Note      string   `json:"note,omitempty"`
	// last 30 days: [share online 0..100, crashes] per day, oldest first
	Uptime    [][2]int      `json:"uptime,omitempty"`
	UptimePct *float64      `json:"uptime_pct,omitempty"`
	Key       string        `json:"key,omitempty"`  // for /api/public/mods
	Mods      int           `json:"mods,omitempty"` // client mods (from the cache)
	Season    *publicSeason `json:"season,omitempty"`
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
	o := pubOpts{Notes: map[string]string{}, Names: c.ShowNames, Host: host, LAN: k == 1, Uptime: !c.HideUptime,
		Players: c.ShowPlayers, Horizon: 14 * 24 * time.Hour}
	for id, e := range c.Servers {
		if e.Show {
			o.Notes[id] = e.Note
		}
	}
	v := a.localPublic(o)
	a.addRemotePublic(&v, o)
	for i := range v.Servers {
		v.Servers[i].Key = "s-" + v.Servers[i].ID
	}
	title := c.Title
	if title == "" {
		title = "Unsere Gameserver"
	}
	cal := ""
	if c.CalToken != "" {
		cal = "/api/public/calendar/" + c.CalToken + ".ics"
	}
	body := map[string]any{"title": title, "note": c.Note, "servers": v.Servers, "updated": time.Now(),
		"events": v.Events, "calendar": cal, "wishes": c.Wishes}
	if c.ShowPlayers {
		body["players"] = v.Players
	}
	statusCache.body[k] = body
	statusCache.at[k] = time.Now()
	writeJSON(w, 200, statusCache.body[k])
}

// pubOpts says what a public view (status page, Discord, calendar) shows.
// The same options go to other machines for their servers.
type pubOpts struct {
	Notes   map[string]string `json:"notes"` // server id → note; only these servers
	Names   bool              `json:"names"`
	Host    string            `json:"host"`
	LAN     bool              `json:"lan"` // visitor from the home network
	Uptime  bool              `json:"uptime"`
	Players bool              `json:"players"`
	Horizon time.Duration     `json:"horizon"` // events up to this far ahead; 0 = none
	Mods    bool              `json:"mods"`
}

type pubView struct {
	Servers []publicServer        `json:"-"`
	Events  []publicEvent         `json:"events"`
	Players []manager.PlayerTotal `json:"players"`
}

// localPublic builds the public view of this machine's servers.
func (a *API) localPublic(o pubOpts) pubView {
	v := pubView{Events: []publicEvent{}}
	v.Servers = a.publicServers(func(id string) (bool, string) {
		note, ok := o.Notes[id]
		return ok, note
	}, o.Names, o.Host)
	for i := range v.Servers {
		if sv, ok := a.Store.Server(v.Servers[i].ID); ok {
			v.Servers[i].Season = seasonOf(sv)
		}
		if cm, ok := a.Mgr.ClientModsCached(v.Servers[i].ID); ok {
			v.Servers[i].Mods = len(cm.Mods)
		}
	}
	if o.Uptime {
		for i := range v.Servers {
			days, pct := a.Mgr.UptimeCached(v.Servers[i].ID)
			known := false
			for _, d := range days {
				up := int(d.Up*100 + 0.5)
				if d.Known == 0 {
					up = -1 // no data that day
				}
				v.Servers[i].Uptime = append(v.Servers[i].Uptime, [2]int{up, d.Crashes})
				known = known || d.Known > 0
			}
			if known {
				p := math.Round(pct*10) / 10
				v.Servers[i].UptimePct = &p
			} else {
				v.Servers[i].Uptime = nil
			}
		}
	}
	if lan := manager.LANIP(); o.LAN && lan != "" {
		for i := range v.Servers {
			if v.Servers[i].State == "online" {
				v.Servers[i].LANAddr, _, _ = a.Mgr.JoinInfo(v.Servers[i].ID, lan)
			}
		}
	}
	var shown []store.Server
	for _, s := range v.Servers {
		if sv, ok := a.Store.Server(s.ID); ok {
			shown = append(shown, sv)
		}
	}
	if o.Horizon > 0 {
		v.Events = a.publicEvents(shown, o.Horizon)
	}
	if o.Players {
		names := map[string]string{}
		for _, sv := range shown {
			names[sv.ID] = sv.Name
		}
		v.Players = a.Mgr.PlayerTotals(names, 50)
	}
	return v
}

// publicServers lists the servers pick() lets through, as players see them.
func (a *API) publicServers(pick func(id string) (bool, string), names bool, host string) []publicServer {
	list := []publicServer{}
	for _, s := range a.Mgr.List() {
		show, note := pick(s.ID)
		if !show {
			continue
		}
		p := publicServer{ID: s.ID, Name: s.Name, Game: s.TemplateName, Players: max(s.Players, 0), Max: s.MaxPlayers, Note: note, State: publicState(s)}
		if sv, ok := a.Store.Server(s.ID); ok && sv.InMaintenance(time.Now()) {
			p.State = "wartung"
			if !sv.Maintenance.Until.IsZero() {
				p.Until = sv.Maintenance.Until.Format(time.RFC3339)
			}
			if sv.Maintenance.Note != "" {
				p.Note = sv.Maintenance.Note
			}
			list = append(list, p)
			continue
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
	for _, s := range a.fleetInfos() {
		e := c.Servers[s.ID]
		rows = append(rows, row{s.ID, a.fleetName(s), s.TemplateName, e.Show, e.Note})
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
		if !a.fleetHas(id) {
			delete(in.Servers, id)
			continue
		}
		e.Note = strings.TrimSpace(e.Note)
		if len(e.Note) > 200 {
			e.Note = e.Note[:200]
		}
		in.Servers[id] = e
	}
	_ = a.Store.Update(func(d *store.Data) error {
		in.CalToken = d.Settings.Status.CalToken // changed only through its own call
		d.Settings.Status = in
		return nil
	})
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
	public := map[string]bool{"/status.html": true, "/status.js": true, "/status.css": true, "/api/public/status": true, "/invite.html": true, "/invite.js": true, "/i18n.js": true, "/i18n/en.json": true}
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		c := a.statusCfg()
		if c.Enabled && c.Host != "" && strings.EqualFold(hostOnly(r.Host), c.Host) {
			switch {
			case r.URL.Path == "/" || r.URL.Path == "/status":
				r.URL.Path = "/status.html"
			case strings.HasPrefix(r.URL.Path, "/invite/"):
				r.URL.Path = "/invite.html"
			case public[r.URL.Path] || strings.HasPrefix(r.URL.Path, "/img/") || strings.HasPrefix(r.URL.Path, "/api/public/"):
			default:
				http.NotFound(w, r)
				return
			}
		} else if strings.HasPrefix(r.URL.Path, "/invite/") {
			r.URL.Path = "/invite.html"
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

// publicState is the state players see.
func publicState(s manager.Info) string {
	if s.InMaintenance(time.Now()) {
		return "wartung"
	}
	switch s.Status {
	case "running":
		return "online"
	case "starting", "installing", "preparing":
		return "startet"
	case "error":
		return "fehler"
	}
	return "aus"
}
