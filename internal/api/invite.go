package api

import (
	"crypto/rand"
	"encoding/base64"
	"errors"
	"net/http"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Invite links: one public page per server with everything a friend needs
// to join. The token in the link is the only key.

func (a *API) routesInvite(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/invite", a.need(auth.Operator, a.inviteGet))
	mux.HandleFunc("PUT /api/servers/{id}/invite", a.need(auth.Operator, a.invitePut))
	mux.HandleFunc("DELETE /api/servers/{id}/invite", a.need(auth.Operator, a.inviteDelete))
	mux.HandleFunc("GET /api/public/invite/{token}", a.invitePublic)
}

func newToken() string {
	b := make([]byte, 16)
	_, _ = rand.Read(b)
	return base64.RawURLEncoding.EncodeToString(b)
}

func (a *API) inviteView(sv store.Server) map[string]any {
	c := a.statusCfg()
	base := ""
	if c.Enabled && c.Host != "" {
		base = "https://" + c.Host
	}
	out := map[string]any{"invite": sv.Invite, "base": base}
	return out
}

func (a *API) inviteGet(w http.ResponseWriter, r *http.Request) {
	sv, ok := a.Store.Server(r.PathValue("id"))
	if !ok {
		fail(w, 404, errNotFound)
		return
	}
	writeJSON(w, 200, a.inviteView(sv))
}

func (a *API) invitePut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Note  string `json:"note"`
		Renew bool   `json:"renew"` // new link, the old one stops working
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	in.Note = strings.TrimSpace(in.Note)
	if len(in.Note) > 300 {
		fail(w, 400, errTooLong)
		return
	}
	err := a.Store.ModifyServer(r.PathValue("id"), func(sv *store.Server) error {
		if sv.Invite == nil || in.Renew {
			sv.Invite = &store.Invite{Token: newToken(), Created: time.Now()}
		}
		sv.Invite.Note = in.Note
		return nil
	})
	if err != nil {
		fail(w, 400, err)
		return
	}
	sv, _ := a.Store.Server(r.PathValue("id"))
	writeJSON(w, 200, a.inviteView(sv))
}

func (a *API) inviteDelete(w http.ResponseWriter, r *http.Request) {
	err := a.Store.ModifyServer(r.PathValue("id"), func(sv *store.Server) error { sv.Invite = nil; return nil })
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) invitePublic(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	tok := r.PathValue("token")
	var sv store.Server
	found := false
	for _, s := range a.Store.Servers() {
		if s.Invite != nil && len(tok) >= 16 && s.Invite.Token == tok {
			sv, found = s, true
			break
		}
	}
	if !found {
		if len(tok) >= 16 && a.publicFromNodes(w, r, "/api/public/invite/"+tok, "") {
			return // a server on another machine
		}
		fail(w, 404, errors.New("Diesen Einladungslink gibt es nicht (mehr)."))
		return
	}
	c := a.statusCfg()
	host := ""
	a.Store.Read(func(d *store.Data) { host = d.Settings.PublicHost })
	if host == "" {
		host = hostOnly(r.Host)
		if c.Host != "" && strings.EqualFold(host, c.Host) {
			host = ""
		}
	}
	list := a.publicServers(func(id string) (bool, string) { return id == sv.ID, sv.Invite.Note }, c.ShowNames, host)
	if len(list) == 0 {
		fail(w, 404, errNotFound)
		return
	}
	p := list[0]
	if p.Address == "" {
		// also when the server is off: friends keep the link for later
		h := host
		if h == "" {
			h = hostOnly(r.Host)
		}
		p.Address, p.SteamLink, p.Hint = a.Mgr.JoinInfo(sv.ID, h)
	}
	if lan := manager.LANIP(); fromLAN(r) && lan != "" {
		p.LANAddr, _, _ = a.Mgr.JoinInfo(sv.ID, lan)
	}
	title := c.Title
	if title == "" {
		title = "Unsere Gameserver"
	}
	status := ""
	if c.Enabled {
		status = "/status"
		if c.Host != "" {
			status = "https://" + c.Host + "/"
		}
	}
	writeJSON(w, 200, map[string]any{"title": title, "server": p, "status": status})
}
