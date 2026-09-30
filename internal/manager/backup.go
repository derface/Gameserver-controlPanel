package manager

import (
	"archive/tar"
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// Backup kinds, part of the file name.
const (
	KindManual  = "manuell"
	KindAuto    = "auto"
	KindRestore = "vor-wiederherstellung"
)

var backupName = regexp.MustCompile(`^\d{4}-\d{2}-\d{2}_\d{2}-\d{2}-\d{2}_(manuell|auto|vor-wiederherstellung)\.tar\.gz$`)

type BackupInfo struct {
	Name    string    `json:"name"`
	Kind    string    `json:"kind"`
	Size    int64     `json:"size"`
	Created time.Time `json:"created"`
}

func (m *Manager) backupDir(id string) string { return filepath.Join(m.dataDir, "backups", id) }

// BackupScope lists the paths (relative to the server folder) a backup contains.
func (m *Manager) BackupScope(id string) ([]string, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return nil, err
	}
	return scopeRoots(m.ServerDir(sv.ID), tpl)
}

// scopeRoots returns the top-level entries that are not excluded plus the
// explicit includes that exist.
func scopeRoots(dir string, tpl templates.Template) ([]string, error) {
	ex := map[string]bool{}
	for _, e := range tpl.Excludes() {
		ex[filepath.Clean(e)] = true
	}
	entries, err := os.ReadDir(dir)
	if err != nil && !errors.Is(err, fs.ErrNotExist) {
		return nil, err
	}
	var roots []string
	for _, e := range entries {
		if !ex[e.Name()] {
			roots = append(roots, e.Name())
		}
	}
	for _, inc := range tpl.BackupInclude {
		inc = filepath.Clean(inc)
		if strings.HasPrefix(inc, "..") || filepath.IsAbs(inc) {
			continue
		}
		if _, err := os.Lstat(filepath.Join(dir, inc)); err == nil {
			roots = append(roots, inc)
		}
	}
	sort.Strings(roots)
	return roots, nil
}

func (m *Manager) ListBackups(id string) ([]BackupInfo, error) {
	entries, err := os.ReadDir(m.backupDir(id))
	if errors.Is(err, fs.ErrNotExist) {
		return []BackupInfo{}, nil
	}
	if err != nil {
		return nil, err
	}
	out := []BackupInfo{}
	for _, e := range entries {
		if !backupName.MatchString(e.Name()) {
			continue
		}
		fi, err := e.Info()
		if err != nil {
			continue
		}
		created, _ := time.ParseInLocation("2006-01-02_15-04-05", e.Name()[:19], time.Local)
		kind := strings.TrimSuffix(e.Name()[20:], ".tar.gz")
		out = append(out, BackupInfo{Name: e.Name(), Kind: kind, Size: fi.Size(), Created: created})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name > out[j].Name })
	return out, nil
}

// BackupFile returns the absolute path of a backup after validating its name.
func (m *Manager) BackupFile(id, name string) (string, error) {
	if _, ok := m.st.Server(id); !ok {
		return "", errors.New("Server nicht gefunden")
	}
	if !backupName.MatchString(name) {
		return "", errors.New("Ungültiger Backup-Name")
	}
	p := filepath.Join(m.backupDir(id), name)
	if _, err := os.Stat(p); err != nil {
		return "", errors.New("Backup nicht gefunden")
	}
	return p, nil
}

func (m *Manager) DeleteBackup(id, name string) error {
	p, err := m.BackupFile(id, name)
	if err != nil {
		return err
	}
	return os.Remove(p)
}

// CreateBackup writes a new backup. It takes the server lock.
func (m *Manager) CreateBackup(ctx context.Context, id, kind string) (BackupInfo, error) {
	unlock := m.lock(id)
	defer unlock()
	return m.createBackup(ctx, id, kind)
}

func (m *Manager) isRunning(ctx context.Context, sv store.Server) bool {
	if sv.ContainerID == "" {
		return false
	}
	st, err := m.dk.Inspect(ctx, sv.ContainerID)
	return err == nil && st.Running
}

// createBackup expects the caller to hold the server lock.
func (m *Manager) createBackup(ctx context.Context, id, kind string) (BackupInfo, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return BackupInfo{}, err
	}
	dir := m.ServerDir(id)
	roots, err := scopeRoots(dir, tpl)
	if err != nil {
		return BackupInfo{}, err
	}
	if len(roots) == 0 {
		return BackupInfo{}, errors.New("Es gibt noch nichts zu sichern. Starte den Server einmal, damit Spieldaten entstehen")
	}
	running := m.isRunning(ctx, sv)
	if running && len(tpl.BackupPre) > 0 {
		for _, c := range tpl.BackupPre {
			_, _ = m.Command(ctx, id, c)
		}
		time.Sleep(2 * time.Second)
		defer func() {
			for _, c := range tpl.BackupPost {
				_, _ = m.Command(context.Background(), id, c)
			}
		}()
	}
	bdir := m.backupDir(id)
	if err := os.MkdirAll(bdir, 0o750); err != nil {
		return BackupInfo{}, err
	}
	now := time.Now()
	name := now.Format("2006-01-02_15-04-05") + "_" + kind + ".tar.gz"
	for i := 0; i < 60; i++ { // zwei Backups in derselben Sekunde
		if _, err := os.Stat(filepath.Join(bdir, name)); err != nil {
			break
		}
		now = now.Add(time.Second)
		name = now.Format("2006-01-02_15-04-05") + "_" + kind + ".tar.gz"
	}
	final := filepath.Join(bdir, name)
	tmp := final + ".part"
	if err := writeArchive(ctx, tmp, dir, roots); err != nil {
		os.Remove(tmp)
		return BackupInfo{}, fmt.Errorf("Backup fehlgeschlagen: %w", err)
	}
	if err := os.Rename(tmp, final); err != nil {
		return BackupInfo{}, err
	}
	fi, _ := os.Stat(final)
	m.prune(id)
	return BackupInfo{Name: name, Kind: kind, Size: fi.Size(), Created: now}, nil
}

func writeArchive(ctx context.Context, dst, dir string, roots []string) error {
	f, err := os.OpenFile(dst, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o600)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, _ := gzip.NewWriterLevel(f, gzip.BestSpeed)
	tw := tar.NewWriter(gz)
	seen := map[string]bool{}
	for _, root := range roots {
		err := filepath.WalkDir(filepath.Join(dir, root), func(p string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if ctx.Err() != nil {
				return ctx.Err()
			}
			rel, _ := filepath.Rel(dir, p)
			if seen[rel] {
				return nil
			}
			seen[rel] = true
			fi, err := d.Info()
			if err != nil {
				return err
			}
			if !fi.Mode().IsRegular() && !fi.IsDir() {
				return nil // Symlinks, Sockets usw. werden übersprungen
			}
			hdr, err := tar.FileInfoHeader(fi, "")
			if err != nil {
				return err
			}
			hdr.Name = filepath.ToSlash(rel)
			if fi.IsDir() {
				hdr.Name += "/"
			}
			if err := tw.WriteHeader(hdr); err != nil {
				return err
			}
			if fi.Mode().IsRegular() {
				src, err := os.Open(p)
				if err != nil {
					return err
				}
				_, err = io.CopyN(tw, src, hdr.Size)
				src.Close()
				if err != nil {
					return err
				}
			}
			return nil
		})
		if err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	if err := gz.Close(); err != nil {
		return err
	}
	return f.Sync()
}

// prune removes old automatic and pre-restore backups.
func (m *Manager) prune(id string) {
	sv, ok := m.st.Server(id)
	if !ok {
		return
	}
	keep := map[string]int{KindAuto: sv.Backup.Keep, KindRestore: 3}
	if keep[KindAuto] <= 0 {
		keep[KindAuto] = 7
	}
	list, err := m.ListBackups(id)
	if err != nil {
		return
	}
	count := map[string]int{}
	for _, b := range list { // newest first
		lim, managed := keep[b.Kind]
		if !managed {
			continue
		}
		count[b.Kind]++
		if count[b.Kind] > lim {
			_ = os.Remove(filepath.Join(m.backupDir(id), b.Name))
		}
	}
}

// RestoreBackup runs in the background: safety backup, stop, replace, start.
func (m *Manager) RestoreBackup(id, name string) error {
	p, err := m.BackupFile(id, name)
	if err != nil {
		return err
	}
	m.setJob(id, "Backup wird wiederhergestellt")
	go func() {
		unlock := m.lock(id)
		defer unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		if err := m.restore(ctx, id, p); err != nil {
			m.failJob(id, fmt.Errorf("Wiederherstellung fehlgeschlagen: %w", err))
			return
		}
		m.refresh(id)
		m.clearJob(id)
	}()
	return nil
}

func (m *Manager) restore(ctx context.Context, id, archive string) error {
	sv, tpl, err := m.get(id)
	if err != nil {
		return err
	}
	dir := m.ServerDir(id)
	wasRunning := m.isRunning(ctx, sv)
	// Erst stoppen: viele Spiele (z. B. Valheim) speichern die Welt erst
	// beim Beenden. So enthält die Sicherung wirklich den letzten Stand.
	if wasRunning {
		m.setJob(id, "Server wird gestoppt")
		if err := m.dk.Stop(ctx, sv.ContainerID, tpl.StopTimeout); err != nil {
			return err
		}
	}
	m.setJob(id, "Sicherung des aktuellen Stands")
	if roots, _ := scopeRoots(dir, tpl); len(roots) > 0 {
		if _, err := m.createBackup(ctx, id, KindRestore); err != nil {
			if wasRunning {
				_ = m.dk.Start(ctx, sv.ContainerID)
			}
			return err
		}
	}
	m.setJob(id, "Dateien werden ersetzt")
	roots, err := scopeRoots(dir, tpl)
	if err != nil {
		return err
	}
	for _, r := range roots {
		if err := os.RemoveAll(filepath.Join(dir, r)); err != nil {
			return err
		}
	}
	if err := extractArchive(archive, dir); err != nil {
		return err
	}
	if wasRunning {
		m.setJob(id, "Server wird gestartet")
		return m.dk.Start(ctx, sv.ContainerID)
	}
	return nil
}

func extractArchive(archive, dir string) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	tr := tar.NewReader(gz)
	base := filepath.Clean(dir) + string(os.PathSeparator)
	for {
		hdr, err := tr.Next()
		if err == io.EOF {
			return nil
		}
		if err != nil {
			return err
		}
		target := filepath.Join(dir, filepath.FromSlash(hdr.Name))
		if !strings.HasPrefix(target+string(os.PathSeparator), base) || target == filepath.Clean(dir) {
			continue // Pfade außerhalb des Serverordners ignorieren
		}
		mode := os.FileMode(hdr.Mode).Perm()
		switch hdr.Typeflag {
		case tar.TypeDir:
			if err := os.MkdirAll(target, mode|0o700); err != nil {
				return err
			}
		case tar.TypeReg:
			if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
				return err
			}
			out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, mode)
			if err != nil {
				return err
			}
			_, err = io.Copy(out, tr)
			out.Close()
			if err != nil {
				return err
			}
		default:
			continue
		}
		_ = os.Lchown(target, hdr.Uid, hdr.Gid)
		_ = os.Chtimes(target, hdr.ModTime, hdr.ModTime)
	}
}

// SetBackupPlan stores the schedule of a server.
func (m *Manager) SetBackupPlan(id string, every, at, keep int) (store.Server, error) {
	sv, ok := m.st.Server(id)
	if !ok {
		return sv, errors.New("Server nicht gefunden")
	}
	switch every {
	case 0, 6, 12, 24:
	default:
		return sv, errors.New("Erlaubt sind aus, alle 6 oder 12 Stunden und täglich")
	}
	if at < 0 || at > 23 {
		return sv, errors.New("Die Uhrzeit muss zwischen 0 und 23 liegen")
	}
	if keep < 1 || keep > 100 {
		return sv, errors.New("Behalten: 1 bis 100 Backups")
	}
	sv.Backup.EveryHours, sv.Backup.AtHour, sv.Backup.Keep = every, at, keep
	return sv, m.st.SaveServer(sv)
}

// due reports whether an automatic backup should run now.
func due(p store.BackupPlan, now time.Time) bool {
	switch p.EveryHours {
	case 6, 12:
		return now.Sub(p.LastAuto) >= time.Duration(p.EveryHours)*time.Hour-time.Minute
	case 24:
		if now.Hour() != p.AtHour {
			return false
		}
		y1, m1, d1 := p.LastAuto.Date()
		y2, m2, d2 := now.Date()
		return y1 != y2 || m1 != m2 || d1 != d2
	}
	return false
}

// runSchedule checks all servers once a minute.
func (m *Manager) runSchedule(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		if !m.AutoBackups() {
			continue
		}
		now := time.Now()
		for _, sv := range m.st.Servers() {
			if !due(sv.Backup, now) || sv.ContainerID == "" {
				continue
			}
			sv.Backup.LastAuto = now // also on failure, so it does not retry every minute
			_ = m.st.SaveServer(sv)
			go func(id string) {
				bctx, cancel := context.WithTimeout(ctx, time.Hour)
				defer cancel()
				_, err := m.CreateBackup(bctx, id, KindAuto)
				if cur, ok := m.st.Server(id); ok {
					cur.Backup.LastError = ""
					if err != nil {
						cur.Backup.LastError = err.Error()
					}
					_ = m.st.SaveServer(cur)
				}
			}(sv.ID)
		}
	}
}
