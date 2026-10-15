package manager

import (
	"archive/zip"
	"bytes"
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/mods"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestClientModsMinecraft(t *testing.T) {
	m, st := settingsMgr(t)
	_ = st.Update(func(d *store.Data) error {
		d.Servers = append(d.Servers, store.Server{ID: "mc", Name: "MC", Template: "minecraft-java", MemoryMB: 4096,
			Vars: map[string]string{"type": "FABRIC", "version": "1.21.1", "mods": "sodium"}})
		return nil
	})
	dir := filepath.Join(m.ServerDir("mc"), "mods")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "sodium-0.5.jar"), []byte("sodium"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "servercore-1.0.jar"), []byte("servercore"), 0o644)
	_ = os.WriteFile(filepath.Join(dir, "unknown.jar"), []byte("whatever"), 0o644)
	hs, _ := mods.SHA1(filepath.Join(dir, "sodium-0.5.jar"))
	hc, _ := mods.SHA1(filepath.Join(dir, "servercore-1.0.jar"))
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch r.URL.Path {
		case "/version_files":
			json.NewEncoder(w).Encode(map[string]any{
				hs: map[string]any{"id": "v1", "project_id": "P1", "version_number": "0.5"},
				hc: map[string]any{"id": "v9", "project_id": "P2", "version_number": "1.0"}})
		case "/version_files/update":
			var in struct {
				Loaders      []string `json:"loaders"`
				GameVersions []string `json:"game_versions"`
			}
			json.NewDecoder(r.Body).Decode(&in)
			if len(in.Loaders) != 1 || in.Loaders[0] != "fabric" || in.GameVersions[0] != "1.21.1" {
				t.Errorf("filter %+v", in)
			}
			json.NewEncoder(w).Encode(map[string]any{
				hs: map[string]any{"id": "v2", "project_id": "P1", "version_number": "0.6"},
				hc: map[string]any{"id": "v9", "project_id": "P2", "version_number": "1.0"}})
		case "/projects":
			json.NewEncoder(w).Encode([]map[string]any{
				{"id": "P1", "slug": "sodium", "title": "Sodium", "client_side": "required"},
				{"id": "P2", "slug": "servercore", "title": "ServerCore", "client_side": "unsupported"}})
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	old := mods.BaseURL
	mods.BaseURL = srv.URL
	defer func() { mods.BaseURL = old }()

	cm, err := m.ClientMods(context.Background(), "mc")
	if err != nil {
		t.Fatal(err)
	}
	var names []string
	for _, x := range cm.Mods {
		names = append(names, x.Name)
	}
	sort.Strings(names)
	if strings.Join(names, ",") != "Sodium,unknown" || !cm.Download {
		t.Fatalf("%v %v", names, cm.Download)
	}
	var buf bytes.Buffer
	if err := m.WriteModPack(context.Background(), "mc", &buf); err != nil {
		t.Fatal(err)
	}
	zr, _ := zip.NewReader(bytes.NewReader(buf.Bytes()), int64(buf.Len()))
	var files []string
	for _, f := range zr.File {
		files = append(files, f.Name)
	}
	sort.Strings(files)
	if strings.Join(files, ",") != "mods/sodium-0.5.jar,mods/unknown.jar" {
		t.Fatalf("zip %v", files)
	}
	u := m.CheckModUpdates(context.Background(), "mc")
	if len(u.List) != 1 || u.List[0].Name != "Sodium" || u.List[0].To != "0.6" || !strings.Contains(u.List[0].Note, "Neustart") {
		t.Fatalf("%+v", u)
	}
	inv, _ := m.ModList(context.Background(), "mc")
	hint := false
	for _, e := range inv.Mods {
		if e.Name == "sodium-0.5" && e.UpdateHint {
			hint = true
		}
	}
	if !hint {
		t.Fatal("no update hint in mod list")
	}
}
