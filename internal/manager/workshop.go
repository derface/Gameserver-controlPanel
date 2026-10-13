package manager

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Steam Workshop: search and one-click install for games whose server
// downloads Workshop items itself (Project Zomboid: WorkshopItems= and
// Mods= in the server ini). Search uses the Steam Web API when a key is
// set, otherwise the public Workshop page; details always come from the
// public GetPublishedFileDetails call.

type WorkshopItem struct {
	ID          string    `json:"id"`
	Title       string    `json:"title"`
	Description string    `json:"description"`
	Preview     string    `json:"preview,omitempty"`
	Subs        int       `json:"subs"`
	Size        int64     `json:"size"`
	Updated     time.Time `json:"updated"`
	Tags        []string  `json:"tags"`
	ModIDs      []string  `json:"mod_ids"`
	Maps        []string  `json:"maps,omitempty"`
	Installed   bool      `json:"installed"`
}

type WorkshopPage struct {
	Items   []WorkshopItem `json:"items"`
	More    bool           `json:"more"`
	Build42 bool           `json:"build42"`
	Source  string         `json:"source"` // api | page
}

var (
	wsDetailsURL = "https://api.steampowered.com/ISteamRemoteStorage/GetPublishedFileDetails/v1/"
	wsQueryURL   = "https://api.steampowered.com/IPublishedFileService/QueryFiles/v1/"
	wsBrowseURL  = "https://steamcommunity.com/workshop/browse/"
	reWsLink     = regexp.MustCompile(`filedetails/\?id=(\d{6,})`)
	reModIDLine  = regexp.MustCompile(`(?im)^[\s*•\-\[\]b/]*mod ?ids?\s*[:=]\s*(.+)$`)
	reMapLine    = regexp.MustCompile(`(?im)^[\s*•\-\[\]b/]*map ?folders?\s*[:=]\s*(.+)$`)
	reBBCode     = regexp.MustCompile(`\[/?[a-zA-Z0-9*]+(=[^\]]*)?\]`)
	reWsDigits   = regexp.MustCompile(`^\d{6,}$`)
)

func cleanListLine(s string) []string {
	s = reBBCode.ReplaceAllString(s, "")
	var out []string
	for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ';' }) {
		x = strings.TrimSpace(strings.Trim(strings.TrimSpace(x), `"'`))
		if x != "" && len(x) < 80 && !strings.Contains(strings.ToLower(x), "http") {
			out = append(out, x)
		}
	}
	return out
}

// modIDsFrom reads "Mod ID: x" and "Map Folder: y" lines that Zomboid
// authors put into the description (the Workshop shows them by convention).
func modIDsFrom(desc string) (mods, maps []string) {
	seen := map[string]bool{}
	for _, m := range reModIDLine.FindAllStringSubmatch(reBBCode.ReplaceAllString(desc, "\n"), -1) {
		for _, x := range cleanListLine(m[1]) {
			if !seen[x] {
				seen[x] = true
				mods = append(mods, x)
			}
		}
	}
	for _, m := range reMapLine.FindAllStringSubmatch(reBBCode.ReplaceAllString(desc, "\n"), -1) {
		maps = append(maps, cleanListLine(m[1])...)
	}
	return
}

// workshopDetails fetches title, description and pictures (no key needed).
func workshopDetails(ctx context.Context, ids []string) ([]WorkshopItem, error) {
	form := url.Values{"itemcount": {strconv.Itoa(len(ids))}}
	for i, id := range ids {
		form.Set(fmt.Sprintf("publishedfileids[%d]", i), id)
	}
	req, err := http.NewRequestWithContext(ctx, "POST", wsDetailsURL, strings.NewReader(form.Encode()))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/x-www-form-urlencoded")
	resp, err := wsClient.Do(req)
	if err != nil {
		return nil, errors.New("Steam ist gerade nicht erreichbar")
	}
	defer resp.Body.Close()
	var r struct {
		Response struct {
			Details []struct {
				ID      string `json:"publishedfileid"`
				Result  int    `json:"result"`
				Title   string `json:"title"`
				Desc    string `json:"description"`
				Preview string `json:"preview_url"`
				Subs    int    `json:"lifetime_subscriptions"`
				Size    any    `json:"file_size"`
				Updated int64  `json:"time_updated"`
				Tags    []struct {
					Tag string `json:"tag"`
				} `json:"tags"`
			} `json:"publishedfiledetails"`
		} `json:"response"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
		return nil, errors.New("Antwort von Steam unlesbar")
	}
	var out []WorkshopItem
	for _, d := range r.Response.Details {
		if d.Result != 1 {
			continue
		}
		it := WorkshopItem{ID: d.ID, Title: d.Title, Preview: d.Preview, Subs: d.Subs, Updated: time.Unix(d.Updated, 0), Tags: []string{}}
		it.Size, _ = strconv.ParseInt(fmt.Sprint(d.Size), 10, 64)
		for _, t := range d.Tags {
			it.Tags = append(it.Tags, t.Tag)
		}
		it.ModIDs, it.Maps = modIDsFrom(d.Desc)
		if it.ModIDs == nil {
			it.ModIDs = []string{}
		}
		desc := strings.TrimSpace(reBBCode.ReplaceAllString(d.Desc, " "))
		desc = strings.Join(strings.Fields(desc), " ")
		if r := []rune(desc); len(r) > 260 {
			desc = string(r[:260]) + "…"
		}
		it.Description = desc
		out = append(out, it)
	}
	// keep the order of the request (search ranking)
	pos := map[string]int{}
	for i, id := range ids {
		pos[id] = i
	}
	sortBy(out, func(a, b WorkshopItem) bool { return pos[a.ID] < pos[b.ID] })
	return out, nil
}

func sortBy(s []WorkshopItem, less func(a, b WorkshopItem) bool) {
	for i := 1; i < len(s); i++ {
		for j := i; j > 0 && less(s[j], s[j-1]); j-- {
			s[j], s[j-1] = s[j-1], s[j]
		}
	}
}

// workshopSearchIDs returns the IDs of one result page.
func workshopSearchIDs(ctx context.Context, app int, q string, page int, tags []string, key string) ([]string, bool, string, error) {
	if key != "" {
		v := url.Values{"key": {key}, "appid": {strconv.Itoa(app)}, "numperpage": {"20"}, "page": {strconv.Itoa(page)},
			"return_details": {"false"}, "query_type": {"3"}} // trend
		if q != "" {
			v.Set("search_text", q)
			v.Set("query_type", "12") // relevance
		} else {
			v.Set("days", "90")
		}
		for i, t := range tags {
			v.Set(fmt.Sprintf("requiredtags[%d]", i), t)
		}
		req, _ := http.NewRequestWithContext(ctx, "GET", wsQueryURL+"?"+v.Encode(), nil)
		resp, err := wsClient.Do(req)
		if err != nil {
			return nil, false, "api", errors.New("Steam ist gerade nicht erreichbar")
		}
		defer resp.Body.Close()
		if resp.StatusCode == 403 || resp.StatusCode == 401 {
			return nil, false, "api", errors.New("Steam lehnt den Web-API-Schlüssel ab. Bitte unter Einstellungen → Steam-Konto prüfen")
		}
		var r struct {
			Response struct {
				Total   int `json:"total"`
				Details []struct {
					ID string `json:"publishedfileid"`
				} `json:"publishedfiledetails"`
			} `json:"response"`
		}
		if err := json.NewDecoder(resp.Body).Decode(&r); err != nil {
			return nil, false, "api", errors.New("Antwort von Steam unlesbar")
		}
		var ids []string
		for _, d := range r.Response.Details {
			ids = append(ids, d.ID)
		}
		return ids, page*20 < r.Response.Total, "api", nil
	}
	v := url.Values{"appid": {strconv.Itoa(app)}, "section": {"readytouseitems"}, "p": {strconv.Itoa(page)}}
	if q != "" {
		v.Set("searchtext", q)
		v.Set("browsesort", "textsearch")
	} else {
		v.Set("browsesort", "trend")
		v.Set("actualsort", "trend")
		v.Set("days", "90")
	}
	for _, t := range tags {
		v.Add("requiredtags[]", t)
	}
	req, _ := http.NewRequestWithContext(ctx, "GET", wsBrowseURL+"?"+v.Encode(), nil)
	req.Header.Set("Accept-Language", "de,en;q=0.8")
	resp, err := wsClient.Do(req)
	if err != nil {
		return nil, false, "page", errors.New("Steam ist gerade nicht erreichbar")
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	seen := map[string]bool{}
	var ids []string
	for _, m := range reWsLink.FindAllStringSubmatch(string(b), -1) {
		if !seen[m[1]] {
			seen[m[1]] = true
			ids = append(ids, m[1])
		}
	}
	if len(ids) > 30 {
		ids = ids[:30]
	}
	return ids, len(ids) >= 9 && strings.Contains(string(b), "pagebtn"), "page", nil
}

// zomboidLists reads the current Workshop and mod lists.
func (m *Manager) zomboidLists(id string) (ini string, items, mods, maps []string, b42 bool, err error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return
	}
	p, ok := m.zomboidIni(sv, tpl)
	if !ok {
		err = errors.New("Die Server-INI gibt es noch nicht. Starte den Server einmal, dann lassen sich Mods installieren")
		return
	}
	kv := readKV(p)
	items, mods = splitSemi(kv["workshopitems"]), splitSemi(kv["mods"])
	maps = splitSemi(kv["map"])
	for _, x := range mods {
		if strings.HasPrefix(x, `\`) {
			b42 = true
		}
	}
	return p, items, mods, maps, b42, nil
}

// WorkshopSearch lists Workshop items for the server's game.
func (m *Manager) WorkshopSearch(ctx context.Context, id, q string, page int, b42 *bool) (WorkshopPage, error) {
	_, tpl, err := m.get(id)
	if err != nil {
		return WorkshopPage{}, err
	}
	if tpl.Workshop == nil {
		return WorkshopPage{}, errors.New("Für dieses Spiel gibt es keine Workshop-Installation im Panel")
	}
	_, items, _, _, isB42, lerr := m.zomboidLists(id)
	if lerr != nil {
		isB42 = true // new servers run the current build
	}
	if b42 != nil {
		isB42 = *b42
	}
	var tags []string
	if isB42 && tpl.Workshop.Build42Tag != "" {
		tags = append(tags, tpl.Workshop.Build42Tag)
	}
	if page < 1 {
		page = 1
	}
	key := m.aiSettings().SteamAPIKey
	ids, more, src, err := workshopSearchIDs(ctx, tpl.Workshop.App, strings.TrimSpace(q), page, tags, key)
	if err != nil {
		return WorkshopPage{}, err
	}
	res := WorkshopPage{Items: []WorkshopItem{}, More: more, Build42: isB42, Source: src}
	if len(ids) == 0 {
		return res, nil
	}
	det, err := workshopDetails(ctx, ids)
	if err != nil {
		return res, err
	}
	have := map[string]bool{}
	for _, x := range items {
		have[x] = true
	}
	for i := range det {
		det[i].Installed = have[det[i].ID]
	}
	res.Items = det
	return res, nil
}

// localModIDs reads the mod IDs of a downloaded Workshop item.
func (m *Manager) localModIDs(id, wid string) []string {
	dir := m.ServerDir(id)
	for _, root := range []string{filepath.Join(dir, "server"), dir, filepath.Join(dir, "home"), filepath.Join(dir, "home", "Steam")} {
		p := filepath.Join(root, "steamapps", "workshop", "content", "108600", wid)
		if _, err := os.Stat(p); err == nil {
			var out []string
			for _, e := range pzMods(p) {
				out = append(out, e.ID)
			}
			return out
		}
	}
	return nil
}

// WorkshopInstall adds an item to WorkshopItems and its mods to Mods.
func (m *Manager) WorkshopInstall(ctx context.Context, id, wid string, modIDs []string) (string, error) {
	if !reWsDigits.MatchString(wid) {
		return "", errors.New("Ungültige Workshop-ID")
	}
	_, tpl, err := m.get(id)
	if err != nil {
		return "", err
	}
	if tpl.Workshop == nil {
		return "", errors.New("Für dieses Spiel gibt es keine Workshop-Installation im Panel")
	}
	_, items, mods, maps, b42, err := m.zomboidLists(id)
	if err != nil {
		return "", err
	}
	var newMaps []string
	if len(modIDs) == 0 {
		if det, err := workshopDetails(ctx, []string{wid}); err == nil && len(det) == 1 {
			modIDs, newMaps = det[0].ModIDs, det[0].Maps
		}
	}
	if local := m.localModIDs(id, wid); len(local) > 0 && len(modIDs) == 0 {
		modIDs = local
	}
	prefix := ""
	if b42 || len(mods) == 0 {
		prefix = `\`
	}
	has := func(list []string, x string) bool {
		for _, y := range list {
			if strings.TrimPrefix(y, `\`) == x {
				return true
			}
		}
		return false
	}
	if !has(items, wid) {
		items = append(items, wid)
	}
	for _, x := range modIDs {
		if strings.ContainsAny(x, ";=\n") {
			continue
		}
		if !has(mods, x) {
			mods = append(mods, prefix+x)
		}
	}
	vals := map[string]string{"WorkshopItems": strings.Join(items, ";"), "Mods": strings.Join(mods, ";")}
	if len(newMaps) > 0 {
		var mp []string
		for _, x := range newMaps {
			if !has(maps, x) {
				mp = append(mp, x)
			}
		}
		if len(mp) > 0 {
			vals["Map"] = strings.Join(append(mp, maps...), ";")
		}
	}
	if err := m.applyZomboidIni(ctx, id, vals); err != nil {
		return "", err
	}
	if len(modIDs) == 0 {
		return "Eingetragen. Die Mod-ID stand nicht in der Beschreibung: Nach dem nächsten Start taucht die Mod oben als „Aus“ auf, dort einschalten.", nil
	}
	return "Eingetragen: " + strings.Join(modIDs, ", ") + ". Der Server lädt die Mod beim nächsten Start herunter.", nil
}

// WorkshopRemove takes an item and its mods out of the lists (files stay).
func (m *Manager) WorkshopRemove(ctx context.Context, id, wid string) (string, error) {
	if !reWsDigits.MatchString(wid) {
		return "", errors.New("Ungültige Workshop-ID")
	}
	_, items, mods, _, _, err := m.zomboidLists(id)
	if err != nil {
		return "", err
	}
	drop := map[string]bool{}
	for _, x := range m.localModIDs(id, wid) {
		drop[x] = true
	}
	if len(drop) == 0 {
		if det, err := workshopDetails(ctx, []string{wid}); err == nil && len(det) == 1 {
			for _, x := range det[0].ModIDs {
				drop[x] = true
			}
		}
	}
	var keepItems, keepMods []string
	for _, x := range items {
		if x != wid {
			keepItems = append(keepItems, x)
		}
	}
	for _, x := range mods {
		if !drop[strings.TrimPrefix(x, `\`)] {
			keepMods = append(keepMods, x)
		}
	}
	if err := m.applyZomboidIni(ctx, id, map[string]string{"WorkshopItems": strings.Join(keepItems, ";"), "Mods": strings.Join(keepMods, ";")}); err != nil {
		return "", err
	}
	return "Aus den Listen entfernt. Die Dateien bleiben liegen, bis Steam sie aufräumt.", nil
}

func (m *Manager) applyZomboidIni(ctx context.Context, id string, vals map[string]string) error {
	sv, tpl, err := m.get(id)
	if err != nil {
		return err
	}
	for i, cf := range m.configFiles(sv, tpl) {
		if strings.HasSuffix(cf.c.File, ".ini") && strings.Contains(cf.c.File, "Zomboid/Server/") {
			_, err := m.ApplySettings(ctx, id, nil, map[int]map[string]string{i: vals})
			return err
		}
	}
	return errors.New("Die Server-INI fehlt in der Vorlage")
}
