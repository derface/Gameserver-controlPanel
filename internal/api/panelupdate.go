package api

import (
	"context"
	"errors"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sync"
	"syscall"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/selfupdate"
)

// Panel self-update: check GitHub every 6 hours, install on request.

var upd struct {
	sync.Mutex
	info     selfupdate.Info
	busy     bool
	notified string
}

func (a *API) routesPanelUpdate(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/panel/update", a.need(auth.Admin, a.panelUpdateGet))
	mux.HandleFunc("POST /api/panel/update", a.need(auth.Admin, a.panelUpdateApply))
}

func (a *API) checkPanelUpdate(ctx context.Context) selfupdate.Info {
	in := selfupdate.Check(ctx, a.Version)
	upd.Lock()
	upd.info = in
	first := in.Available && upd.notified != in.Latest
	if first {
		upd.notified = in.Latest
	}
	upd.Unlock()
	if first {
		a.Mgr.NotifyPanel("panel", "info", "Bytewise Panel "+in.Latest, "Eine neue Panel-Version ist da ("+in.Latest+", installiert "+a.Version+"). Installieren unter Einstellungen → Panel & Updates.")
	}
	return in
}

// RunUpdateCheck checks shortly after the start and then every 6 hours.
func (a *API) RunUpdateCheck(ctx context.Context) {
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	for {
		cctx, cancel := context.WithTimeout(ctx, time.Minute)
		a.checkPanelUpdate(cctx)
		cancel()
		select {
		case <-ctx.Done():
			return
		case <-time.After(6 * time.Hour):
		}
	}
}

// updateState is the short version for /api/state (admins only).
func updateState() map[string]any {
	upd.Lock()
	defer upd.Unlock()
	if upd.info.Checked.IsZero() {
		return nil
	}
	return map[string]any{"available": upd.info.Available, "latest": upd.info.Latest}
}

func (a *API) panelUpdateGet(w http.ResponseWriter, r *http.Request) {
	upd.Lock()
	in, busy := upd.info, upd.busy
	upd.Unlock()
	if r.URL.Query().Get("refresh") == "1" || in.Checked.IsZero() || time.Since(in.Checked) > time.Hour {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		in = a.checkPanelUpdate(ctx)
		cancel()
	}
	if in.Source == "" {
		in.Source = "https://github.com/" + selfupdate.Repo
	}
	writeJSON(w, 200, map[string]any{"info": in, "busy": busy})
}

func (a *API) panelUpdateApply(w http.ResponseWriter, r *http.Request) {
	upd.Lock()
	if upd.busy {
		upd.Unlock()
		fail(w, 409, errors.New("Das Update läuft schon"))
		return
	}
	upd.busy = true
	upd.Unlock()
	done := func() { upd.Lock(); upd.busy = false; upd.Unlock() }
	ctx, cancel := context.WithTimeout(r.Context(), 10*time.Minute)
	defer cancel()
	in := a.checkPanelUpdate(ctx)
	if in.Error != "" {
		done()
		fail(w, 400, errors.New(in.Error))
		return
	}
	if !in.Available && !in.DevBuild {
		done()
		fail(w, 400, errors.New("Es gibt keine neuere Version"))
		return
	}
	exe, err := os.Executable()
	if err == nil {
		exe, err = filepath.EvalSymlinks(exe)
	}
	if err != nil {
		done()
		fail(w, 500, err)
		return
	}
	if err := selfupdate.Apply(ctx, in, exe); err != nil {
		done()
		fail(w, 400, err)
		return
	}
	Note(r, a.Version+" → "+in.Latest)
	writeJSON(w, 200, map[string]string{"message": "Version " + in.Latest + " ist installiert. Das Panel startet neu, die Spieleserver laufen weiter."})
	if f, ok := w.(http.Flusher); ok {
		f.Flush()
	}
	go func() {
		time.Sleep(800 * time.Millisecond)
		log.Printf("[Panel] Update auf %s, starte neu", in.Latest)
		// replace the process in place; systemd keeps the same unit
		if err := syscall.Exec(exe, os.Args, os.Environ()); err != nil {
			log.Printf("[Panel] Neustart fehlgeschlagen (%v), beende mich für systemd", err)
			os.Exit(1)
		}
	}()
}
