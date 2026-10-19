package api

import (
	"context"
	"errors"
	"net"
	"net/http"
	"regexp"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Setup wizard: after the first admin account the panel walks through the
// address for players, the router and HTTPS. HTTPS and the router use their
// own calls; this file holds what the wizard needs besides.

func (a *API) routesWizard(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/wizard", a.need(auth.Admin, a.wizardGet))
	mux.HandleFunc("PUT /api/wizard", a.need(auth.Admin, a.wizardPut))
	mux.HandleFunc("POST /api/wizard/public-ip", a.need(auth.Admin, a.wizardPublicIP))
}

func (a *API) wizardGet(w http.ResponseWriter, r *http.Request) {
	var s store.Settings
	a.Store.Read(func(d *store.Data) { s = d.Settings })
	dockerErr := ""
	ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
	defer cancel()
	if a.Docker != nil {
		if err := a.Docker.Ping(ctx); err != nil {
			dockerErr = err.Error()
		}
	}
	tlsMode := ""
	if a.TLS != nil {
		tlsMode = s.TLS.Mode
	}
	writeJSON(w, 200, map[string]any{
		"done": s.WizardDone, "servers": len(a.Store.Servers()), "public_host": s.PublicHost, "lan_ip": manager.LANIP(),
		"upnp": s.UPnP, "https": tlsMode, "https_available": a.TLS != nil, "docker_error": dockerErr,
		"secure": r.TLS != nil,
	})
}

var reHost = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)*$`)

func (a *API) wizardPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		PublicHost *string `json:"public_host"`
		Done       *bool   `json:"done"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if in.PublicHost != nil {
		h := strings.ToLower(strings.TrimSpace(*in.PublicHost))
		h = strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(h, "https://"), "http://"), "/")
		if h != "" && net.ParseIP(h) == nil && !reHost.MatchString(h) {
			fail(w, 400, errors.New("Bitte nur die Domain oder IP eintragen, ohne Port, z. B. play.meine-domain.de"))
			return
		}
		in.PublicHost = &h
	}
	_ = a.Store.Update(func(d *store.Data) error {
		if in.PublicHost != nil {
			d.Settings.PublicHost = *in.PublicHost
		}
		if in.Done != nil {
			d.Settings.WizardDone = *in.Done
		}
		return nil
	})
	a.wizardGet(w, r)
}

// wizardPublicIP asks a public service for this connection's IPv4 address.
func (a *API) wizardPublicIP(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Second)
	defer cancel()
	ip, err := manager.ReachServices.PublicIP(ctx)
	if err != nil {
		fail(w, 502, errors.New("Die öffentliche IP ließ sich gerade nicht ermitteln"))
		return
	}
	writeJSON(w, 200, map[string]string{"ip": ip})
}
