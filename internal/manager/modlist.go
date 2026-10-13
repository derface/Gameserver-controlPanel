package manager

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// The mod inventory shows what lies on disk (Steam Workshop items, Project
// Zomboid mods, BepInEx plugins, Unreal pak mods, UE4SS mods, Minecraft
// jars), what the game's own list switches on, and what the console says
// about it since the last start.

type ModEntry struct {
	Key        string    `json:"key"` // for switching on/off; empty = cannot switch
	Name       string    `json:"name"`
	ID         string    `json:"id,omitempty"`
	Version    string    `json:"version,omitempty"`
	Source     string    `json:"source"`
	Workshop   string    `json:"workshop,omitempty"`
	URL        string    `json:"url,omitempty"`
	Path       string    `json:"path,omitempty"`
	Size       int64     `json:"size,omitempty"`
	Modified   time.Time `json:"modified,omitempty"`
	Enabled    *bool     `json:"enabled,omitempty"`
	State      string    `json:"state"` // geladen | fehler | aktiv | inaktiv | fehlt | installiert | nicht_geladen
	Note       string    `json:"note,omitempty"`
	UpdateHint bool      `json:"update_hint,omitempty"`
	Advice     bool      `json:"advice,omitempty"` // downloaded, but the panel does not know how the game loads it
}

type ModInventory struct {
	Mods     []ModEntry `json:"mods"`
	Sources  []string   `json:"sources"`
	Running  bool       `json:"running"`
	LogSince time.Time  `json:"log_since,omitempty"`
	Notes    []string   `json:"notes,omitempty"`
}

func boolp(b bool) *bool { return &b }

func dirSize(p string) (int64, time.Time) {
	var n int64
	var mt time.Time
	_ = filepath.WalkDir(p, func(q string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if fi, e := d.Info(); e == nil {
			if !d.IsDir() {
				n += fi.Size()
			}
			if fi.ModTime().After(mt) {
				mt = fi.ModTime()
			}
		}
		return nil
	})
	return n, mt
}

func readKV(p string) map[string]string {
	out := map[string]string{}
	f, err := os.Open(p)
	if err != nil {
		return out
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		if k, v, ok := strings.Cut(sc.Text(), "="); ok {
			k = strings.ToLower(strings.TrimSpace(k))
			if _, dup := out[k]; !dup {
				out[k] = strings.TrimSpace(v)
			}
		}
	}
	return out
}

// workshopDirs finds every steamapps/workshop/content folder below the
// server folder (games put it next to the server or below their home).
func workshopDirs(dir string) []string {
	var out []string
	base := strings.Count(filepath.Clean(dir), string(os.PathSeparator))
	_ = filepath.WalkDir(dir, func(p string, d fs.DirEntry, err error) error {
		if err != nil || !d.IsDir() {
			return nil
		}
		if strings.Count(p, string(os.PathSeparator))-base > 6 {
			return fs.SkipDir
		}
		switch d.Name() {
		case ".wine", "Saves", "saves", "Logs", "logs", "jre64", "jre", "java", "media", "natives", "linux64", "linux32":
			return fs.SkipDir
		case "content":
			if filepath.Base(filepath.Dir(p)) == "workshop" {
				out = append(out, p)
				return fs.SkipDir
			}
		}
		return nil
	})
	return out
}

// pzMods reads the mod.info files below a folder (one per mod and build).
func pzMods(root string) []ModEntry {
	byID := map[string]ModEntry{}
	_ = filepath.WalkDir(root, func(p string, d fs.DirEntry, err error) error {
		if err != nil {
			return nil
		}
		if d.IsDir() && strings.Count(strings.TrimPrefix(p, root), string(os.PathSeparator)) > 5 {
			return fs.SkipDir
		}
		if d.IsDir() || d.Name() != "mod.info" {
			return nil
		}
		kv := readKV(p)
		id := kv["id"]
		if id == "" {
			return nil
		}
		dir := filepath.Dir(p)
		b42 := filepath.Base(dir) == "42" || strings.Contains(p, string(os.PathSeparator)+"42"+string(os.PathSeparator))
		if old, ok := byID[id]; ok && !b42 && old.Version != "" {
			return nil // keep the build 42 description
		}
		byID[id] = ModEntry{ID: id, Name: firstNonEmpty(kv["name"], id), Version: firstNonEmpty(kv["modversion"], kv["version"]), Path: dir}
		return nil
	})
	out := make([]ModEntry, 0, len(byID))
	for _, e := range byID {
		out = append(out, e)
	}
	return out
}

// ---------- Steam Workshop titles ----------

type wsInfo struct {
	Title   string
	Updated time.Time
}

var wsCache = struct {
	sync.Mutex
	m  map[string]wsInfo
	at map[string]time.Time
}{m: map[string]wsInfo{}, at: map[string]time.Time{}}

var (
	wsClient = &http.Client{Timeout: 8 * time.Second}
	wsURL    = "https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/"
)

// workshopInfo asks Steam for titles and update times (public API, no key).
func workshopInfo(ctx context.Context, ids []string) map[string]wsInfo {
	out := map[string]wsInfo{}
	var ask []string
	wsCache.Lock()
	for _, id := range ids {
		if t, ok := wsCache.at[id]; ok && time.Since(t) < 6*time.Hour {
			out[id] = wsCache.m[id]
		} else {
			ask = append(ask, id)
		}
	}
	wsCache.Unlock()
	if len(ask) == 0 {
		return out
	}
	form := url.Values{"itemcount": {strconv.Itoa(len(ask))}}
	for i, id := range ask {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", wsURL, strings.NewReader(form.Encode()))
	if err != nil {
		return out
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := wsClient.Do(req)
	if err != nil {
		return out
	}
	defer resp.Body.Close()
	var r struct {
		Response struct {
			Details []struct {
				ID      string `json:"publishedfileid"`
				Title   string `json:"title"`
				Updated int64  `json:"time_updated"`
			} `json:"publishedfiledetails"`
		} `json:"response"`
	}
	if json.NewDecoder(resp.Body).Decode(&r) != nil {
		return out
	}
	wsCache.Lock()
	for _, d := range r.Response.Details {
		w := wsInfo{Title: d.Title, Updated: time.Unix(d.Updated, 0)}
		wsCache.m[d.ID], wsCache.at[d.ID] = w, time.Now()
		out[d.ID] = w
	}
	wsCache.Unlock()
	return out
}

var reIDList = regexp.MustCompile(`^\d{5,}(?:[\s,;]+\d{5,})*$`)

// ---------- inventory ----------

// ModList collects the mods of a server.
func (m *Manager) ModList(ctx context.Context, id string) (ModInventory, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return ModInventory{}, err
	}
	dir := m.ServerDir(id)
	inv := ModInventory{Mods: []ModEntry{}, Sources: []string{}}
	src := map[string]bool{}
	add := func(e ModEntry) {
		if e.Path != "" {
			if r, err := filepath.Rel(dir, e.Path); err == nil {
				e.Path = filepath.ToSlash(r)
			}
		}
		src[e.Source] = true
		inv.Mods = append(inv.Mods, e)
	}

	// Steam Workshop folders
	var wsIDs []string
	wsEntries := map[string]int{} // workshop id -> index of a generic entry
	pzByID := map[string]bool{}
	for _, content := range workshopDirs(dir) {
		apps, _ := os.ReadDir(content)
		for _, app := range apps {
			items, _ := os.ReadDir(filepath.Join(content, app.Name()))
			for _, it := range items {
				if !it.IsDir() || !reWsDigits.MatchString(it.Name()) {
					continue
				}
				p := filepath.Join(content, app.Name(), it.Name())
				size, mt := dirSize(p)
				wsIDs = append(wsIDs, it.Name())
				if mods := pzMods(p); len(mods) > 0 {
					for _, e := range mods {
						e.Source, e.Workshop, e.Size, e.Modified = "Steam Workshop", it.Name(), size, mt
						e.Key = "pz:" + e.ID
						pzByID[e.ID] = true
						add(e)
					}
					continue
				}
				wsEntries[it.Name()] = len(inv.Mods)
				add(ModEntry{Name: it.Name(), ID: it.Name(), Workshop: it.Name(), Source: "Steam Workshop", Path: p, Size: size, Modified: mt, State: "installiert"})
			}
		}
	}
	// Project Zomboid: local mods
	for _, root := range []string{filepath.Join(dir, "home", "Zomboid", "mods"), filepath.Join(dir, "home", "Zomboid", "Workshop")} {
		for _, e := range pzMods(root) {
			if pzByID[e.ID] {
				continue
			}
			e.Source, e.Key = "Zomboid (lokal)", "pz:"+e.ID
			e.Size, e.Modified = dirSize(e.Path)
			pzByID[e.ID] = true
			add(e)
		}
	}
	// BepInEx plugins (Valheim and other Unity games)
	for _, root := range []string{filepath.Join(dir, "server", "BepInEx", "plugins"), filepath.Join(dir, "BepInEx", "plugins")} {
		ents, _ := os.ReadDir(root)
		for _, d := range ents {
			p := filepath.Join(root, d.Name())
			switch {
			case d.IsDir():
				var man struct {
					Name    string `json:"name"`
					Version string `json:"version_number"`
					URL     string `json:"website_url"`
				}
				if b, err := os.ReadFile(filepath.Join(p, "manifest.json")); err == nil {
					_ = json.Unmarshal(b, &man)
				}
				dlls, _ := filepath.Glob(filepath.Join(p, "*.dll"))
				off, _ := filepath.Glob(filepath.Join(p, "*.dll.off"))
				size, mt := dirSize(p)
				add(ModEntry{Key: "bep:" + d.Name(), Name: firstNonEmpty(man.Name, d.Name()), Version: man.Version, URL: man.URL, Source: "BepInEx",
					Path: p, Size: size, Modified: mt, Enabled: boolp(len(dlls) > 0 || len(off) == 0)})
			case strings.HasSuffix(d.Name(), ".dll") || strings.HasSuffix(d.Name(), ".dll.off"):
				fi, _ := d.Info()
				add(ModEntry{Key: "bep:" + d.Name(), Name: strings.TrimSuffix(strings.TrimSuffix(d.Name(), ".off"), ".dll"), Source: "BepInEx", Path: p,
					Size: fi.Size(), Modified: fi.ModTime(), Enabled: boolp(strings.HasSuffix(d.Name(), ".dll"))})
			}
		}
	}
	// Unreal pak mods and UE4SS
	paks, _ := filepath.Glob(filepath.Join(dir, "server", "*", "Content", "Paks", "~mods", "*.pak*"))
	logic, _ := filepath.Glob(filepath.Join(dir, "server", "*", "Content", "Paks", "LogicMods", "*.pak*"))
	for _, p := range append(paks, logic...) {
		if !strings.HasSuffix(p, ".pak") && !strings.HasSuffix(p, ".pak.off") {
			continue
		}
		fi, err := os.Stat(p)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(dir, p)
		add(ModEntry{Key: "pak:" + filepath.ToSlash(rel), Name: strings.TrimSuffix(strings.TrimSuffix(filepath.Base(p), ".off"), ".pak"), Source: "Pak-Mod",
			Path: p, Size: fi.Size(), Modified: fi.ModTime(), Enabled: boolp(strings.HasSuffix(p, ".pak"))})
	}
	ue, _ := filepath.Glob(filepath.Join(dir, "server", "*", "Binaries", "*", "ue4ss", "Mods", "mods.txt"))
	ue2, _ := filepath.Glob(filepath.Join(dir, "server", "*", "Binaries", "*", "Mods", "mods.txt"))
	for _, mt := range append(ue, ue2...) {
		b, err := os.ReadFile(mt)
		if err != nil {
			continue
		}
		rel, _ := filepath.Rel(dir, mt)
		for _, l := range strings.Split(string(b), "\n") {
			name, val, ok := strings.Cut(l, ":")
			name = strings.TrimSpace(name)
			if !ok || name == "" || strings.HasPrefix(name, ";") || strings.HasPrefix(name, "#") {
				continue
			}
			builtin := map[string]bool{"CheatManagerEnablerMod": true, "ActorDumperMod": true, "ConsoleCommandsMod": true, "ConsoleEnablerMod": true, "SplitScreenMod": true,
				"LineTraceMod": true, "BPML_GenericFunctions": true, "BPModLoaderMod": true, "Keybinds": true, "jsbLuaProfilerMod": true}
			if builtin[name] {
				continue
			}
			p := filepath.Join(filepath.Dir(mt), name)
			size, mod := dirSize(p)
			add(ModEntry{Key: "ue4ss:" + filepath.ToSlash(rel) + "#" + name, Name: name, Source: "UE4SS", Path: p, Size: size, Modified: mod,
				Enabled: boolp(strings.TrimSpace(val) == "1")})
		}
	}
	// Minecraft
	for _, sub := range []string{"mods", "plugins"} {
		jars, _ := filepath.Glob(filepath.Join(dir, sub, "*.jar"))
		for _, j := range jars {
			fi, err := os.Stat(j)
			if err != nil {
				continue
			}
			add(ModEntry{Name: strings.TrimSuffix(filepath.Base(j), ".jar"), Source: map[string]string{"mods": "Minecraft-Mod", "plugins": "Minecraft-Plugin"}[sub],
				Path: j, Size: fi.Size(), Modified: fi.ModTime(), State: "installiert"})
		}
	}

	// the game's own list: Project Zomboid Mods= / WorkshopItems=
	if tpl.ID == "zomboid" || tpl.SteamAppID == 380870 {
		if ini, ok := m.zomboidIni(sv, tpl); ok {
			kv := readKV(ini)
			active := map[string]bool{}
			for _, x := range splitSemi(kv["mods"]) {
				active[strings.TrimPrefix(x, `\`)] = true
			}
			for i := range inv.Mods {
				e := &inv.Mods[i]
				if strings.HasPrefix(e.Key, "pz:") {
					e.Enabled = boolp(active[e.ID])
				}
			}
			for idm := range active {
				if !pzByID[idm] {
					add(ModEntry{Key: "pz:" + idm, Name: idm, ID: idm, Source: "Zomboid-Liste", Enabled: boolp(true), State: "fehlt",
						Note: "In der Mod-Liste eingetragen, aber nicht auf der Platte. Fehlt die Workshop-ID unter WorkshopItems, oder lädt der Server sie beim nächsten Start?"})
				}
			}
			have := map[string]bool{}
			for _, w := range wsIDs {
				have[w] = true
			}
			for _, w := range splitSemi(kv["workshopitems"]) {
				if !have[w] {
					wsIDs = append(wsIDs, w)
					add(ModEntry{Name: w, ID: w, Workshop: w, Source: "Steam Workshop", State: "nicht_geladen",
						Note: "Unter WorkshopItems eingetragen, aber noch nicht heruntergeladen. Der Server lädt Workshop-Inhalte beim Start."})
				}
			}
		}
	}
	// the panel's own Workshop list (SteamCMD downloads)
	if wsMode(tpl) == "steamcmd" {
		if _, list := m.readWsList(id); list != nil || fileExists(m.wsListFile(id)) {
			st := m.wsStatus(id)
			recipe := tpl.Workshop != nil && tpl.Workshop.After != ""
			inList := map[string]bool{}
			for _, w := range list {
				inList[w] = true
			}
			for w, i := range wsEntries {
				e := &inv.Mods[i]
				e.Key, e.Enabled = "ws:"+w, boolp(inList[w])
				if inList[w] {
					e.State = "" // modStates decides: geladen, fehler or aktiv
				}
				if inList[w] && !recipe {
					e.Advice = true
					e.Note = "Heruntergeladen. Wie das Spiel die Mod lädt, ist im Panel nicht hinterlegt – „KI fragen“ macht einen Vorschlag."
				}
				if msg, ok := st[w]; ok && strings.HasPrefix(msg, "fehler") && inList[w] {
					e.Note = "Letzte Aktualisierung fehlgeschlagen: " + wsErrText(msg)
				}
			}
			for _, w := range list {
				if _, ok := wsEntries[w]; ok {
					continue
				}
				wsIDs = append(wsIDs, w)
				e := ModEntry{Key: "ws:" + w, Name: w, ID: w, Workshop: w, Source: "Steam Workshop", Enabled: boolp(true), State: "nicht_geladen",
					Note: "Vorgemerkt. Das Panel lädt die Mod beim nächsten Start über SteamCMD herunter."}
				if msg, ok := st[w]; ok && strings.HasPrefix(msg, "fehler") {
					e.State, e.Note = "fehler", "Download fehlgeschlagen: "+wsErrText(msg)
				}
				add(e)
			}
		}
	}
	// IDs in start values (e.g. CurseForge or Workshop IDs for ARK)
	for _, v := range tpl.Vars {
		val := strings.TrimSpace(sv.Vars[v.Key])
		if !strings.Contains(strings.ToLower(v.Key), "mod") || !reIDList.MatchString(val) {
			continue
		}
		for _, x := range splitList(val) {
			add(ModEntry{Name: x, ID: x, Source: "Startwert „" + v.Label + "“", State: "aktiv", Enabled: boolp(true)})
		}
	}

	// Steam titles and update hints
	if len(wsIDs) > 0 {
		info := workshopInfo(ctx, wsIDs)
		for i := range inv.Mods {
			e := &inv.Mods[i]
			w, ok := info[e.Workshop]
			if e.Workshop == "" || !ok {
				continue
			}
			if e.Name == e.Workshop && w.Title != "" {
				e.Name = w.Title
			}
			e.URL = "https://steamcommunity.com/sharedfiles/filedetails/?id=" + e.Workshop
			if !e.Modified.IsZero() && w.Updated.After(e.Modified.Add(time.Hour)) {
				e.UpdateHint = true
			}
		}
		for _, e := range inv.Mods {
			if e.URL == "" && e.Workshop != "" {
				inv.Notes = append(inv.Notes, "Steam war für die Namen der Workshop-Inhalte nicht erreichbar; es stehen nur die Nummern da.")
				break
			}
		}
		for i := range inv.Mods {
			if inv.Mods[i].Workshop != "" && inv.Mods[i].URL == "" {
				inv.Mods[i].URL = "https://steamcommunity.com/sharedfiles/filedetails/?id=" + inv.Mods[i].Workshop
			}
		}
	}

	m.modStates(ctx, sv, &inv)
	for k := range src {
		inv.Sources = append(inv.Sources, k)
	}
	sort.Strings(inv.Sources)
	order := map[string]int{"fehler": 0, "fehlt": 1, "nicht_geladen": 2, "geladen": 3, "aktiv": 4, "installiert": 5, "inaktiv": 6}
	sort.SliceStable(inv.Mods, func(i, j int) bool {
		a, b := inv.Mods[i], inv.Mods[j]
		if order[a.State] != order[b.State] {
			return order[a.State] < order[b.State]
		}
		return strings.ToLower(a.Name) < strings.ToLower(b.Name)
	})
	return inv, nil
}

func splitList(s string) []string {
	var out []string
	for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ';' || r == ',' || r == ' ' || r == '\t' }) {
		if x = strings.TrimSpace(x); x != "" && x != `\` {
			out = append(out, x)
		}
	}
	return out
}

// splitSemi splits Zomboid's lists; mod IDs may contain spaces.
func splitSemi(s string) []string {
	var out []string
	for _, x := range strings.Split(s, ";") {
		if x = strings.TrimSpace(x); x != "" && x != `\` {
			out = append(out, x)
		}
	}
	return out
}

// zomboidIni is the server's ini with Mods= and WorkshopItems=.
func (m *Manager) zomboidIni(sv store.Server, tpl templates.Template) (string, bool) {
	name := firstNonEmpty(sv.Vars["server_name"], "servertest")
	p := filepath.Join(m.ServerDir(sv.ID), "home", "Zomboid", "Server", name+".ini")
	if _, err := os.Stat(p); err == nil {
		return p, true
	}
	return "", false
}

var (
	reModErr  = regexp.MustCompile(`(?i)\b(error|exception|failed|failure|could not|cannot|missing|not found|invalid)\b`)
	reModLoad = regexp.MustCompile(`(?i)\b(load(ing|ed)?|activat\w*|initiali\w*|mount\w*|register\w*|enabl\w*|start\w*)\b`)
)

// modStates reads the console of the current (or last) run.
func (m *Manager) modStates(ctx context.Context, sv store.Server, inv *ModInventory) {
	m.mu.RLock()
	l := m.live[sv.ID]
	m.mu.RUnlock()
	var since int64
	if l != nil {
		inv.Running = l.State.Running
		if t, err := time.Parse(time.RFC3339Nano, l.State.StartedAt); err == nil && t.Year() > 2000 {
			since, inv.LogSince = t.Unix(), t
		}
	}
	var lines []string
	if since > 0 {
		if rc, err := m.logs(ctx, sv.ID, false, 20000, since); err == nil {
			sc := bufio.NewScanner(rc)
			sc.Buffer(make([]byte, 64*1024), 1<<20)
			for sc.Scan() {
				lines = append(lines, ansiRe.ReplaceAllString(sc.Text(), ""))
			}
			rc.Close()
		}
	}
	low := make([]string, len(lines))
	for i, x := range lines {
		low[i] = strings.ToLower(x)
	}
	for i := range inv.Mods {
		e := &inv.Mods[i]
		if e.State == "fehlt" || e.State == "nicht_geladen" || (e.State == "fehler" && e.Path == "") {
			continue
		}
		if e.Enabled != nil && !*e.Enabled {
			e.State = "inaktiv"
			continue
		}
		var toks []string
		for _, t := range []string{e.ID, e.Name, filepath.Base(e.Path)} {
			t = strings.ToLower(strings.TrimSpace(t))
			if len(t) >= 4 && t != "." {
				toks = append(toks, t)
			}
		}
		found, bad := false, ""
		for j, x := range low {
			hit := false
			for _, t := range toks {
				if strings.Contains(x, t) {
					hit = true
					break
				}
			}
			if !hit {
				continue
			}
			if reModErr.MatchString(x) {
				bad = strings.TrimSpace(lines[j])
			} else if reModLoad.MatchString(x) || strings.Contains(x, "mod") {
				found = true
			}
		}
		switch {
		case bad != "":
			e.State = "fehler"
			if len(bad) > 300 {
				bad = bad[:300] + "…"
			}
			e.Note = bad
		case found:
			e.State = "geladen"
		case e.State == "":
			if e.Enabled != nil {
				e.State = "aktiv"
			} else {
				e.State = "installiert"
			}
		}
	}
}

// ---------- switching ----------

// ToggleMod switches a mod on or off. Zomboid mods go through the settings
// (the server is stopped and started for that); files are renamed and take
// effect with the next start.
func (m *Manager) ToggleMod(ctx context.Context, id, key string, on bool) (string, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return "", err
	}
	dir := m.ServerDir(id)
	kind, rest, _ := strings.Cut(key, ":")
	inside := func(p string) (string, error) {
		full := filepath.Join(dir, filepath.FromSlash(p))
		if !strings.HasPrefix(full, filepath.Clean(dir)+string(os.PathSeparator)) {
			return "", errors.New("Ungültiger Pfad")
		}
		return full, nil
	}
	rename := func(from, to string) error {
		if _, err := os.Stat(from); err != nil {
			return errors.New("Datei nicht gefunden")
		}
		return os.Rename(from, to)
	}
	switch kind {
	case "pz":
		ini, ok := m.zomboidIni(sv, tpl)
		if !ok {
			return "", errors.New("Die Server-INI gibt es noch nicht. Starte den Server einmal")
		}
		kv := readKV(ini)
		list := splitSemi(kv["mods"])
		prefix := ""
		for _, x := range list {
			if strings.HasPrefix(x, `\`) {
				prefix = `\`
			}
		}
		var out []string
		has := false
		for _, x := range list {
			if strings.TrimPrefix(x, `\`) == rest {
				has = true
				if !on {
					continue
				}
			}
			out = append(out, x)
		}
		if on && !has {
			out = append(out, prefix+rest)
		}
		idx := -1
		for i, cf := range m.configFiles(sv, tpl) {
			if strings.HasSuffix(cf.c.File, ".ini") && strings.Contains(cf.c.File, "Zomboid/Server/") {
				idx = i
				break
			}
		}
		if idx < 0 {
			return "", errors.New("Die Server-INI fehlt in der Vorlage")
		}
		if _, err := m.ApplySettings(ctx, id, nil, map[int]map[string]string{idx: {"Mods": strings.Join(out, ";")}}); err != nil {
			return "", err
		}
		return "Mod-Liste gespeichert. Läuft der Server, wird er dafür neu gestartet.", nil
	case "ws":
		if !reWsDigits.MatchString(rest) {
			return "", errors.New("Ungültige Workshop-ID")
		}
		return m.steamcmdInstall(ctx, id, rest, on)
	case "bep":
		base := filepath.Join("server", "BepInEx", "plugins")
		if _, err := os.Stat(filepath.Join(dir, base)); err != nil {
			base = filepath.Join("BepInEx", "plugins")
		}
		p, err := inside(filepath.Join(base, filepath.Base(rest)))
		if err != nil {
			return "", err
		}
		if fi, err := os.Stat(p); err == nil && fi.IsDir() {
			pat := "*.dll.off"
			if !on {
				pat = "*.dll"
			}
			files, _ := filepath.Glob(filepath.Join(p, pat))
			for _, f := range files {
				to := strings.TrimSuffix(f, ".off")
				if !on {
					to = f + ".off"
				}
				if err := os.Rename(f, to); err != nil {
					return "", err
				}
			}
		} else {
			stem := strings.TrimSuffix(strings.TrimSuffix(p, ".off"), ".dll")
			if on {
				err = rename(stem+".dll.off", stem+".dll")
			} else {
				err = rename(stem+".dll", stem+".dll.off")
			}
			if err != nil {
				return "", err
			}
		}
	case "pak":
		p, err := inside(strings.TrimSuffix(rest, ".off"))
		if err != nil || !strings.HasSuffix(p, ".pak") {
			return "", errors.New("Ungültiger Pfad")
		}
		if on {
			err = rename(p+".off", p)
		} else {
			err = rename(p, p+".off")
		}
		if err != nil {
			return "", err
		}
	case "ue4ss":
		file, name, _ := strings.Cut(rest, "#")
		p, err := inside(file)
		if err != nil || filepath.Base(p) != "mods.txt" {
			return "", errors.New("Ungültiger Pfad")
		}
		b, err := os.ReadFile(p)
		if err != nil {
			return "", err
		}
		lines := strings.Split(string(b), "\n")
		found := false
		for i, l := range lines {
			if n, _, ok := strings.Cut(l, ":"); ok && strings.TrimSpace(n) == name {
				lines[i] = name + " : " + map[bool]string{true: "1", false: "0"}[on]
				found = true
			}
		}
		if !found {
			return "", errors.New("Mod nicht in mods.txt")
		}
		if err := os.WriteFile(p, []byte(strings.Join(lines, "\n")), 0o644); err != nil {
			return "", err
		}
	default:
		return "", errors.New("Diese Mod lässt sich hier nicht schalten")
	}
	return "Gespeichert. Wirkt beim nächsten Start des Servers.", nil
}

func fileExists(p string) bool {
	_, err := os.Stat(p)
	return err == nil
}

// wsErrText turns a status line ("fehler <steamcmd output>") into a hint.
func wsErrText(status string) string {
	msg := strings.TrimSpace(strings.TrimPrefix(status, "fehler"))
	low := strings.ToLower(msg)
	switch {
	case strings.Contains(low, "access denied") || strings.Contains(low, "no subscription") || strings.Contains(low, "not subscribed"):
		return "Steam gibt die Mod nur an ein Konto heraus, das das Spiel besitzt. Unter Einstellungen → Steam-Konto anmelden; beim nächsten Start versucht das Panel es dann mit dem Konto. (" + msg + ")"
	case strings.Contains(low, "timeout"):
		return "Zeitüberschreitung bei Steam; beim nächsten Start wird es erneut versucht. (" + msg + ")"
	case msg == "" || msg == "nicht geladen":
		return "SteamCMD hat die Mod nicht geliefert. Die Konsole des letzten Starts zeigt mehr."
	}
	return msg
}
