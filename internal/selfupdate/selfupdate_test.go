package selfupdate

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

func TestCheckAndApply(t *testing.T) {
	bin := []byte("#!/bin/sh\necho neu\n")
	sum := sha256.Sum256(bin)
	name := "gamepanel-linux-" + runtime.GOARCH
	good := true
	var srv *httptest.Server
	srv = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/repos/x/y/releases/latest":
			_ = json.NewEncoder(w).Encode(map[string]any{"tag_name": "v0.30.0", "body": "Neu: Spielerverwaltung", "html_url": "https://github.com/x/y/releases/v0.30.0",
				"assets": []map[string]string{{"name": name, "browser_download_url": srv.URL + "/bin"}, {"name": "SHA256SUMS", "browser_download_url": srv.URL + "/sums"}}})
		case "/bin":
			_, _ = w.Write(bin)
		case "/sums":
			s := hex.EncodeToString(sum[:])
			if !good {
				s = "0000000000000000000000000000000000000000000000000000000000000000"
			}
			fmt.Fprintf(w, "%s  install.sh\n%s  %s\n", s, s, name)
		}
	}))
	defer srv.Close()
	apiBase, Repo = srv.URL, "x/y"

	if in := Check(t.Context(), "v0.30.0"); in.Available || in.Error != "" {
		t.Fatalf("gleiche Version gilt als neu: %+v", in)
	}
	in := Check(t.Context(), "v0.27.2")
	if !in.Available || in.Latest != "v0.30.0" || in.Notes == "" {
		t.Fatalf("%+v", in)
	}
	exe := filepath.Join(t.TempDir(), "gamepanel")
	_ = os.WriteFile(exe, []byte("alt"), 0o755)
	good = false
	if err := Apply(t.Context(), in, exe); err == nil {
		t.Fatal("falsche Prüfsumme angenommen")
	}
	if b, _ := os.ReadFile(exe); string(b) != "alt" {
		t.Fatal("Datei trotz Fehler ersetzt")
	}
	good = true
	if err := Apply(t.Context(), in, exe); err != nil {
		t.Fatal(err)
	}
	if b, _ := os.ReadFile(exe); string(b) != string(bin) {
		t.Fatal("nicht ersetzt")
	}
	if b, _ := os.ReadFile(exe + ".old"); string(b) != "alt" {
		t.Fatal("alte Version fehlt")
	}
	if in := Check(t.Context(), "v0.27.2-4-gabcdef"); !in.Available || !in.DevBuild {
		t.Fatalf("Entwicklerstand: %+v", in)
	}
}
