package manager

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
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/remote"
)

// Backup copies on the external target live under "<server-id>/<backup-name>".

type uploads struct {
	mu    sync.Mutex
	state map[string]string // "id/name" -> "" (läuft) oder Fehlertext
}

func (u *uploads) set(key, v string) {
	u.mu.Lock()
	if u.state == nil {
		u.state = map[string]string{}
	}
	u.state[key] = v
	u.mu.Unlock()
}

func (u *uploads) done(key string) {
	u.mu.Lock()
	delete(u.state, key)
	u.mu.Unlock()
}

func (u *uploads) get(key string) (string, bool) {
	u.mu.Lock()
	defer u.mu.Unlock()
	v, ok := u.state[key]
	return v, ok
}

func (m *Manager) target() (remote.Target, remote.Config) {
	if m.Remote == nil {
		return nil, remote.Config{}
	}
	return m.Remote()
}

// uploadAsync copies a local backup to the external target and prunes there.
func (m *Manager) uploadAsync(id, name string) {
	t, cfg := m.target()
	if t == nil {
		return
	}
	key := id + "/" + name
	m.up.set(key, "")
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
		defer cancel()
		if err := t.Put(ctx, key, filepath.Join(m.backupDir(id), name)); err != nil {
			m.up.set(key, "Externe Kopie fehlgeschlagen: "+err.Error())
			m.EventKind(id, "error", "error", "Externe Kopie von "+name+" fehlgeschlagen: "+err.Error())
			return
		}
		m.up.done(key)
		m.pruneRemote(ctx, t, cfg, id)
	}()
}

// UploadBackup retries or starts the external copy of one backup.
func (m *Manager) UploadBackup(id, name string) error {
	if _, err := m.BackupFile(id, name); err != nil {
		return err
	}
	if t, _ := m.target(); t == nil {
		return errors.New("Kein externes Backup-Ziel eingerichtet")
	}
	m.uploadAsync(id, name)
	return nil
}

func (m *Manager) pruneRemote(ctx context.Context, t remote.Target, cfg remote.Config, id string) {
	objs, err := t.List(ctx, id+"/")
	if err != nil {
		return
	}
	keep := map[string]int{KindAuto: cfg.Keep, KindRestore: 3}
	if keep[KindAuto] <= 0 {
		keep[KindAuto] = 14
	}
	sort.Slice(objs, func(i, j int) bool { return objs[i].Key > objs[j].Key })
	count := map[string]int{}
	for _, o := range objs {
		name := path.Base(o.Key)
		if !backupName.MatchString(name) {
			continue
		}
		kind := strings.TrimSuffix(name[20:], ".tar.gz")
		lim, managed := keep[kind]
		if !managed {
			continue
		}
		count[kind]++
		if count[kind] > lim {
			_ = t.Delete(ctx, o.Key)
		}
	}
}

// remoteList returns the external backups of a server by name.
func (m *Manager) remoteList(ctx context.Context, id string) (map[string]remote.Object, error) {
	t, _ := m.target()
	if t == nil {
		return nil, nil
	}
	objs, err := t.List(ctx, id+"/")
	if err != nil {
		return nil, err
	}
	out := map[string]remote.Object{}
	for _, o := range objs {
		name := path.Base(o.Key)
		if backupName.MatchString(name) && o.Key == id+"/"+name {
			out[name] = o
		}
	}
	return out, nil
}

// BackupsWithRemote merges local and external backups.
func (m *Manager) BackupsWithRemote(ctx context.Context, id string) ([]BackupInfo, string, error) {
	local, err := m.ListBackups(id)
	if err != nil {
		return nil, "", err
	}
	rctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	rem, rerr := m.remoteList(rctx, id)
	remoteErr := ""
	if rerr != nil {
		remoteErr = "Externer Speicher nicht erreichbar: " + rerr.Error()
	}
	t, _ := m.target()
	byName := map[string]int{}
	for i := range local {
		local[i].Local = true
		byName[local[i].Name] = i
		if _, ok := rem[local[i].Name]; ok {
			local[i].Remote = true
		}
		if t != nil {
			if st, ok := m.up.get(id + "/" + local[i].Name); ok {
				if st == "" {
					local[i].Upload = "läuft"
				} else {
					local[i].Upload = st
				}
			}
		}
	}
	for name, o := range rem {
		if _, ok := byName[name]; ok {
			continue
		}
		created, _ := time.ParseInLocation("2006-01-02_15-04-05", name[:19], time.Local)
		local = append(local, BackupInfo{Name: name, Kind: strings.TrimSuffix(name[20:], ".tar.gz"), Size: o.Size, Created: created, Remote: true})
	}
	sort.Slice(local, func(i, j int) bool { return local[i].Name > local[j].Name })
	return local, remoteErr, nil
}

// fetchRemote downloads an external-only backup into the local backup folder.
func (m *Manager) fetchRemote(ctx context.Context, id, name string) (string, error) {
	if !backupName.MatchString(name) {
		return "", errors.New("Ungültiger Backup-Name")
	}
	t, _ := m.target()
	if t == nil {
		return "", errors.New("Backup nicht gefunden")
	}
	if err := os.MkdirAll(m.backupDir(id), 0o750); err != nil {
		return "", err
	}
	dst := filepath.Join(m.backupDir(id), name)
	tmp := dst + ".part"
	f, err := os.OpenFile(tmp, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return "", err
	}
	err = t.Get(ctx, id+"/"+name, f)
	f.Close()
	if err != nil {
		os.Remove(tmp)
		return "", fmt.Errorf("Download vom externen Speicher: %w", err)
	}
	return dst, os.Rename(tmp, dst)
}

// StreamRemote writes an external backup to w (for browser downloads).
func (m *Manager) StreamRemote(ctx context.Context, id, name string, w io.Writer) error {
	if !backupName.MatchString(name) {
		return errors.New("Ungültiger Backup-Name")
	}
	t, _ := m.target()
	if t == nil {
		return errors.New("Backup nicht gefunden")
	}
	return t.Get(ctx, id+"/"+name, w)
}

// DeleteBackupEverywhere removes the local file and the external copy.
func (m *Manager) DeleteBackupEverywhere(ctx context.Context, id, name string) error {
	if !backupName.MatchString(name) {
		return errors.New("Ungültiger Backup-Name")
	}
	if p, err := m.BackupFile(id, name); err == nil {
		if err := os.Remove(p); err != nil {
			return err
		}
	}
	if t, _ := m.target(); t != nil {
		if err := t.Delete(ctx, id+"/"+name); err != nil {
			return fmt.Errorf("Lokal gelöscht, extern nicht: %w", err)
		}
	}
	return nil
}
