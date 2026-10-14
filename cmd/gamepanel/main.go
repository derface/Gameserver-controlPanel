// Command gamepanel is the self-hosted game server control panel.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"github.com/derface/Gameserver-controlPanel/internal/proxy"
	"io/fs"

	"github.com/derface/Gameserver-controlPanel/internal/mods"
	"log"
	"net"
	"os"
	"os/signal"
	"path/filepath"
	"sync"
	"syscall"

	"github.com/derface/Gameserver-controlPanel/internal/api"
	"github.com/derface/Gameserver-controlPanel/internal/audit"
	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/hoststats"
	"github.com/derface/Gameserver-controlPanel/internal/https"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/remote"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
	"github.com/derface/Gameserver-controlPanel/web"
)

var version = "dev"

func env(key, def string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return def
}

func main() {
	listen := flag.String("listen", env("GP_LISTEN", ":8420"), "Adresse des Web-Panels")
	dataDir := flag.String("data", env("GP_DATA", "/var/lib/gamepanel"), "Datenverzeichnis")
	sock := flag.String("docker", env("GP_DOCKER", "/var/run/docker.sock"), "Docker-Socket")
	if u := os.Getenv("GP_MODRINTH_URL"); u != "" { // for tests
		mods.BaseURL = u
	}
	noTLS := flag.Bool("no-tls", os.Getenv("GP_NO_TLS") != "", "nicht auf HTTPS umleiten (Notausgang, falls HTTPS falsch eingestellt ist)")
	acmeHTTP := flag.String("acme-http", env("GP_ACME_HTTP", ":80"), "Adresse für die Let's-Encrypt-Prüfung über HTTP")
	showVersion := flag.Bool("version", false, "Version anzeigen")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}

	st, err := store.Open(*dataDir)
	if err != nil {
		log.Fatalf("Datenverzeichnis %s: %v", *dataDir, err)
	}
	tpls, err := templates.Load(filepath.Join(*dataDir, "templates"))
	if err != nil {
		log.Fatalf("Vorlagen: %v", err)
	}
	dk := docker.New(*sock)
	mgr := manager.New(st, dk, tpls, *dataDir)
	host := hoststats.New(*dataDir)
	webFS, _ := fs.Sub(web.FS, "static")
	tlsm := https.New(*dataDir)
	tlsm.Off, tlsm.HTTPAddr = *noTLS, *acmeHTTP
	var tc store.TLSConfig
	var publicHost string
	st.Read(func(d *store.Data) {
		tc, publicHost = d.Settings.TLS, d.Settings.PublicHost
		proxy.Set(d.Settings.TrustedProxies)
	})
	if err := tlsm.Apply(tc, publicHost); err != nil {
		log.Printf("[HTTPS] %v", err)
	}
	if *noTLS && tc.Mode != "" {
		log.Printf("[HTTPS] Umleitung auf HTTPS ist abgeschaltet (GP_NO_TLS / -no-tls)")
	}
	a := &api.API{Auth: auth.New(st), Store: st, Mgr: mgr, Host: host, Docker: dk, Web: webFS, Version: version,
		Audit: audit.Open(*dataDir), TLS: tlsm}
	var rmu sync.Mutex
	var rkey string
	var rt remote.Target
	mgr.Remote = func() (remote.Target, remote.Config) {
		var c remote.Config
		st.Read(func(d *store.Data) { c = d.Settings.Remote })
		b, _ := json.Marshal(c)
		rmu.Lock()
		defer rmu.Unlock()
		if string(b) != rkey {
			rkey = string(b)
			rt, _ = remote.New(c, *dataDir)
		}
		return rt, c
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go host.Run(ctx)
	go mgr.Run(ctx)
	go a.RunUpdateCheck(ctx)

	if err := dk.Ping(ctx); err != nil {
		log.Printf("Warnung: %v", err)
	}

	ln, err := net.Listen("tcp", *listen)
	if err != nil {
		log.Fatal(err)
	}
	mode := "HTTP und HTTPS"
	if tlsm.Redirect() {
		mode = "HTTPS (HTTP wird umgeleitet)"
	}
	log.Printf("[Panel] %s läuft auf %s, %s (Daten: %s, %d Vorlagen)", version, *listen, mode, *dataDir, len(tpls.All()))
	if err := tlsm.Serve(ctx, ln, a.Handler()); err != nil {
		log.Fatal(err)
	}
}
