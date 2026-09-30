package remote

import (
	"bytes"
	"context"
	"crypto/rand"
	"os"
	"path/filepath"
	"testing"
)

// Runs against any S3-compatible endpoint when GP_S3_TEST=endpoint is set
// (e.g. a local moto or MinIO server with bucket "gp-test").
func TestS3Integration(t *testing.T) {
	ep := os.Getenv("GP_S3_TEST")
	if ep == "" {
		t.Skip("GP_S3_TEST nicht gesetzt")
	}
	s, err := NewS3(Config{Type: "s3", Endpoint: ep, Region: "eu-central-003", Bucket: "gp-test", AccessKey: "a", SecretKey: "b", Prefix: "panel", PathStyle: true})
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if err := Test(ctx, s); err != nil {
		t.Fatal(err)
	}
	dir := t.TempDir()
	big := make([]byte, multipartFrom+partSize/2) // erzwingt Multipart mit 3 Teilen
	rand.Read(big)
	p := filepath.Join(dir, "gross.tar.gz")
	os.WriteFile(p, big, 0o600)
	if err := s.Put(ctx, "srv_Test/2026-09-30_20-00-00_manuell.tar.gz", p); err != nil {
		t.Fatal("multipart:", err)
	}
	os.WriteFile(filepath.Join(dir, "klein"), []byte("klein"), 0o600)
	if err := s.Put(ctx, "srv_Test/2026-09-30_20-00-01_auto.tar.gz", filepath.Join(dir, "klein")); err != nil {
		t.Fatal(err)
	}
	list, err := s.List(ctx, "srv_Test/")
	if err != nil || len(list) != 2 {
		t.Fatalf("List = %v, %v", list, err)
	}
	var got bytes.Buffer
	if err := s.Get(ctx, "srv_Test/2026-09-30_20-00-00_manuell.tar.gz", &got); err != nil || !bytes.Equal(got.Bytes(), big) {
		t.Fatalf("Get: %v, gleich=%v", err, bytes.Equal(got.Bytes(), big))
	}
	for _, o := range list {
		if err := s.Delete(ctx, o.Key); err != nil {
			t.Fatal(err)
		}
	}
}

func TestDirTarget(t *testing.T) {
	root := t.TempDir()
	if _, err := New(Config{Type: "dir", Dir: "/var/lib/gamepanel/x"}, "/var/lib/gamepanel"); err == nil {
		t.Fatal("Ordner im Datenverzeichnis wurde akzeptiert")
	}
	d, err := New(Config{Type: "dir", Dir: root}, "/var/lib/gamepanel")
	if err != nil {
		t.Fatal(err)
	}
	if err := Test(context.Background(), d); err != nil {
		t.Fatal(err)
	}
	f := filepath.Join(t.TempDir(), "b")
	os.WriteFile(f, []byte("daten"), 0o600)
	d.Put(context.Background(), "a_Server/x.tar.gz", f)
	list, _ := d.List(context.Background(), "a_Server/")
	if len(list) != 1 || list[0].Key != "a_Server/x.tar.gz" {
		t.Fatalf("List = %v", list)
	}
	if _, err := d.(*Dir).path("../../etc/passwd"); err == nil {
		t.Fatal("Pfad nach draußen akzeptiert")
	}
}
