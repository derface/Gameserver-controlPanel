package manager

import (
	"context"
	"os"
	"path/filepath"
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
