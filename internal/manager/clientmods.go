package manager

import (
	"archive/zip"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/mods"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// Client mods ("Mod-Paket") and mod updates. For Minecraft the jars are
// identified at Modrinth by their hash; BepInEx plugins (Valheim) and Steam
// Workshop items come from the mod inventory.

// ClientMod is a mod players need (or may want) on their own computer.
type ClientMod struct {
	Name    string `json:"name"`
	Version string `json:"version,omitempty"`
	URL     string `json:"url,omitempty"`
	Source  string `json:"source"`
	Need    string `json:"need,omitempty"` // pflicht | optional
	file    string // absolute path, for the zip
	zipName string
}

type ClientMods struct {
	Mods     []ClientMod `json:"mods"`
	Download bool        `json:"download"` // a zip with the files can be built
	Note     string      `json:"note,omitempty"`
	At       time.Time   `json:"at"`
}

// ModUpdate is a mod with a newer version.
type ModUpdate struct {
	Name   string `json:"name"`
	File   string `json:"file,omitempty"` // jar file name (Minecraft)
	Source string `json:"source"`
	From   string `json:"from,omitempty"`
	To     string `json:"to,omitempty"`
	URL    string `json:"url,omitempty"`
	Note   string `json:"note,omitempty"`
}

type ModUpdates struct {
	List  []ModUpdate `json:"list"`
	At    time.Time   `json:"at"`
	Error string      `json:"error,omitempty"`
}

type modCacheEntry struct {
	client  *ClientMods
	updates *ModUpdates
	told    string // update list we last notified about
}

var modCache = struct {
	sync.Mutex
	m      map[string]*modCacheEntry
	hashes map[string]string // path|size|mtime -> sha1
}{m: map[string]*modCacheEntry{}, hashes: map[string]string{}}

func cachedHash(p string) (string, error) {
	fi, err := os.Stat(p)
	if err != nil {
		return "", err
	}
	k := fmt.Sprintf("%s|%d|%d", p, fi.Size(), fi.ModTime().UnixNano())
	modCache.Lock()
	h, ok := modCache.hashes[k]
	modCache.Unlock()
	if ok {
		return h, nil
	}
	h, err = mods.SHA1(p)
	if err != nil {
		return "", err
	}
	modCache.Lock()
	if len(modCache.hashes) > 5000 {
		modCache.hashes = map[string]string{}
	}
	modCache.hashes[k] = h
	modCache.Unlock()
	return h, nil
}

func modEntry(id string) *modCacheEntry {
	modCache.Lock()
	defer modCache.Unlock()
	e := modCache.m[id]
	if e == nil {
		e = &modCacheEntry{}
		modCache.m[id] = e
	}
	return e
}

var reMCVersion = regexp.MustCompile(`Starting minecraft server version (\S+)`)

// mcTarget finds loader and Minecraft version of a server.
func (m *Manager) mcTarget(sv store.Server, tpl templates.Template) (mods.Target, bool) {
	if !isMinecraft(tpl) {
		return mods.Target{}, false
	}
	t, ok := mods.TargetFor(sv.Vars["type"], sv.Vars["version"])
	if !ok {
		return t, false
	}
	if t.GameVersion == "" {
		if b, err := os.ReadFile(filepath.Join(m.ServerDir(sv.ID), "logs", "latest.log")); err == nil {
			if mm := reMCVersion.FindSubmatch(b); mm != nil {
				t.GameVersion = string(mm[1])
			}
		}
	}
	return t, true
}

type jarInfo struct {
	path, hash string
	ver        mods.Version
	known      bool
}

// minecraftJars identifies the jars in mods/ (or plugins/) at Modrinth.
func (m *Manager) minecraftJars(ctx context.Context, sv store.Server, sub string) ([]jarInfo, map[string]mods.Project, error) {
	files, _ := filepath.Glob(filepath.Join(m.ServerDir(sv.ID), sub, "*.jar"))
	var jars []jarInfo
	var hashes []string
	for _, f := range files {
		h, err := cachedHash(f)
		if err != nil {
			continue
		}
		jars = append(jars, jarInfo{path: f, hash: h})
		hashes = append(hashes, h)
	}
	if len(jars) == 0 {
		return nil, nil, nil
	}
	vers, err := mods.Identify(ctx, hashes)
	if err != nil {
		return jars, nil, err
	}
	var pids []string
	seen := map[string]bool{}
	for i := range jars {
		if v, ok := vers[jars[i].hash]; ok {
			jars[i].ver, jars[i].known = v, true
			if !seen[v.ProjectID] {
				seen[v.ProjectID] = true
				pids = append(pids, v.ProjectID)
			}
		}
	}
	projects := map[string]mods.Project{}
	for i := 0; i < len(pids); i += 100 {
		end := min(len(pids), i+100)
		ps, err := mods.Lookup(ctx, pids[i:end])
		if err != nil {
			return jars, projects, err
		}
		for _, p := range ps {
			projects[p.ID] = p
		}
	}
	return jars, projects, nil
}

// ClientMods lists what players need, cached for 30 minutes.
func (m *Manager) ClientMods(ctx context.Context, id string) (ClientMods, error) {
	e := modEntry(id)
	modCache.Lock()
	if c := e.client; c != nil && time.Since(c.At) < 30*time.Minute {
		out := *c
		modCache.Unlock()
		return out, nil
	}
	modCache.Unlock()
	sv, tpl, err := m.get(id)
	if err != nil {
		return ClientMods{}, err
	}
	out := ClientMods{Mods: []ClientMod{}, At: time.Now()}
	dir := m.ServerDir(id)
	if t, ok := m.mcTarget(sv, tpl); ok && t.ProjectType == "mod" {
		jars, projects, err := m.minecraftJars(ctx, sv, "mods")
		for _, j := range jars {
			cm := ClientMod{Name: strings.TrimSuffix(filepath.Base(j.path), ".jar"), Source: "Minecraft-Mod", Need: "pflicht", file: j.path, zipName: "mods/" + filepath.Base(j.path)}
			if j.known {
				p := projects[j.ver.ProjectID]
				if p.ClientSide == "unsupported" {
					continue // only for the server
				}
				if p.Title != "" {
					cm.Name = p.Title
				}
				if p.ClientSide == "optional" {
					cm.Need = "optional"
				}
				cm.Version = j.ver.VersionNumber
				if p.Slug != "" {
					cm.URL = "https://modrinth.com/mod/" + p.Slug
				}
			}
			out.Mods = append(out.Mods, cm)
		}
		if len(jars) > 0 {
			out.Download = true
			out.Note = fmt.Sprintf("Minecraft %s mit %s: Lade den Loader für deine Version, dann kommen diese Dateien in den Ordner „mods“ deines Minecraft-Profils.", firstNonEmpty(t.GameVersion, ""), t.Loader)
			if err != nil {
				out.Note += " (Modrinth war nicht erreichbar, deshalb stehen nur Dateinamen da und Server-Mods sind womöglich mit dabei.)"
			}
		}
	}
	inv, ierr := m.ModList(ctx, id)
	if ierr == nil {
		bep := false
		for _, e := range inv.Mods {
			switch {
			case e.Source == "BepInEx" && (e.Enabled == nil || *e.Enabled):
				bep = true
				cm := ClientMod{Name: e.Name, Version: e.Version, URL: e.URL, Source: "BepInEx", Need: "pflicht", file: filepath.Join(dir, e.Path)}
				if i := strings.Index(e.Path, "BepInEx/"); i >= 0 {
					cm.zipName = e.Path[i:]
				} else {
					cm.zipName = "BepInEx/plugins/" + filepath.Base(e.Path)
				}
				out.Mods = append(out.Mods, cm)
			case e.Workshop != "" && (e.Enabled == nil || *e.Enabled) && e.State != "inaktiv":
				out.Mods = append(out.Mods, ClientMod{Name: e.Name, URL: e.URL, Source: "Steam Workshop", Need: "pflicht"})
			}
		}
		if bep {
			out.Download = true
			out.Note = "Zuerst BepInExPack für das Spiel installieren (z. B. über Thunderstore oder r2modman), dann den Inhalt des Mod-Pakets in den Spielordner entpacken."
		} else if len(out.Mods) > 0 && out.Note == "" {
			out.Note = "Abonniere die Mods im Steam Workshop. Viele Spiele laden fehlende Workshop-Mods beim Beitreten auch selbst."
		}
	}
	sort.SliceStable(out.Mods, func(i, j int) bool { return strings.ToLower(out.Mods[i].Name) < strings.ToLower(out.Mods[j].Name) })
	modCache.Lock()
	c := out
	e.client = &c
	modCache.Unlock()
	return out, nil
}

// WriteModPack streams the client mod files as a zip.
func (m *Manager) WriteModPack(ctx context.Context, id string, w io.Writer) error {
	cm, err := m.ClientMods(ctx, id)
	if err != nil {
		return err
	}
	if !cm.Download {
		return errors.New("Für diesen Server gibt es kein Mod-Paket")
	}
	zw := zip.NewWriter(w)
	add := func(src, name string) error {
		fi, err := os.Stat(src)
		if err != nil {
			return nil
		}
		if fi.IsDir() {
			return filepath.WalkDir(src, func(p string, d os.DirEntry, err error) error {
				if err != nil || d.IsDir() || !d.Type().IsRegular() {
					return nil
				}
				rel, _ := filepath.Rel(src, p)
				return addFile(zw, p, name+"/"+filepath.ToSlash(rel))
			})
		}
		return addFile(zw, src, name)
	}
	for _, c := range cm.Mods {
		if c.file == "" || c.zipName == "" {
			continue
		}
		if err := ctx.Err(); err != nil {
			return err
		}
		if err := add(c.file, c.zipName); err != nil {
			return err
		}
	}
	return zw.Close()
}

func addFile(zw *zip.Writer, src, name string) error {
	f, err := os.Open(src)
	if err != nil {
		return nil
	}
	defer f.Close()
	fi, _ := f.Stat()
	h, _ := zip.FileInfoHeader(fi)
	h.Name = name
	h.Method = zip.Store // jars and dlls are compressed already or tiny
	if !strings.HasSuffix(name, ".jar") && !strings.HasSuffix(name, ".zip") {
		h.Method = zip.Deflate
	}
	w, err := zw.CreateHeader(h)
	if err != nil {
		return err
	}
	_, err = io.Copy(w, f)
	return err
}

// ---------- updates ----------

// CheckModUpdates looks for newer mod versions (Workshop and Modrinth).
func (m *Manager) CheckModUpdates(ctx context.Context, id string) ModUpdates {
	sv, tpl, err := m.get(id)
	if err != nil {
		return ModUpdates{Error: err.Error()}
	}
	out := ModUpdates{List: []ModUpdate{}, At: time.Now()}
	var errs []string
	if t, ok := m.mcTarget(sv, tpl); ok {
		sub := "mods"
		if t.ProjectType == "plugin" {
			sub = "plugins"
		}
		jars, projects, err := m.minecraftJars(ctx, sv, sub)
		if err != nil {
			errs = append(errs, err.Error())
		}
		var hashes []string
		for _, j := range jars {
			if j.known {
				hashes = append(hashes, j.hash)
			}
		}
		latest, err := mods.Latest(ctx, hashes, t.Loader, t.GameVersion)
		if err != nil {
			errs = append(errs, err.Error())
		}
		for _, j := range jars {
			l, ok := latest[j.hash]
			if !j.known || !ok || l.ID == "" || l.ID == j.ver.ID {
				continue
			}
			p := projects[j.ver.ProjectID]
			u := ModUpdate{Name: firstNonEmpty(p.Title, filepath.Base(j.path)), File: filepath.Base(j.path), Source: "Modrinth", From: j.ver.VersionNumber, To: l.VersionNumber}
			if p.Slug != "" {
				u.URL = "https://modrinth.com/project/" + p.Slug
			}
			if m.inModList(sv, p) {
				u.Note = "Wird beim nächsten Neustart geladen."
			} else {
				u.Note = "Von Hand installiert: neue Datei selbst in den Ordner „" + sub + "“ legen."
			}
			out.List = append(out.List, u)
		}
	}
	if inv, err := m.ModList(ctx, id); err == nil {
		for _, e := range inv.Mods {
			if e.UpdateHint {
				out.List = append(out.List, ModUpdate{Name: e.Name, Source: "Steam Workshop", URL: e.URL, Note: "Der Server lädt die neue Version beim nächsten Start."})
			}
		}
	}
	if len(errs) > 0 {
		out.Error = errs[0]
	}
	en := modEntry(id)
	modCache.Lock()
	c := out
	en.updates = &c
	modCache.Unlock()
	return out
}

// inModList says whether the project is in the server's Modrinth list (the
// image downloads those itself on start).
func (m *Manager) inModList(sv store.Server, p mods.Project) bool {
	for _, s := range mods.ParseList(sv.Vars["mods"]) {
		if strings.EqualFold(s, p.Slug) || s == p.ID {
			return true
		}
	}
	return false
}

// PendingModUpdates returns the last check (nil if none yet).
func (m *Manager) PendingModUpdates(id string) *ModUpdates {
	modCache.Lock()
	defer modCache.Unlock()
	if e := modCache.m[id]; e != nil && e.updates != nil {
		c := *e.updates
		return &c
	}
	return nil
}

// runModUpdates checks every server with mods every 6 hours and reports
// new updates once.
func (m *Manager) runModUpdates(ctx context.Context) {
	t := time.NewTicker(20 * time.Minute)
	defer t.Stop()
	select {
	case <-ctx.Done():
		return
	case <-time.After(2 * time.Minute):
	}
	for {
		for _, sv := range m.st.Servers() {
			if sv.CreatedBy == testCreator {
				continue
			}
			if p := m.PendingModUpdates(sv.ID); p != nil && time.Since(p.At) < 6*time.Hour {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
			u := m.CheckModUpdates(cctx, sv.ID)
			cancel()
			m.tellModUpdates(sv.ID, u)
		}
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) tellModUpdates(id string, u ModUpdates) {
	if len(u.List) == 0 {
		return
	}
	var names []string
	for _, x := range u.List {
		names = append(names, x.Name+" "+x.To)
	}
	key := strings.Join(names, "|")
	e := modEntry(id)
	modCache.Lock()
	same := e.told == key
	e.told = key
	modCache.Unlock()
	if same {
		return
	}
	txt := fmt.Sprintf("%d Mod-Update%s verfügbar: %s", len(u.List), map[bool]string{true: "", false: "s"}[len(u.List) == 1], strings.Join(firstN(names, 5), ", "))
	if len(names) > 5 {
		txt += " …"
	}
	m.EventKind(id, "info", "update", txt)
}

func firstN(s []string, n int) []string {
	if len(s) > n {
		return s[:n]
	}
	return s
}

func isMinecraft(tpl templates.Template) bool {
	return strings.HasPrefix(tpl.ID, "minecraft-java") || strings.Contains(tpl.Image, "itzg/minecraft-server")
}

var reUnsafe = regexp.MustCompile(`[^A-Za-z0-9._-]+`)

// SafeFileName turns a server name into a file name.
func SafeFileName(s string) string {
	s = strings.Trim(reUnsafe.ReplaceAllString(s, "-"), "-")
	if s == "" {
		return "server"
	}
	return s
}

// ClientModsCached returns the cached list without waiting; when there is
// none yet it is built in the background.
func (m *Manager) ClientModsCached(id string) (ClientMods, bool) {
	e := modEntry(id)
	modCache.Lock()
	c := e.client
	modCache.Unlock()
	if c == nil || time.Since(c.At) > 30*time.Minute {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_, _ = m.ClientMods(ctx, id)
		}()
	}
	if c == nil {
		return ClientMods{}, false
	}
	return *c, true
}
