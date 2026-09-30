// Command gamepanel is the self-hosted game server control panel.
package main

import (
	"context"
	"flag"
	"fmt"
	"io/fs"
	"log"
	"net/http"
	"os"
	"os/signal"
	"path/filepath"
	"syscall"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/api"
	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/hoststats"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
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
	a := &api.API{Auth: auth.New(st), Store: st, Mgr: mgr, Host: host, Docker: dk, Web: webFS, Version: version}
	mgr.Limit = func() int { return a.License().Edition.MaxServers }
	mgr.AutoBackups = func() bool { return a.License().Edition.AutoBackup }

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	go host.Run(ctx)
	go mgr.Run(ctx)

	if err := dk.Ping(ctx); err != nil {
		log.Printf("Warnung: %v", err)
	}

	srv := &http.Server{Addr: *listen, Handler: a.Handler(), ReadHeaderTimeout: 10 * time.Second}
	go func() {
		<-ctx.Done()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = srv.Shutdown(sctx)
	}()
	log.Printf("[Panel] %s läuft auf %s (Daten: %s, %d Vorlagen)", version, *listen, *dataDir, len(tpls.All()))
	if err := srv.ListenAndServe(); err != nil && err != http.ErrServerClosed {
		log.Fatal(err)
	}
}
