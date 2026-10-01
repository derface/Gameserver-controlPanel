package manager

import (
	"context"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

func TestScopeAndArchive(t *testing.T) {
	dir := t.TempDir()
	mk := func(p, s string) {
		os.MkdirAll(filepath.Dir(filepath.Join(dir, p)), 0o755)
		os.WriteFile(filepath.Join(dir, p), []byte(s), 0o644)
	}
	mk("server/game.bin", "riesig")
	mk("server/Pal/Saved/world.sav", "welt")
	mk("saves/w.db", "save")
	mk("home/.config/x", "cfg")

	tpl := templates.Template{Kind: "steam", BackupInclude: []string{"server/Pal/Saved"}}
	roots, err := scopeRoots(dir, tpl)
	if err != nil {
		t.Fatal(err)
	}
	want := []string{"home", "saves", "server/Pal/Saved"}
	if len(roots) != 3 || roots[0] != want[0] || roots[1] != want[1] || roots[2] != want[2] {
		t.Fatalf("roots = %v", roots)
	}
	arc := filepath.Join(t.TempDir(), "b.tar.gz")
	if err := writeArchive(context.Background(), arc, dir, roots); err != nil {
		t.Fatal(err)
	}
	// change and delete, then restore
	mk("saves/w.db", "kaputt")
	os.RemoveAll(filepath.Join(dir, "home"))
	for _, r := range roots {
		os.RemoveAll(filepath.Join(dir, r))
	}
	if err := extractArchive(arc, dir); err != nil {
		t.Fatal(err)
	}
	for p, s := range map[string]string{"saves/w.db": "save", "home/.config/x": "cfg", "server/Pal/Saved/world.sav": "welt", "server/game.bin": "riesig"} {
		b, err := os.ReadFile(filepath.Join(dir, p))
		if err != nil || string(b) != s {
			t.Fatalf("%s = %q, %v", p, b, err)
		}
	}
}

func TestDue(t *testing.T) {
	now := time.Date(2026, 9, 30, 4, 10, 0, 0, time.Local)
	if !due(store.BackupPlan{EveryHours: 24, AtHour: 4, LastAuto: now.Add(-24 * time.Hour)}, now) {
		t.Fatal("täglich 4 Uhr nicht fällig")
	}
	if due(store.BackupPlan{EveryHours: 24, AtHour: 4, LastAuto: now.Add(-5 * time.Minute)}, now) {
		t.Fatal("zweimal am selben Tag")
	}
	if due(store.BackupPlan{EveryHours: 24, AtHour: 5}, now) {
		t.Fatal("falsche Stunde")
	}
	if !due(store.BackupPlan{EveryHours: 6, LastAuto: now.Add(-6 * time.Hour)}, now) {
		t.Fatal("6 h nicht fällig")
	}
	if due(store.BackupPlan{EveryHours: 0}, now) {
		t.Fatal("aus, aber fällig")
	}
}

func TestLayerDockerfile(t *testing.T) {
	base := templates.Template{Kind: "steam"}
	if SteamImageFor(base) != SteamImage {
		t.Fatal("ohne Pakete muss das Grund-Image genutzt werden")
	}
	a := templates.Template{Kind: "steam", Packages: []string{"lib32z1", "libtinfo5:i386"}}
	b := templates.Template{Kind: "steam", Packages: []string{"libtinfo5:i386", "lib32z1"}}
	if SteamImageFor(a) != SteamImageFor(b) || SteamImageFor(a) == SteamImage {
		t.Fatal("gleiche Pakete müssen das gleiche Image ergeben")
	}
	df, err := layerDockerfile(a)
	if err != nil || !strings.Contains(df, "apt-get install -y --no-install-recommends lib32z1 libtinfo5:i386") {
		t.Fatalf("Dockerfile: %v\n%s", err, df)
	}
	if _, err := layerDockerfile(templates.Template{Packages: []string{"x; rm -rf /"}}); err == nil {
		t.Fatal("ungültiger Paketname angenommen")
	}
	w, _ := layerDockerfile(templates.Template{Runtime: "wine"})
	if !strings.Contains(w, "winehq-stable") {
		t.Fatal("Wine fehlt")
	}
}

func TestAllTemplatesRender(t *testing.T) {
	set, err := templates.Load("")
	if err != nil {
		t.Fatal(err)
	}
	if len(set.All()) < 70 {
		t.Fatalf("nur %d Vorlagen", len(set.All()))
	}
	for _, tpl := range set.All() {
		if tpl.Custom {
			continue
		}
		vars, err := tpl.ResolveVars(map[string]string{"password": "geheim1", "admin_password": "geheim1", "rcon_password": "geheim1", "adminpassword": "geheim1"})
		if err != nil {
			t.Errorf("%s: %v", tpl.ID, err)
			continue
		}
		ports := []int{}
		for _, p := range tpl.Ports {
			ports = append(ports, p.Port)
		}
		for _, m := range regexp.MustCompile(`\{\{\s*([a-z0-9_]+)\s*\}\}`).FindAllStringSubmatch(tpl.Start+strings.Join(tpl.Cmd, " "), -1) {
			k := m[1]
			if _, ok := vars[k]; !ok && k != "port" && !regexp.MustCompile(`^port_[0-9]+$`).MatchString(k) {
				t.Errorf("%s: unbekannter Platzhalter %s", tpl.ID, k)
			}
			if strings.HasPrefix(k, "port_") {
				n, _ := strconv.Atoi(k[5:])
				if n > len(tpl.Ports) {
					t.Errorf("%s: %s, aber nur %d Ports", tpl.ID, k, len(tpl.Ports))
				}
			}
		}
		out := tpl.Render(tpl.Start, vars, ports)
		if strings.Contains(out, "{{") {
			t.Errorf("%s: Platzhalter übrig: %s", tpl.ID, out)
		}
		if tpl.Query != nil && tpl.Query.Port >= len(tpl.Ports) {
			t.Errorf("%s: Abfrage-Port %d existiert nicht", tpl.ID, tpl.Query.Port)
		}
	}
}
