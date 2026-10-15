package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/nodes"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// The public side (status page, calendar, Discord bot) across machines: the
// servers of other machines are asked for through /api/node/… with the
// node key, the rest of the work stays here.

func (a *API) routesFleet(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/node/public", a.need(auth.Admin, a.nodePublic))
	mux.HandleFunc("GET /api/node/mods/{id}", a.need(auth.Admin, a.nodeMods))
	mux.HandleFunc("GET /api/node/modpack/{file}", a.need(auth.Admin, a.nodeModpack))
}

// nodePublicServer carries the id, which publicServer keeps to itself.
type nodePublicServer struct {
	ID string `json:"id"`
	publicServer
}

type nodePublicAnswer struct {
	Servers []nodePublicServer    `json:"servers"`
	Events  []publicEvent         `json:"events"`
	Players []manager.PlayerTotal `json:"players"`
}

// nodePublic is asked by the panel this machine belongs to.
func (a *API) nodePublic(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	var o pubOpts
	if err := decode(r, &o); err != nil {
		fail(w, 400, err)
		return
	}
	host := ""
	a.Store.Read(func(d *store.Data) { host = d.Settings.PublicHost })
	if host != "" { // this machine's own address for players wins
		o.Host = host
	}
	v := a.localPublic(o)
	out := nodePublicAnswer{Events: v.Events, Players: v.Players}
	for _, s := range v.Servers {
		out.Servers = append(out.Servers, nodePublicServer{ID: s.ID, publicServer: s})
	}
	writeJSON(w, 200, out)
}

// addRemotePublic adds the servers of other machines that o names.
func (a *API) addRemotePublic(v *pubView, o pubOpts) {
	if a.Nodes == nil {
		return
	}
	byNode := map[string]map[string]string{}
	for id, note := range o.Notes {
		if nid, rid, ok := nodes.Split(id); ok {
			if byNode[nid] == nil {
				byNode[nid] = map[string]string{}
			}
			byNode[nid][rid] = note
		}
	}
	if len(byNode) == 0 {
		return
	}
	type result struct {
		nid string
		ans nodePublicAnswer
	}
	var mu sync.Mutex
	var res []result
	var wg sync.WaitGroup
	for _, n := range a.Nodes.Online() {
		notes := byNode[n.ID]
		if notes == nil {
			continue
		}
		wg.Add(1)
		go func(n store.Node, notes map[string]string) {
			defer wg.Done()
			q := o
			q.Notes = notes
			body, _ := json.Marshal(q)
			ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
			defer cancel()
			code, b, err := a.Nodes.Do(ctx, n, "POST", "/api/node/public", body, "")
			var ans nodePublicAnswer
			if err != nil || code != 200 || json.Unmarshal(b, &ans) != nil {
				return
			}
			mu.Lock()
			res = append(res, result{n.ID, ans})
			mu.Unlock()
		}(n, notes)
	}
	wg.Wait()
	sort.Slice(res, func(i, j int) bool { return res[i].nid < res[j].nid })
	answered := map[string]bool{}
	for _, r := range res {
		answered[r.nid] = true
		for _, s := range r.ans.Servers {
			p := s.publicServer
			p.ID = r.nid + nodes.Sep + s.ID
			v.Servers = append(v.Servers, p)
		}
		for _, e := range r.ans.Events {
			e.ID = r.nid + "-" + e.ID
			v.Events = append(v.Events, e)
		}
		v.Players = mergePlayers(v.Players, r.ans.Players)
	}
	// servers of machines that do not answer: shown as unknown, not hidden
	for id, note := range o.Notes {
		nid, _, ok := nodes.Split(id)
		if !ok || answered[nid] {
			continue
		}
		if s, ok := a.Nodes.Server(id); ok {
			name, _ := s["name"].(string)
			game, _ := s["template_name"].(string)
			v.Servers = append(v.Servers, publicServer{ID: id, Name: name, Game: game, State: "fehler", Note: note})
		}
	}
	sort.SliceStable(v.Events, func(i, j int) bool { return v.Events[i].Start.Before(v.Events[j].Start) })
}

// mergePlayers adds up the play times of players seen on several machines.
func mergePlayers(a, b []manager.PlayerTotal) []manager.PlayerTotal {
	if len(b) == 0 {
		return a
	}
	idx := map[string]int{}
	for i, p := range a {
		idx[strings.ToLower(p.Name)] = i
	}
	for _, p := range b {
		i, ok := idx[strings.ToLower(p.Name)]
		if !ok {
			idx[strings.ToLower(p.Name)] = len(a)
			a = append(a, p)
			continue
		}
		q := &a[i]
		if p.Seconds > q.Seconds {
			q.Fav = p.Fav
		}
		q.Seconds += p.Seconds
		q.Sessions += p.Sessions
		q.Servers += p.Servers
		q.Online = q.Online || p.Online
		if !p.First.IsZero() && (q.First.IsZero() || p.First.Before(q.First)) {
			q.First = p.First
		}
		if p.Last.After(q.Last) {
			q.Last = p.Last
		}
	}
	sort.SliceStable(a, func(i, j int) bool { return a[i].Seconds > a[j].Seconds })
	if len(a) > 50 {
		a = a[:50]
	}
	return a
}

// nodeMods gives the client mods of one server to the controlling panel,
// which decides itself whether its status page shows them.
func (a *API) nodeMods(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	cm, err := a.Mgr.ClientMods(ctx, r.PathValue("id"))
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"mods": cm.Mods, "note": cm.Note, "download": cm.Download})
}

func (a *API) nodeModpack(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	id := strings.TrimSuffix(r.PathValue("file"), ".zip")
	sv, ok := a.Store.Server(id)
	if !ok {
		http.NotFound(w, r)
		return
	}
	w.Header().Set("Content-Type", "application/zip")
	w.Header().Set("Content-Disposition", `attachment; filename="`+manager.SafeFileName(sv.Name)+`-mods.zip"`)
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Minute)
	defer cancel()
	_ = a.Mgr.WriteModPack(ctx, sv.ID, w)
}

// remoteShown says whether the status page shows this server of another
// machine; it returns the node and the id there.
func (a *API) remoteShown(key string) (store.Node, string, bool) {
	id, ok := strings.CutPrefix(key, "s-")
	if !ok || a.Nodes == nil {
		return store.Node{}, "", false
	}
	nid, rid, ok := nodes.Split(id)
	if !ok {
		return store.Node{}, "", false
	}
	c := a.statusCfg()
	if e, shown := c.Servers[id]; !c.Enabled || !shown || !e.Show {
		return store.Node{}, "", false
	}
	n, ok := a.Nodes.Get(nid)
	return n, rid, ok
}

// remoteMods answers /api/public/mods/s-<node>~<id>.
func (a *API) remoteMods(w http.ResponseWriter, r *http.Request, key string) bool {
	n, rid, ok := a.remoteShown(key)
	if !ok {
		return false
	}
	ctx, cancel := context.WithTimeout(r.Context(), time.Minute)
	defer cancel()
	code, b, err := a.Nodes.Do(ctx, n, "GET", "/api/node/mods/"+rid, nil, "")
	var v struct {
		Mods     any    `json:"mods"`
		Note     string `json:"note"`
		Download bool   `json:"download"`
	}
	if err != nil || code != 200 || json.Unmarshal(b, &v) != nil {
		fail(w, 502, errors.New("Der Rechner mit diesem Server antwortet gerade nicht"))
		return true
	}
	dl := ""
	if v.Download && a.statusCfg().ModDownload {
		dl = "/api/public/modpack/" + key + ".zip"
	}
	writeJSON(w, 200, map[string]any{"mods": v.Mods, "note": v.Note, "download": dl})
	return true
}

// remoteModpack streams the mod pack of a server on another machine.
func (a *API) remoteModpack(w http.ResponseWriter, r *http.Request, key string) bool {
	n, rid, ok := a.remoteShown(key)
	if !ok || !a.statusCfg().ModDownload {
		return false
	}
	select {
	case modpackSlots <- struct{}{}:
		defer func() { <-modpackSlots }()
	default:
		fail(w, 429, errors.New("Gerade laden schon zwei Leute das Mod-Paket. Bitte gleich nochmal versuchen."))
		return true
	}
	a.Nodes.Proxy(w, r, n, "/api/node/modpack/"+rid+".zip", "", "")
	return true
}

// ---- servers here and there, for the Discord bot ----

// fleetInfos lists this machine's servers and those of the other machines.
func (a *API) fleetInfos() []manager.Info {
	list := a.Mgr.List()
	if a.Nodes == nil {
		return list
	}
	for _, s := range a.Nodes.Servers() {
		b, _ := json.Marshal(s)
		var in manager.Info
		if json.Unmarshal(b, &in) == nil && in.ID != "" {
			if s["node_offline"] == true {
				in.Status = "error"
			}
			list = append(list, in)
		}
	}
	return list
}

// fleetHas says whether a server exists here or on another machine.
func (a *API) fleetHas(id string) bool {
	if _, ok := a.Store.Server(id); ok {
		return true
	}
	if a.Nodes != nil && strings.Contains(id, nodes.Sep) {
		_, ok := a.Nodes.Server(id)
		return ok
	}
	return false
}

func (a *API) fleetIsUp(id string) bool {
	if strings.Contains(id, nodes.Sep) && a.Nodes != nil {
		return a.Nodes.IsUp(id)
	}
	return a.Mgr.IsUp(id)
}

func (a *API) fleetCanWhitelist(id string) bool {
	if strings.Contains(id, nodes.Sep) && a.Nodes != nil {
		return a.Nodes.CanWhitelist(id)
	}
	return a.Mgr.CanWhitelist(id)
}

// remoteCall sends one action to a server on another machine.
func (a *API) remoteCall(ctx context.Context, id, method, rest string, body any, user string) error {
	nid, rid, ok := nodes.Split(id)
	if !ok || a.Nodes == nil {
		return errors.New("Server nicht gefunden")
	}
	n, ok := a.Nodes.Get(nid)
	if !ok {
		return errors.New("Rechner nicht gefunden")
	}
	var raw []byte
	if body != nil {
		raw, _ = json.Marshal(body)
	}
	code, b, err := a.Nodes.Do(ctx, n, method, "/api/servers/"+rid+rest, raw, user)
	if err != nil {
		return err
	}
	if code >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return errors.New(http.StatusText(code))
	}
	a.Nodes.Refresh()
	return nil
}

func (a *API) fleetPlayerAction(ctx context.Context, id, action, name, user string) error {
	if strings.Contains(id, nodes.Sep) {
		return a.remoteCall(ctx, id, "POST", "/players", map[string]string{"action": action, "name": name}, user)
	}
	_, err := a.Mgr.PlayerAction(ctx, id, action, name, "")
	return err
}

// fleetPower starts, stops or restarts a server here or on another machine.
// who appears in the server's history.
func (a *API) fleetPower(ctx context.Context, id, action, who string) error {
	if strings.Contains(id, nodes.Sep) {
		return a.remoteCall(ctx, id, "POST", "/"+action, nil, who)
	}
	switch action {
	case "start":
		return a.Mgr.Start(ctx, id)
	case "stop":
		return a.Mgr.Stop(ctx, id)
	case "restart":
		return a.Mgr.Restart(ctx, id)
	}
	return errors.New("Unbekannte Aktion")
}

// fleetName is a server's name with its machine, for lists in the settings.
func (a *API) fleetName(s manager.Info) string {
	if nid, _, ok := nodes.Split(s.ID); ok && a.Nodes != nil {
		if n, ok := a.Nodes.Get(nid); ok {
			return s.Name + " (" + n.Name + ")"
		}
	}
	return s.Name
}
