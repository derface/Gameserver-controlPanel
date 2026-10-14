package manager

import (
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
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/ai"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// Steam Workshop for every game: the panel finds the game's Steam app (the
// Workshop belongs to the game, not to the dedicated server), checks Steam's
// store data for the Workshop category and downloads items with SteamCMD
// before the start (see gp-run). How a game loads them is known for some
// games (templates.workshopRecipes); for the rest the AI can suggest a way.

type WorkshopInfo struct {
	Available bool   `json:"available"`
	App       int    `json:"app,omitempty"`
	Game      string `json:"game,omitempty"`
	Mode      string `json:"mode,omitempty"` // zomboid | steamcmd
	Recipe    bool   `json:"recipe"`         // the panel knows how the game loads mods
	Note      string `json:"note,omitempty"`
	Build42   bool   `json:"build42_filter,omitempty"`
	Login     string `json:"login"` // anonym | konto
	Reason    string `json:"reason,omitempty"`
}

type gameAppInfo struct {
	App      int       `json:"app"`
	Name     string    `json:"name"`
	Workshop bool      `json:"workshop"`
	Checked  time.Time `json:"checked"`
}

var (
	steamStoreSearch  = "https://store.steampowered.com/api/storesearch/"
	steamStoreDetails = "https://store.steampowered.com/api/appdetails"
	reSteamAppID      = regexp.MustCompile(`SteamAppId="?(\d{3,})`)
	gameAppMu         sync.Mutex
)

func (m *Manager) gameAppFile() string { return filepath.Join(m.dataDir, "steam-apps.json") }

func (m *Manager) loadGameApps() map[string]gameAppInfo {
	out := map[string]gameAppInfo{}
	if b, err := os.ReadFile(m.gameAppFile()); err == nil {
		_ = json.Unmarshal(b, &out)
	}
	return out
}

func getJSON(ctx context.Context, u string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	resp, err := wsClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	return json.NewDecoder(resp.Body).Decode(v)
}

// appHasWorkshop asks the store for the "Steam Workshop" category (id 30).
func appHasWorkshop(ctx context.Context, app int) (name string, ws bool, err error) {
	var r map[string]struct {
		Success bool `json:"success"`
		Data    struct {
			Name       string `json:"name"`
			Categories []struct {
				ID int `json:"id"`
			} `json:"categories"`
		} `json:"data"`
	}
	if err := getJSON(ctx, steamStoreDetails+"?appids="+strconv.Itoa(app)+"&l=german", &r); err != nil {
		return "", false, err
	}
	d, ok := r[strconv.Itoa(app)]
	if !ok || !d.Success {
		return "", false, nil
	}
	for _, c := range d.Data.Categories {
		if c.ID == 30 {
			return d.Data.Name, true, nil
		}
	}
	return d.Data.Name, false, nil
}

var reNameNoise = regexp.MustCompile(`(?i)\s*\((legacy|linux wine|beta)\)|\s+dedicated server`)

// gameApp finds the Steam app of the game a template runs.
func (m *Manager) gameApp(ctx context.Context, tpl templates.Template) gameAppInfo {
	if tpl.Workshop != nil && tpl.Workshop.App > 0 {
		return gameAppInfo{App: tpl.Workshop.App, Name: tpl.Name, Workshop: true}
	}
	if tpl.Kind != "steam" || tpl.SteamAppID == 0 {
		return gameAppInfo{}
	}
	gameAppMu.Lock()
	cache := m.loadGameApps()
	gameAppMu.Unlock()
	if c, ok := cache[tpl.ID]; ok && time.Since(c.Checked) < 30*24*time.Hour {
		return c
	}
	var cands []int
	seen := map[int]bool{}
	addC := func(a int) {
		if a > 0 && !seen[a] {
			seen[a] = true
			cands = append(cands, a)
		}
	}
	if mt := reSteamAppID.FindStringSubmatch(tpl.Start); mt != nil {
		a, _ := strconv.Atoi(mt[1])
		addC(a)
	}
	for _, e := range tpl.Env {
		if a, err := strconv.Atoi(e); err == nil && a > 1000 {
			addC(a)
		}
	}
	term := strings.TrimSpace(reNameNoise.ReplaceAllString(tpl.Name, ""))
	var sr struct {
		Items []struct {
			ID   int    `json:"id"`
			Name string `json:"name"`
			Type string `json:"type"`
		} `json:"items"`
	}
	failed := false
	if err := getJSON(ctx, steamStoreSearch+"?term="+url.QueryEscape(term)+"&l=english&cc=US", &sr); err == nil {
		want := normKey(term)
		for _, it := range sr.Items {
			if it.Type == "app" && normKey(it.Name) == want {
				addC(it.ID)
			}
		}
		for _, it := range sr.Items {
			if it.Type == "app" && !strings.Contains(strings.ToLower(it.Name), "dedicated") && strings.HasPrefix(normKey(it.Name), want) {
				addC(it.ID)
			}
		}
	} else {
		failed = true
	}
	res := gameAppInfo{Checked: time.Now()}
	for i, a := range cands {
		if i >= 4 {
			break
		}
		name, ws, err := appHasWorkshop(ctx, a)
		if err != nil {
			failed = true
			continue
		}
		if res.App == 0 && name != "" {
			res.App, res.Name = a, name
		}
		if ws {
			res = gameAppInfo{App: a, Name: name, Workshop: true, Checked: time.Now()}
			break
		}
	}
	if failed && !res.Workshop {
		return res // do not remember an answer we could not check
	}
	gameAppMu.Lock()
	cache = m.loadGameApps()
	cache[tpl.ID] = res
	if b, err := json.MarshalIndent(cache, "", " "); err == nil {
		_ = os.WriteFile(m.gameAppFile(), b, 0o600)
	}
	gameAppMu.Unlock()
	return res
}

// WorkshopInfoFor says whether and how the Workshop works for a server.
func (m *Manager) WorkshopInfoFor(ctx context.Context, id string) (WorkshopInfo, error) {
	_, tpl, err := m.get(id)
	if err != nil {
		return WorkshopInfo{}, err
	}
	in := WorkshopInfo{Login: "anonym"}
	if tpl.SteamLogin || m.steamUser() != "" {
		in.Login = "konto"
	}
	g := m.gameApp(ctx, tpl)
	if !g.Workshop {
		in.Reason = "Für dieses Spiel gibt es keinen Steam Workshop (oder Steam war nicht erreichbar)."
		return in, nil
	}
	in.Available, in.App, in.Game, in.Mode = true, g.App, g.Name, "steamcmd"
	if w := tpl.Workshop; w != nil {
		if w.Mode != "" {
			in.Mode = w.Mode
		}
		in.Recipe = w.Mode == "zomboid" || w.After != ""
		in.Note, in.Build42 = w.Note, w.Build42Tag != ""
	}
	if !in.Recipe {
		in.Note = "Das Panel lädt die Mods vor dem Start über SteamCMD herunter. Wie " + firstNonEmpty(g.Name, tpl.Name) +
			" sie einbindet, ist im Panel noch nicht hinterlegt: Bei jeder Mod hilft „KI fragen“ weiter."
	}
	return in, nil
}

// ---------- download list ----------

func (m *Manager) wsListFile(id string) string { return filepath.Join(m.ServerDir(id), ".gp-workshop") }

func (m *Manager) readWsList(id string) (int, []string) {
	b, err := os.ReadFile(m.wsListFile(id))
	if err != nil {
		return 0, nil
	}
	app := 0
	var ids []string
	for _, l := range strings.Split(string(b), "\n") {
		l = strings.TrimSpace(l)
		if strings.HasPrefix(l, "app ") {
			app, _ = strconv.Atoi(strings.TrimSpace(l[4:]))
		} else if reWsDigits.MatchString(l) {
			ids = append(ids, l)
		}
	}
	return app, ids
}

func (m *Manager) writeWsList(sv store.Server, tpl templates.Template, app int, ids []string) error {
	dir := m.ServerDir(sv.ID)
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "app %d\n", app)
	for _, x := range ids {
		b.WriteString(x + "\n")
	}
	p := m.wsListFile(sv.ID)
	if err := os.WriteFile(p, []byte(b.String()), 0o644); err != nil {
		return err
	}
	uid := containerUID
	if tpl.DataUID > 0 {
		uid = tpl.DataUID
	}
	_ = os.Chown(p, uid, uid)
	return m.writeWorkshopHook(sv, tpl)
}

// writeWorkshopHook keeps /data/.gp-workshop-after.sh in line with the
// template (also called before every start).
func (m *Manager) writeWorkshopHook(sv store.Server, tpl templates.Template) error {
	p := filepath.Join(m.ServerDir(sv.ID), ".gp-workshop-after.sh")
	if tpl.Workshop == nil || tpl.Workshop.After == "" {
		_ = os.Remove(p)
		return nil
	}
	if b, err := os.ReadFile(p); err == nil && string(b) == tpl.Workshop.After {
		return nil
	}
	if _, err := os.Stat(m.ServerDir(sv.ID)); err != nil {
		return nil
	}
	return os.WriteFile(p, []byte(tpl.Workshop.After), 0o644)
}

// wsStatus reads what gp-run reported per item after the last download.
func (m *Manager) wsStatus(id string) map[string]string {
	out := map[string]string{}
	b, err := os.ReadFile(filepath.Join(m.ServerDir(id), ".gp-workshop-status"))
	if err != nil {
		return out
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.SplitN(strings.TrimSpace(l), " ", 2)
		if len(f) == 2 {
			out[f[0]] = f[1]
		}
	}
	return out
}

func (m *Manager) steamcmdInstall(ctx context.Context, id, wid string, add bool) (string, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return "", err
	}
	info, err := m.WorkshopInfoFor(ctx, id)
	if err != nil {
		return "", err
	}
	if !info.Available {
		return "", errors.New(info.Reason)
	}
	_, ids := m.readWsList(id)
	var out []string
	have := false
	for _, x := range ids {
		if x == wid {
			have = true
			if !add {
				continue
			}
		}
		out = append(out, x)
	}
	if add && !have {
		out = append(out, wid)
	}
	if err := m.writeWsList(sv, tpl, info.App, out); err != nil {
		return "", err
	}
	if !add {
		return "Aus der Liste genommen. Ab dem nächsten Start wird die Mod nicht mehr geladen; die Dateien bleiben liegen.", nil
	}
	msg := "Vorgemerkt. Das Panel lädt die Mod beim nächsten Start über SteamCMD herunter"
	if info.Recipe {
		msg += " und bindet sie ein."
	} else {
		msg += ". Wie das Spiel sie lädt, ist noch nicht hinterlegt – in der Liste hilft „KI fragen“."
	}
	if info.Login == "anonym" {
		msg += " Verlangt Steam dafür ein Konto, steht das danach in der Liste."
	}
	return msg, nil
}

// wsMode returns "zomboid" for games that download themselves.
func wsMode(tpl templates.Template) string {
	if tpl.Workshop != nil && tpl.Workshop.Mode != "" {
		return tpl.Workshop.Mode
	}
	return "steamcmd"
}

// WorkshopAdvice asks the AI how the game loads a downloaded item.
func (m *Manager) WorkshopAdvice(ctx context.Context, id, wid string) (ai.ModAdvice, error) {
	if !reWsDigits.MatchString(wid) {
		return ai.ModAdvice{}, errors.New("Ungültige Workshop-ID")
	}
	s := m.aiSettings()
	if s.AIProvider == "" {
		return ai.ModAdvice{}, errors.New("Kein KI-Anbieter eingerichtet. Bitte unter Einstellungen → Allgemein & KI auswählen")
	}
	_, tpl, err := m.get(id)
	if err != nil {
		return ai.ModAdvice{}, err
	}
	dir := m.ServerDir(id)
	var item string
	for _, content := range workshopDirs(dir) {
		ms, _ := filepath.Glob(filepath.Join(content, "*", wid))
		if len(ms) > 0 {
			item = ms[0]
			break
		}
	}
	if item == "" {
		return ai.ModAdvice{}, errors.New("Die Mod ist noch nicht heruntergeladen. Starte den Server einmal, dann kann die KI die Dateien ansehen")
	}
	in := ai.ModAdviceInput{Game: tpl.Name, Template: tpl.ID, Start: tpl.Start, ModTitle: wid}
	if rel, err := filepath.Rel(dir, item); err == nil {
		in.ItemDir = "/data/" + filepath.ToSlash(rel)
	}
	if len(in.Start) > 600 {
		in.Start = in.Start[:600] + "…"
	}
	if det, err := workshopDetails(ctx, []string{wid}); err == nil && len(det) == 1 {
		in.ModTitle, in.ModDesc = det[0].Title, det[0].Description
	}
	_ = filepath.WalkDir(item, func(p string, d fs.DirEntry, err error) error {
		if err != nil || p == item {
			return nil
		}
		if len(in.Files) >= 80 {
			return fs.SkipAll
		}
		rel, _ := filepath.Rel(item, p)
		if d.IsDir() {
			if strings.Count(rel, string(os.PathSeparator)) >= 3 {
				return fs.SkipDir
			}
			rel += "/"
		}
		in.Files = append(in.Files, filepath.ToSlash(rel))
		return nil
	})
	if ents, err := os.ReadDir(filepath.Join(dir, "server")); err == nil {
		for _, e := range ents {
			if len(in.ServerFiles) >= 40 {
				break
			}
			n := e.Name()
			if e.IsDir() {
				n += "/"
			}
			in.ServerFiles = append(in.ServerFiles, n)
		}
	}
	return ai.ModAdvise(ctx, s, in)
}
