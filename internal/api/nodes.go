package api

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"os"
	"path"
	"regexp"
	"strings"
	"time"
	"unicode/utf8"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/nodes"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Mehrere Rechner: this panel can control the servers of other panels
// ("Rechner"), and it can hand out keys so another panel controls it.

func (a *API) nodeRoutes(mux *http.ServeMux) {
	// this machine as a node
	mux.HandleFunc("GET /api/node/hello", a.need(auth.Admin, a.nodeHello))
	mux.HandleFunc("GET /api/node/keys", a.need(auth.Admin, a.nodeKeys))
	mux.HandleFunc("POST /api/node/keys", a.need(auth.Admin, a.nodeKeyNew))
	mux.HandleFunc("DELETE /api/node/keys/{kid}", a.need(auth.Admin, a.nodeKeyDelete))
	// other machines
	mux.HandleFunc("GET /api/nodes", a.need(auth.Viewer, a.nodeList))
	mux.HandleFunc("POST /api/nodes", a.need(auth.Admin, a.nodeAdd))
	mux.HandleFunc("PATCH /api/nodes/{nid}", a.need(auth.Admin, a.nodeRename))
	mux.HandleFunc("DELETE /api/nodes/{nid}", a.need(auth.Admin, a.nodeDelete))
}

func (a *API) nodeHello(w http.ResponseWriter, r *http.Request) {
	host, _ := os.Hostname()
	h := nodes.Hello{Version: a.Version, Hostname: host}
	for _, s := range a.Mgr.List() {
		h.Servers++
		if a.Mgr.CanWhitelist(s.ID) {
			h.Whitelist = append(h.Whitelist, s.ID)
		}
	}
	writeJSON(w, 200, h)
}

func (a *API) nodeKeys(w http.ResponseWriter, r *http.Request) {
	out := []store.NodeKey{}
	a.Store.Read(func(d *store.Data) {
		for _, k := range d.NodeKeys {
			k.Hash = ""
			out = append(out, k)
		}
	})
	writeJSON(w, 200, out)
}

func (a *API) nodeKeyNew(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Label string `json:"label"`
		URL   string `json:"url"` // how the other panel reaches this one
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	req.Label = strings.TrimSpace(req.Label)
	if req.Label == "" {
		req.Label = "Hauptpanel"
	}
	if utf8.RuneCountInString(req.Label) > 40 {
		fail(w, 400, errors.New("Name zu lang"))
		return
	}
	u, err := nodes.CleanURL(req.URL)
	if err != nil {
		fail(w, 400, err)
		return
	}
	key, err := a.Auth.NewNodeKey(req.Label)
	if err != nil {
		fail(w, 500, err)
		return
	}
	writeJSON(w, 201, map[string]string{"code": nodes.EncodeCode(nodes.Code{URL: u, Token: key})})
}

func (a *API) nodeKeyDelete(w http.ResponseWriter, r *http.Request) {
	kid := r.PathValue("kid")
	err := a.Store.Update(func(d *store.Data) error {
		for i, k := range d.NodeKeys {
			if k.ID == kid {
				d.NodeKeys = append(d.NodeKeys[:i], d.NodeKeys[i+1:]...)
				return nil
			}
		}
		return errors.New("Schlüssel nicht gefunden")
	})
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) nodeList(w http.ResponseWriter, r *http.Request) {
	list := a.Nodes.Status()
	if userOf(r).Role != auth.Admin { // others only see names and whether they answer
		for i := range list {
			list[i] = nodes.Status{ID: list[i].ID, Name: list[i].Name, Online: list[i].Online}
		}
	}
	writeJSON(w, 200, list)
}

func (a *API) nodeAdd(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Code string `json:"code"`
		Name string `json:"name"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	c, err := nodes.DecodeCode(req.Code)
	if err != nil {
		fail(w, 400, err)
		return
	}
	u, err := nodes.CleanURL(c.URL)
	if err != nil {
		fail(w, 400, err)
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	fp, hello, err := nodes.Probe(ctx, u, c.Token)
	if err != nil {
		fail(w, 400, errors.New("Verbindung zu "+u+" klappt nicht: "+err.Error()))
		return
	}
	if hello.Version != a.Version {
		Note(r, "andere Version: "+hello.Version)
	}
	name := strings.TrimSpace(req.Name)
	if name == "" {
		name = hello.Hostname
	}
	if name == "" {
		name = u
	}
	if utf8.RuneCountInString(name) > 40 {
		fail(w, 400, errors.New("Name zu lang"))
		return
	}
	n := store.Node{ID: nodes.NewID(), Name: name, URL: u, Token: c.Token, FP: fp, Added: time.Now()}
	err = a.Store.Update(func(d *store.Data) error {
		for _, o := range d.Nodes {
			if o.URL == u {
				return errors.New("Dieser Rechner ist schon eingetragen")
			}
		}
		d.Nodes = append(d.Nodes, n)
		return nil
	})
	if err != nil {
		fail(w, 400, err)
		return
	}
	a.Nodes.Refresh()
	writeJSON(w, 201, map[string]any{"id": n.ID, "name": n.Name, "fp": fp, "version": hello.Version, "servers": hello.Servers})
}

func (a *API) nodeRename(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Name  string `json:"name"`
		Repin bool   `json:"repin"` // the node has a new certificate: take it
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if req.Repin {
		n, ok := a.Nodes.Get(r.PathValue("nid"))
		if !ok {
			fail(w, 404, errors.New("Rechner nicht gefunden"))
			return
		}
		ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
		defer cancel()
		fp, _, err := nodes.Probe(ctx, n.URL, n.Token)
		if err != nil {
			fail(w, 400, err)
			return
		}
		_ = a.Store.Update(func(d *store.Data) error {
			for i := range d.Nodes {
				if d.Nodes[i].ID == n.ID {
					d.Nodes[i].FP = fp
				}
			}
			return nil
		})
		Note(r, "neues Zertifikat "+fp)
		a.Nodes.Refresh()
		writeJSON(w, 200, map[string]string{"fp": fp})
		return
	}
	name := strings.TrimSpace(req.Name)
	if name == "" || utf8.RuneCountInString(name) > 40 {
		fail(w, 400, errors.New("Ungültiger Name"))
		return
	}
	err := a.Store.Update(func(d *store.Data) error {
		for i := range d.Nodes {
			if d.Nodes[i].ID == r.PathValue("nid") {
				d.Nodes[i].Name = name
				return nil
			}
		}
		return errors.New("Rechner nicht gefunden")
	})
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) nodeDelete(w http.ResponseWriter, r *http.Request) {
	nid := r.PathValue("nid")
	err := a.Store.Update(func(d *store.Data) error {
		for i, n := range d.Nodes {
			if n.ID == nid {
				d.Nodes = append(d.Nodes[:i], d.Nodes[i+1:]...)
				// users limited to servers of that machine lose them
				for j := range d.Users {
					d.Users[j].Servers = dropPrefix(d.Users[j].Servers, nid+nodes.Sep)
				}
				return nil
			}
		}
		return errors.New("Rechner nicht gefunden")
	})
	if err != nil {
		fail(w, 404, err)
		return
	}
	a.Nodes.Refresh()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func dropPrefix(ids []string, prefix string) []string {
	out := ids[:0]
	for _, id := range ids {
		if !strings.HasPrefix(id, prefix) {
			out = append(out, id)
		}
	}
	return out
}

// proxyServer passes a request for /api/servers/<node>~<id>/… to the node.
// need() has already checked the user's role and access to that server.
func (a *API) proxyServer(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	nid, rid, _ := nodes.Split(id)
	n, ok := a.Nodes.Get(nid)
	if !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	}
	rest := strings.TrimPrefix(r.URL.Path, "/api/servers/"+id)
	// no way out of /api/servers/<id>/ on the node: no encoded slashes, no dot segments
	esc := strings.ToLower(r.URL.EscapedPath())
	if rest == r.URL.Path || strings.Contains(esc, "%2f") || strings.Contains(esc, "%5c") || strings.Contains(rest, "\\") ||
		strings.Contains(rest+"/", "/../") || strings.Contains(rest+"/", "/./") || strings.Contains(rest, "//") {
		fail(w, 400, errors.New("Unbekannter Pfad"))
		return
	}
	a.Nodes.Proxy(w, r, n, "/api/servers/"+rid+rest, rid, userOf(r).Name)
	if r.Method == http.MethodDelete && rest == "" { // deleted: gone from the list right away
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		a.Nodes.PollNode(ctx, n)
	} else if r.Method != http.MethodGet { // start, stop …: show it here soon
		a.Nodes.Refresh()
	}
}

// createOnNode creates a server on another machine.
func (a *API) createOnNode(w http.ResponseWriter, r *http.Request, nid string, body map[string]json.RawMessage) {
	n, ok := a.Nodes.Get(nid)
	if !ok {
		fail(w, 400, errors.New("Rechner nicht gefunden"))
		return
	}
	delete(body, "node")
	raw, _ := json.Marshal(body)
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Minute)
	defer cancel()
	code, b, err := a.Nodes.Do(ctx, n, "POST", "/api/servers", raw, userOf(r).Name)
	if err != nil {
		fail(w, 502, err)
		return
	}
	var v map[string]any
	if json.Unmarshal(b, &v) != nil {
		fail(w, 502, errors.New("Unerwartete Antwort vom Rechner"))
		return
	}
	if code < 300 {
		if id, _ := v["id"].(string); id != "" {
			v["id"], v["node"], v["node_name"] = nid+nodes.Sep+id, nid, n.Name
		}
		Note(r, "auf "+n.Name)
		a.Nodes.PollNode(ctx, n)
	}
	writeJSON(w, code, v)
}

// publicFromNodes answers a public request (invite page, mod list) for a
// server on another machine: the first node that knows the key answers.
// With pass set, that path is streamed from the node instead of the probe's
// answer (the mod pack).
func (a *API) publicFromNodes(w http.ResponseWriter, r *http.Request, probe, pass string) bool {
	if a.Nodes == nil || !rePublicKey.MatchString(path.Base(probe)) {
		return false
	}
	for _, st := range a.Nodes.Status() {
		if !st.Online {
			continue
		}
		n, ok := a.Nodes.Get(st.ID)
		if !ok {
			continue
		}
		ctx, cancel := context.WithTimeout(r.Context(), 20*time.Second)
		code, b, err := a.Nodes.Do(ctx, n, "GET", probe, nil, "")
		cancel()
		if err != nil || code != 200 {
			continue
		}
		if pass != "" {
			a.Nodes.Proxy(w, r, n, pass, "", "")
			return true
		}
		var v any
		if json.Unmarshal(b, &v) != nil {
			continue
		}
		if !fromLAN(r) { // the node sees this panel as a visitor from the home network
			v = dropKey(v, "lan_address")
		}
		writeJSON(w, 200, v)
		return true
	}
	return false
}

func dropKey(v any, key string) any {
	switch t := v.(type) {
	case map[string]any:
		delete(t, key)
		for k, x := range t {
			t[k] = dropKey(x, key)
		}
	case []any:
		for i, x := range t {
			t[i] = dropKey(x, key)
		}
	}
	return v
}

// keys in public links (invite tokens) are base64url
var rePublicKey = regexp.MustCompile(`^[A-Za-z0-9_-]{8,100}$`)
