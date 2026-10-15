package manager

import (
	"archive/tar"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
	"path/filepath"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Moving a server to another machine: the old machine stops the server and
// packs what a backup contains, plus its settings in the panel, history,
// player statistics and events. The new machine creates the server from it;
// the game files themselves come from Steam or the image again.
//
// A move package is a plain tar with meta.json, files.tar.gz (the backup
// part of the server folder) and panel/… (events, stats, history).

// MoveMeta describes the server in a move package.
type MoveMeta struct {
	Version    int          `json:"version"`
	Server     store.Server `json:"server"`
	WasRunning bool         `json:"was_running"`
	From       string       `json:"from"` // host name of the old machine
	Created    time.Time    `json:"created"`
}

const moveVersion = 1

// ExportMove stops the server and writes the move package to w.
func (m *Manager) ExportMove(ctx context.Context, id string, w io.Writer) (MoveMeta, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return MoveMeta{}, err
	}
	meta := MoveMeta{Version: moveVersion, Server: sv, Created: time.Now()}
	meta.From, _ = os.Hostname()
	if m.isRunning(ctx, sv) {
		meta.WasRunning = true
		m.setJob(id, "Server wird für den Umzug gestoppt")
		m.expect(id)
		if err := m.dk.Stop(ctx, sv.ContainerID, tpl.StopTimeout); err != nil {
			m.clearJob(id)
			return meta, err
		}
		m.refresh(id)
	}
	unlock := m.lock(id)
	defer unlock()
	m.setJob(id, "Umzug: Dateien werden gepackt")
	defer m.clearJob(id)
	dir := m.ServerDir(id)
	roots, err := scopeRoots(dir, tpl)
	if err != nil {
		return meta, err
	}
	tmp, err := os.CreateTemp(m.dataDir, ".move-*.tar.gz")
	if err != nil {
		return meta, err
	}
	tmp.Close()
	defer os.Remove(tmp.Name())
	if err := writeArchive(ctx, tmp.Name(), dir, roots); err != nil { // empty roots: empty archive
		return meta, fmt.Errorf("Packen fehlgeschlagen: %w", err)
	}
	// what the panel keeps about the server
	meta.Server.ContainerID = ""
	tw := tar.NewWriter(w)
	b, _ := json.MarshalIndent(meta, "", " ")
	if err := tarBytes(tw, "meta.json", b); err != nil {
		return meta, err
	}
	if err := tarFile(tw, "files.tar.gz", tmp.Name()); err != nil {
		return meta, err
	}
	m.ev.mu.Lock()
	evs, _ := json.Marshal(m.loadEvents(id))
	m.ev.mu.Unlock()
	_ = tarBytes(tw, "panel/events.json", evs)
	statsMu.Lock()
	sb, _ := os.ReadFile(m.statsFile(id))
	statsMu.Unlock()
	if len(sb) > 0 {
		_ = tarBytes(tw, "panel/stats.json", sb)
	}
	if files, _ := os.ReadDir(m.historyDir(id)); len(files) > 0 {
		for _, f := range files {
			if strings.HasSuffix(f.Name(), ".csv") {
				_ = tarFile(tw, "panel/history/"+f.Name(), filepath.Join(m.historyDir(id), f.Name()))
			}
		}
	}
	return meta, tw.Close()
}

func tarBytes(tw *tar.Writer, name string, b []byte) error {
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: int64(len(b)), ModTime: time.Now(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err := tw.Write(b)
	return err
}

func tarFile(tw *tar.Writer, name, p string) error {
	f, err := os.Open(p)
	if err != nil {
		return err
	}
	defer f.Close()
	fi, err := f.Stat()
	if err != nil {
		return err
	}
	if err := tw.WriteHeader(&tar.Header{Name: name, Mode: 0o600, Size: fi.Size(), ModTime: fi.ModTime(), Typeflag: tar.TypeReg}); err != nil {
		return err
	}
	_, err = io.CopyN(tw, f, fi.Size())
	return err
}

// ReadMoveMeta reads only the description of a move package.
func ReadMoveMeta(p string) (MoveMeta, error) {
	var meta MoveMeta
	f, err := os.Open(p)
	if err != nil {
		return meta, err
	}
	defer f.Close()
	tr := tar.NewReader(f)
	hdr, err := tr.Next()
	if err != nil || hdr.Name != "meta.json" {
		return meta, errors.New("Das ist kein Umzugspaket des Panels")
	}
	if err := json.NewDecoder(io.LimitReader(tr, 4<<20)).Decode(&meta); err != nil {
		return meta, errors.New("Umzugspaket beschädigt")
	}
	if meta.Version > moveVersion {
		return meta, errors.New("Das Umzugspaket stammt von einer neueren Panel-Version. Bitte erst hier aktualisieren")
	}
	return meta, nil
}

// ImportMove creates the server from a move package (a file, deleted when
// done). start starts it afterwards. by appears in the history.
func (m *Manager) ImportMove(p, by string, start bool) (store.Server, error) {
	meta, err := ReadMoveMeta(p)
	if err != nil {
		os.Remove(p)
		return store.Server{}, err
	}
	old := meta.Server
	if _, ok := m.tpls.Get(old.Template); !ok {
		os.Remove(p)
		return store.Server{}, fmt.Errorf("Die Vorlage %s gibt es hier nicht. Ist das Panel hier auf demselben Stand?", old.Template)
	}
	req := CreateReq{Template: old.Template, Name: old.Name, Vars: old.Vars, MemoryMB: old.MemoryMB, Start: start, CreatedBy: old.CreatedBy, internal: true, keepVars: true}
	req.apply = func(sv *store.Server) {
		sv.CPUs, sv.UpdateOnRun, sv.Created = old.CPUs, old.UpdateOnRun, old.Created
		sv.Backup, sv.Backup.LastAuto = old.Backup, time.Time{}
		sv.Tasks, sv.GameEvents, sv.Watch, sv.NoRouter = old.Tasks, old.GameEvents, old.Watch, old.NoRouter
		sv.Maintenance, sv.Invite, sv.Seasons = old.Maintenance, old.Invite, old.Seasons
	}
	done := make(chan error, 1)
	req.before = func(dir string, uid int) (err error) {
		defer func() { done <- err }()
		defer os.Remove(p)
		id := filepath.Base(dir)
		m.setJob(id, "Umzug: Spielstände werden ausgepackt")
		f, err := os.Open(p)
		if err != nil {
			return err
		}
		defer f.Close()
		tr := tar.NewReader(f)
		for {
			hdr, err := tr.Next()
			if err == io.EOF {
				break
			}
			if err != nil {
				return fmt.Errorf("Umzugspaket beschädigt: %w", err)
			}
			name := path.Clean(hdr.Name)
			switch {
			case name == "files.tar.gz":
				tmp, err := os.CreateTemp(m.dataDir, ".move-in-*.tar.gz")
				if err != nil {
					return err
				}
				n, err := io.Copy(tmp, tr)
				tmp.Close()
				if err == nil && n > 0 { // nothing to unpack when the server had no data yet
					err = extractArchive(tmp.Name(), dir)
				}
				os.Remove(tmp.Name())
				if err != nil {
					return fmt.Errorf("Spielstände auspacken: %w", err)
				}
			case name == "panel/events.json":
				_ = writeLimited(m.eventFile(id), tr)
			case name == "panel/stats.json":
				_ = writeLimited(m.statsFile(id), tr)
			case strings.HasPrefix(name, "panel/history/") && strings.HasSuffix(name, ".csv") && !strings.Contains(path.Base(name), ".."):
				_ = writeLimited(filepath.Join(m.historyDir(id), path.Base(name)), tr)
			}
		}
		from := meta.From
		if from == "" {
			from = "einem anderen Rechner"
		}
		m.Event(id, "info", fmt.Sprintf("Umgezogen von %s (%s)", from, by))
		return nil
	}
	sv, err := m.Create(req)
	if err != nil {
		os.Remove(p)
		return sv, err
	}
	// the old machine deletes its server only once the save games are here
	select {
	case err = <-done:
	case <-time.After(3 * time.Hour):
		err = errors.New("Auspacken dauert zu lange")
	}
	if err != nil {
		_ = m.Delete(context.Background(), sv.ID, true)
		return store.Server{}, err
	}
	return sv, nil
}

// MoveFile makes a temporary file for a move package in the data folder
// (packages can be large; /tmp is often small).
func (m *Manager) MoveFile() (*os.File, error) {
	dir := filepath.Join(m.dataDir, "moves")
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return nil, err
	}
	// leftovers of moves that broke off
	if old, _ := os.ReadDir(dir); len(old) > 0 {
		for _, f := range old {
			if fi, err := f.Info(); err == nil && time.Since(fi.ModTime()) > 24*time.Hour {
				os.Remove(filepath.Join(dir, f.Name()))
			}
		}
	}
	return os.CreateTemp(dir, "move-*.tar")
}

func writeLimited(p string, r io.Reader) error {
	if err := os.MkdirAll(filepath.Dir(p), 0o700); err != nil {
		return err
	}
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(r, 256<<20))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	return err
}
