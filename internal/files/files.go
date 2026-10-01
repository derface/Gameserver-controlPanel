// Package files gives safe access to one server's data folder.
// Every path from the browser is resolved inside that folder; symlinks
// that point outside are rejected.
package files

import (
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
	"unicode/utf8"
)

const (
	MaxEditSize = 2 << 20 // Textdateien bis 2 MB im Editor
	OwnerUID    = 1000    // Benutzer in den Spiel-Containern
)

var (
	ErrOutside = errors.New("Dieser Pfad liegt außerhalb des Serverordners")
	ErrBinary  = errors.New("Das ist keine Textdatei. Bitte herunterladen statt bearbeiten")
	ErrTooBig  = errors.New("Die Datei ist zu groß für den Editor (max. 2 MB). Bitte herunterladen")
)

type Entry struct {
	Name    string    `json:"name"`
	Dir     bool      `json:"dir"`
	Size    int64     `json:"size"`
	ModTime time.Time `json:"mod_time"`
}

type Root struct {
	dir string
	uid int
}

func New(dir string) Root { return Root{dir: filepath.Clean(dir), uid: OwnerUID} }

// WithOwner sets the user new and changed files belong to.
func (r Root) WithOwner(uid int) Root {
	if uid > 0 {
		r.uid = uid
	}
	return r
}

// Resolve turns a browser path ("/server/config.txt") into an absolute path.
// If the target exists, symlinks are resolved and checked; for a new file
// its parent is checked instead.
func (r Root) Resolve(rel string) (string, error) {
	rel = filepath.Clean("/" + filepath.FromSlash(rel))
	abs := filepath.Join(r.dir, rel)
	realRoot, err := filepath.EvalSymlinks(r.dir)
	if err != nil {
		return "", err
	}
	check := abs
	if _, err := os.Lstat(abs); errors.Is(err, fs.ErrNotExist) {
		check = filepath.Dir(abs)
	}
	real, err := filepath.EvalSymlinks(check)
	if err != nil {
		return "", err
	}
	if real != realRoot && !strings.HasPrefix(real, realRoot+string(os.PathSeparator)) {
		return "", ErrOutside
	}
	return abs, nil
}

func (r Root) isRoot(abs string) bool { return filepath.Clean(abs) == r.dir }

func (r Root) List(rel string) ([]Entry, error) {
	abs, err := r.Resolve(rel)
	if err != nil {
		return nil, err
	}
	des, err := os.ReadDir(abs)
	if err != nil {
		if errors.Is(err, fs.ErrNotExist) && r.isRoot(abs) {
			return []Entry{}, nil
		}
		return nil, friendly(err)
	}
	out := make([]Entry, 0, len(des))
	for _, d := range des {
		fi, err := d.Info()
		if err != nil {
			continue
		}
		dir := fi.IsDir()
		if fi.Mode()&os.ModeSymlink != 0 {
			if st, err := os.Stat(filepath.Join(abs, d.Name())); err == nil {
				dir = st.IsDir()
			}
		}
		out = append(out, Entry{Name: d.Name(), Dir: dir, Size: fi.Size(), ModTime: fi.ModTime()})
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Dir != out[j].Dir {
			return out[i].Dir
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

func (r Root) ReadText(rel string) (string, error) {
	abs, err := r.Resolve(rel)
	if err != nil {
		return "", err
	}
	fi, err := os.Stat(abs)
	if err != nil {
		return "", friendly(err)
	}
	if fi.IsDir() {
		return "", errors.New("Das ist ein Ordner")
	}
	if fi.Size() > MaxEditSize {
		return "", ErrTooBig
	}
	b, err := os.ReadFile(abs)
	if err != nil {
		return "", friendly(err)
	}
	if !utf8.Valid(b) || strings.ContainsRune(string(b), 0) {
		return "", ErrBinary
	}
	return string(b), nil
}

func (r Root) WriteText(rel, content string) error {
	if len(content) > MaxEditSize {
		return ErrTooBig
	}
	abs, err := r.Resolve(rel)
	if err != nil {
		return err
	}
	if r.isRoot(abs) {
		return errors.New("Ungültiger Dateiname")
	}
	mode := os.FileMode(0o644)
	if fi, err := os.Stat(abs); err == nil {
		if fi.IsDir() {
			return errors.New("Das ist ein Ordner")
		}
		mode = fi.Mode().Perm()
	}
	tmp := abs + ".gp-tmp"
	if err := os.WriteFile(tmp, []byte(content), mode); err != nil {
		return friendly(err)
	}
	_ = os.Chown(tmp, r.uid, r.uid)
	return os.Rename(tmp, abs)
}

// Open returns a file for download.
func (r Root) Open(rel string) (*os.File, fs.FileInfo, error) {
	abs, err := r.Resolve(rel)
	if err != nil {
		return nil, nil, err
	}
	f, err := os.Open(abs)
	if err != nil {
		return nil, nil, friendly(err)
	}
	fi, err := f.Stat()
	if err != nil || fi.IsDir() {
		f.Close()
		return nil, nil, errors.New("Ordner können nicht heruntergeladen werden")
	}
	return f, fi, nil
}

func validName(name string) error {
	if name == "" || name == "." || name == ".." || strings.ContainsAny(name, "/\\\x00") {
		return fmt.Errorf("„%s“ ist kein gültiger Name", name)
	}
	return nil
}

// Save stores an uploaded file in the directory rel.
func (r Root) Save(rel, name string, src io.Reader) error {
	if err := validName(name); err != nil {
		return err
	}
	dir, err := r.Resolve(rel)
	if err != nil {
		return err
	}
	abs, err := r.Resolve(filepath.Join(rel, name))
	if err != nil {
		return err
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return friendly(err)
	}
	tmp := abs + ".gp-upload"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o644)
	if err != nil {
		return friendly(err)
	}
	if _, err := io.Copy(f, src); err != nil {
		f.Close()
		os.Remove(tmp)
		return err
	}
	f.Close()
	_ = os.Chown(tmp, r.uid, r.uid)
	return os.Rename(tmp, abs)
}

func (r Root) Mkdir(rel, name string) error {
	if err := validName(name); err != nil {
		return err
	}
	abs, err := r.Resolve(filepath.Join(rel, name))
	if err != nil {
		return err
	}
	if err := os.Mkdir(abs, 0o755); err != nil {
		return friendly(err)
	}
	return os.Chown(abs, r.uid, r.uid)
}

func (r Root) Rename(rel, newName string) error {
	if err := validName(newName); err != nil {
		return err
	}
	abs, err := r.Resolve(rel)
	if err != nil {
		return err
	}
	if r.isRoot(abs) {
		return errors.New("Der Serverordner selbst kann nicht umbenannt werden")
	}
	dst, err := r.Resolve(filepath.Join(filepath.Dir(filepath.Clean("/"+rel)), newName))
	if err != nil {
		return err
	}
	if _, err := os.Lstat(dst); err == nil {
		return fmt.Errorf("„%s“ gibt es schon", newName)
	}
	return friendly(os.Rename(abs, dst))
}

func (r Root) Delete(rel string) error {
	abs, err := r.Resolve(rel)
	if err != nil {
		return err
	}
	if r.isRoot(abs) {
		return errors.New("Der Serverordner selbst kann nicht gelöscht werden")
	}
	if _, err := os.Lstat(abs); err != nil {
		return friendly(err)
	}
	return friendly(os.RemoveAll(abs))
}

func friendly(err error) error {
	switch {
	case err == nil:
		return nil
	case errors.Is(err, fs.ErrNotExist):
		return errors.New("Datei oder Ordner nicht gefunden")
	case errors.Is(err, fs.ErrExist):
		return errors.New("Gibt es schon")
	case errors.Is(err, fs.ErrPermission):
		return errors.New("Keine Berechtigung")
	}
	return err
}
