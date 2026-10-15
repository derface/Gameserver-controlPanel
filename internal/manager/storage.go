package manager

import (
	"context"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
)

// Disk usage overview: what takes the space and what can go. Walking big
// game folders takes a while, so the numbers are computed in the
// background and cached.

type StorageServer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Game    string `json:"game"`
	Files   int64  `json:"files"`   // game files and world
	Backups int64  `json:"backups"` // local backups
	NBackup int    `json:"n_backups"`
}

type StorageView struct {
	Total    int64             `json:"total"`
	Free     int64             `json:"free"`
	Servers  []StorageServer   `json:"servers"`
	Docker   *docker.DiskUsage `json:"docker,omitempty"`
	DockerEr string            `json:"docker_error,omitempty"`
	Panel    int64             `json:"panel"` // history, stats, geo database, settings copies
	Orphans  []StorageServer   `json:"orphans"`
	At       time.Time         `json:"at"`
	Busy     bool              `json:"busy"`
}

var storageCache struct {
	sync.Mutex
	v    StorageView
	busy bool
}

func treeSize(dir string) (int64, int) {
	var n int64
	files := 0
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.Type().IsRegular() {
			if info, err := d.Info(); err == nil {
				n += info.Size()
				files++
			}
		}
		return nil
	})
	return n, files
}

func diskSpace(dir string) (total, free int64) {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return 0, 0
	}
	return int64(st.Blocks) * st.Bsize, int64(st.Bavail) * st.Bsize
}

// Storage returns the last numbers and starts a new count when they are
// older than 10 minutes (or refresh is set).
func (m *Manager) Storage(refresh bool) StorageView {
	storageCache.Lock()
	v, busy := storageCache.v, storageCache.busy
	stale := refresh || v.At.IsZero() || time.Since(v.At) > 10*time.Minute
	if stale && !busy {
		storageCache.busy = true
		go m.countStorage()
		busy = true
	}
	storageCache.Unlock()
	v.Total, v.Free = diskSpace(m.dataDir)
	v.Busy = busy
	if v.Servers == nil {
		v.Servers = []StorageServer{}
	}
	if v.Orphans == nil {
		v.Orphans = []StorageServer{}
	}
	return v
}

func (m *Manager) countStorage() {
	defer func() {
		storageCache.Lock()
		storageCache.busy = false
		storageCache.Unlock()
	}()
	v := StorageView{At: time.Now(), Servers: []StorageServer{}, Orphans: []StorageServer{}}
	known := map[string]bool{}
	for _, sv := range m.st.Servers() {
		known[sv.ID] = true
		tpl, _ := m.tpls.Get(sv.Template)
		s := StorageServer{ID: sv.ID, Name: sv.Name, Game: tpl.Name}
		s.Files, _ = treeSize(m.ServerDir(sv.ID))
		s.Backups, s.NBackup = treeSize(m.backupDir(sv.ID))
		v.Servers = append(v.Servers, s)
	}
	sort.Slice(v.Servers, func(i, j int) bool {
		return v.Servers[i].Files+v.Servers[i].Backups > v.Servers[j].Files+v.Servers[j].Backups
	})
	// folders of servers that no longer exist (deleted without wiping)
	for _, base := range []string{"servers", "backups"} {
		ents, _ := os.ReadDir(filepath.Join(m.dataDir, base))
		for _, e := range ents {
			if !e.IsDir() || known[e.Name()] {
				continue
			}
			n, _ := treeSize(filepath.Join(m.dataDir, base, e.Name()))
			o := StorageServer{ID: e.Name(), Name: e.Name()}
			if base == "servers" {
				o.Files = n
			} else {
				o.Backups = n
			}
			v.Orphans = append(v.Orphans, o)
		}
	}
	for _, d := range []string{"history", "stats", "geo", "settings-backup", "events"} {
		n, _ := treeSize(filepath.Join(m.dataDir, d))
		v.Panel += n
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	if du, err := m.dk.DiskUsage(ctx); err == nil {
		v.Docker = &du
	} else {
		v.DockerEr = err.Error()
	}
	storageCache.Lock()
	storageCache.v = v
	storageCache.Unlock()
}

// PruneDocker frees space Docker holds: "images" removes images no
// server uses (they are fetched again when needed), "cache" the build
// cache.
func (m *Manager) PruneDocker(ctx context.Context, what string) (int64, error) {
	var n int64
	var err error
	switch what {
	case "images":
		n, err = m.dk.PruneImages(ctx)
	case "cache":
		n, err = m.dk.PruneBuildCache(ctx)
	default:
		return 0, os.ErrInvalid
	}
	if err == nil {
		go m.countStorage()
	}
	return n, err
}

// DeleteOrphan removes the folder of a server that no longer exists.
func (m *Manager) DeleteOrphan(id, kind string) error {
	if _, ok := m.st.Server(id); ok || id == "" || filepath.Base(id) != id || id == "." || id == ".." {
		return os.ErrInvalid
	}
	base := "servers"
	if kind == "backups" {
		base = "backups"
	}
	err := os.RemoveAll(filepath.Join(m.dataDir, base, id))
	go m.countStorage()
	return err
}

// checkDisk warns once a day when less than 10 % (or 10 GB) is free.
func (m *Manager) checkDisk() {
	total, free := diskSpace(m.dataDir)
	if total == 0 {
		return
	}
	if float64(free)/float64(total) < 0.10 || free < 10<<30 {
		m.NotifyPanel("panel", "warn", "Speicherplatz wird knapp",
			"Auf dem Rechner sind nur noch "+gbStr(free)+" frei. Unter Einstellungen → Speicherplatz siehst du, was Platz belegt.")
	}
}

func gbStr(b int64) string {
	return strings.Replace(fmt.Sprintf("%.1f", float64(b)/(1<<30)), ".", ",", 1) + " GB"
}
