package files

import (
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func TestResolveStaysInside(t *testing.T) {
	base := t.TempDir()
	dir := filepath.Join(base, "srv")
	os.MkdirAll(filepath.Join(dir, "saves"), 0o755)
	os.WriteFile(filepath.Join(base, "secret.txt"), []byte("x"), 0o644)
	os.Symlink(base, filepath.Join(dir, "escape"))
	r := New(dir)

	for _, p := range []string{"../secret.txt", "/../../etc/passwd", "saves/../../secret.txt"} {
		abs, err := r.Resolve(p)
		if err == nil && !strings.HasPrefix(abs, dir) {
			t.Fatalf("%s wurde nach %s aufgelöst", p, abs)
		}
	}
	if _, err := r.Resolve("escape/secret.txt"); err != ErrOutside {
		t.Fatalf("Symlink nach draußen nicht erkannt: %v", err)
	}
	if err := r.WriteText("saves/a.txt", "hallo"); err != nil {
		t.Fatal(err)
	}
	if s, err := r.ReadText("/saves/a.txt"); err != nil || s != "hallo" {
		t.Fatalf("ReadText = %q, %v", s, err)
	}
	os.WriteFile(filepath.Join(dir, "bin"), []byte{0, 1, 2}, 0o644)
	if _, err := r.ReadText("bin"); err != ErrBinary {
		t.Fatalf("Binärdatei nicht erkannt: %v", err)
	}
	if err := r.Delete("/"); err == nil {
		t.Fatal("Wurzel durfte gelöscht werden")
	}
	if err := r.Mkdir("", "../x"); err == nil {
		t.Fatal("ungültiger Ordnername akzeptiert")
	}
	if err := r.Rename("saves/a.txt", "b.txt"); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(filepath.Join(dir, "saves", "b.txt")); err != nil {
		t.Fatal("Umbenennen fehlgeschlagen")
	}
}
