package api

import (
	"context"
	"errors"
	"github.com/derface/Gameserver-controlPanel/internal/proxy"
	"net/http"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/https"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func (a *API) httpsStatus(r *http.Request) map[string]any {
	st := https.Status{}
	if a.TLS != nil {
		st = a.TLS.Status()
	}
	return map[string]any{"status": st, "secure": proxy.HTTPS(r), "available": a.TLS != nil}
}

func (a *API) httpsGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.httpsStatus(r))
}

func (a *API) httpsPut(w http.ResponseWriter, r *http.Request) {
	if a.TLS == nil {
		fail(w, 400, errors.New("HTTPS ist in diesem Panel nicht verfügbar."))
		return
	}
	var in struct {
		store.TLSConfig
		Cert string `json:"cert"`
		Key  string `json:"key"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	c := store.TLSConfig{Mode: in.Mode, Domain: strings.ToLower(strings.TrimSpace(in.Domain)), Email: strings.TrimSpace(in.Email)}
	if err := https.Validate(c); err != nil {
		fail(w, 400, err)
		return
	}
	if strings.TrimSpace(in.Cert) != "" || strings.TrimSpace(in.Key) != "" {
		if err := a.TLS.SetCustom(in.Cert, in.Key); err != nil {
			fail(w, 400, err)
			return
		}
		Note(r, "eigenes Zertifikat hochgeladen")
	}
	if c.Mode == "custom" && !a.TLS.Status().HasOwn {
		fail(w, 400, errors.New("Bitte Zertifikat und privaten Schlüssel einfügen."))
		return
	}
	var host string
	if err := a.Store.Update(func(d *store.Data) error {
		d.Settings.TLS = c
		host = d.Settings.PublicHost
		return nil
	}); err != nil {
		fail(w, 500, err)
		return
	}
	if err := a.TLS.Apply(c, host, r.Host); err != nil {
		fail(w, 400, err)
		return
	}
	Note(r, map[string]string{"": "nur HTTP", "self": "selbst signiert", "acme": "Let's Encrypt " + c.Domain, "custom": "eigenes Zertifikat"}[c.Mode])
	writeJSON(w, 200, a.httpsStatus(r))
}

func (a *API) httpsRenew(w http.ResponseWriter, r *http.Request) {
	if a.TLS == nil {
		fail(w, 400, errors.New("HTTPS ist in diesem Panel nicht verfügbar."))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 90*time.Second)
	defer cancel()
	if err := a.TLS.Renew(ctx); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, a.httpsStatus(r))
}

// Reverse proxy (Traefik, nginx …) in front of the panel.
func (a *API) proxyGet(w http.ResponseWriter, r *http.Request) {
	var l []string
	a.Store.Read(func(d *store.Data) { l = d.Settings.TrustedProxies })
	if l == nil {
		l = []string{}
	}
	writeJSON(w, 200, map[string]any{"trusted": l, "seen": proxy.Seen(), "via_proxy": proxy.Trusted(r), "client": proxy.ClientIP(r)})
}

func (a *API) proxyPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Trusted []string `json:"trusted"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	l, err := proxy.Parse(in.Trusted)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if err := a.Store.Update(func(d *store.Data) error { d.Settings.TrustedProxies = l; return nil }); err != nil {
		fail(w, 500, err)
		return
	}
	proxy.Set(l)
	Note(r, strings.Join(l, ", "))
	a.proxyGet(w, r)
}
