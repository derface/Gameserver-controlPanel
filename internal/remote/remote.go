// Package remote stores backup copies outside the machine: in a folder
// (second disk, NAS mount) or in S3-compatible object storage
// (Backblaze B2, Wasabi, MinIO, AWS S3, Hetzner Object Storage).
package remote

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

type Object struct {
	Key     string
	Size    int64
	ModTime time.Time
}

type Target interface {
	Put(ctx context.Context, key, file string) error
	Get(ctx context.Context, key string, w io.Writer) error
	List(ctx context.Context, prefix string) ([]Object, error)
	Delete(ctx context.Context, key string) error
	Describe() string
}

// Config is stored in the panel settings.
type Config struct {
	Type      string `json:"type"` // "" | "dir" | "s3"
	Dir       string `json:"dir,omitempty"`
	Endpoint  string `json:"endpoint,omitempty"` // https://s3.eu-central-003.backblazeb2.com
	Region    string `json:"region,omitempty"`
	Bucket    string `json:"bucket,omitempty"`
	AccessKey string `json:"access_key,omitempty"`
	SecretKey string `json:"secret_key,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	PathStyle bool   `json:"path_style,omitempty"`
	Keep      int    `json:"keep"` // externe Backups je Server
}

// New builds a target from the config. dataDir is refused as a folder target.
func New(c Config, dataDir string) (Target, error) {
	switch c.Type {
	case "":
		return nil, nil
	case "dir":
		d := filepath.Clean(strings.TrimSpace(c.Dir))
		if !filepath.IsAbs(d) {
			return nil, errors.New("Der Ordner muss ein absoluter Pfad sein, z. B. /mnt/backup")
		}
		dd := filepath.Clean(dataDir)
		if d == dd || strings.HasPrefix(d, dd+string(os.PathSeparator)) || d == "/" {
			return nil, errors.New("Der Ordner darf nicht im Datenverzeichnis des Panels liegen. Nimm eine zweite Platte oder ein Netzlaufwerk")
		}
		return &Dir{Root: d}, nil
	case "s3":
		return NewS3(c)
	}
	return nil, fmt.Errorf("Unbekanntes Backup-Ziel „%s“", c.Type)
}

// Test writes, lists, reads and deletes a small file.
func Test(ctx context.Context, t Target) error {
	tmp, err := os.CreateTemp("", "gp-test-*")
	if err != nil {
		return err
	}
	defer os.Remove(tmp.Name())
	payload := "Verbindungstest Bytewise Panel " + time.Now().Format(time.RFC3339)
	tmp.WriteString(payload)
	tmp.Close()
	key := "_verbindungstest.txt"
	if err := t.Put(ctx, key, tmp.Name()); err != nil {
		return fmt.Errorf("Schreiben fehlgeschlagen: %w", err)
	}
	var sb strings.Builder
	if err := t.Get(ctx, key, &sb); err != nil {
		return fmt.Errorf("Lesen fehlgeschlagen: %w", err)
	}
	if sb.String() != payload {
		return errors.New("Die gelesene Testdatei stimmt nicht mit der geschriebenen überein")
	}
	if _, err := t.List(ctx, ""); err != nil {
		return fmt.Errorf("Auflisten fehlgeschlagen: %w", err)
	}
	if err := t.Delete(ctx, key); err != nil {
		return fmt.Errorf("Löschen fehlgeschlagen: %w", err)
	}
	return nil
}

// ---------- folder ----------

type Dir struct{ Root string }

func (d *Dir) Describe() string { return "Ordner " + d.Root }

func (d *Dir) path(key string) (string, error) {
	clean := path.Clean("/" + key)
	if strings.Contains(key, "..") {
		return "", errors.New("ungültiger Name")
	}
	return filepath.Join(d.Root, filepath.FromSlash(clean)), nil
}

func (d *Dir) Put(ctx context.Context, key, file string) error {
	if fi, err := os.Stat(d.Root); err != nil || !fi.IsDir() {
		return fmt.Errorf("Der Ordner %s existiert nicht. Ist die Platte eingehängt?", d.Root)
	}
	dst, err := d.path(key)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(filepath.Dir(dst), 0o750); err != nil {
		return err
	}
	src, err := os.Open(file)
	if err != nil {
		return err
	}
	defer src.Close()
	tmp := dst + ".part"
	out, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, ctxReader{ctx, src}); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	if err := out.Sync(); err != nil {
		out.Close()
		os.Remove(tmp)
		return err
	}
	out.Close()
	return os.Rename(tmp, dst)
}

func (d *Dir) Get(ctx context.Context, key string, w io.Writer) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	_, err = io.Copy(w, ctxReader{ctx, f})
	return err
}

func (d *Dir) List(ctx context.Context, prefix string) ([]Object, error) {
	base, err := d.path(prefix)
	if err != nil {
		return nil, err
	}
	if _, err := os.Stat(d.Root); err != nil {
		return nil, fmt.Errorf("Der Ordner %s existiert nicht. Ist die Platte eingehängt?", d.Root)
	}
	var out []Object
	walkRoot := base
	if !strings.HasSuffix(prefix, "/") && prefix != "" {
		walkRoot = filepath.Dir(base)
	}
	err = filepath.WalkDir(walkRoot, func(p string, e os.DirEntry, err error) error {
		if err != nil {
			if os.IsNotExist(err) {
				return filepath.SkipDir
			}
			return err
		}
		if e.IsDir() || strings.HasSuffix(p, ".part") {
			return nil
		}
		rel, _ := filepath.Rel(d.Root, p)
		key := filepath.ToSlash(rel)
		if !strings.HasPrefix(key, prefix) {
			return nil
		}
		fi, err := e.Info()
		if err != nil {
			return nil
		}
		out = append(out, Object{Key: key, Size: fi.Size(), ModTime: fi.ModTime()})
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Key < out[j].Key })
	return out, err
}

func (d *Dir) Delete(ctx context.Context, key string) error {
	p, err := d.path(key)
	if err != nil {
		return err
	}
	err = os.Remove(p)
	if os.IsNotExist(err) {
		return nil
	}
	return err
}

type ctxReader struct {
	ctx context.Context
	r   io.Reader
}

func (c ctxReader) Read(p []byte) (int, error) {
	if err := c.ctx.Err(); err != nil {
		return 0, err
	}
	return c.r.Read(p)
}
