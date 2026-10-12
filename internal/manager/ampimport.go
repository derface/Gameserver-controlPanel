package manager

import (
	"archive/tar"
	"compress/gzip"
	"crypto/rand"
	"crypto/subtle"
	_ "embed"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path"
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

// Moving servers over from CubeCoders AMP: a script on the AMP machine packs
// save games, game configs and the instance settings (GenericModule.kvp) and
// uploads them with a one-time token. The panel picks the template by Steam
// app ID, takes over the settings it knows and puts the files in place.

//go:embed ampexport.py
var ampExportScript []byte

// AMPExportScript is the export script served to the AMP machine.
func AMPExportScript() []byte { return ampExportScript }

const (
	ampTokenTTL   = 2 * time.Hour
	ampMaxArchive = 20 << 30
)

type AMPImport struct {
	ID           string    `json:"id"`
	Instance     string    `json:"instance"`
	Name         string    `json:"name"`
	Game         string    `json:"game"`
	AppID        int       `json:"app_id"`
	Template     string    `json:"template,omitempty"`
	TemplateName string    `json:"template_name,omitempty"`
	Files        int       `json:"files"`
	Bytes        int64     `json:"bytes"`
	Settings     int       `json:"settings"`
	Received     time.Time `json:"received"`
	Problem      string    `json:"problem,omitempty"`
}

type ampTokens struct {
	mu sync.Mutex
	m  map[string]time.Time
}

var ampTok = ampTokens{m: map[string]time.Time{}}

// NewAMPToken returns a token that allows uploads for two hours.
func (m *Manager) NewAMPToken() (string, time.Time) {
	b := make([]byte, 18)
	_, _ = rand.Read(b)
	t := hex.EncodeToString(b)
	exp := time.Now().Add(ampTokenTTL)
	ampTok.mu.Lock()
	for k, e := range ampTok.m {
		if time.Now().After(e) {
			delete(ampTok.m, k)
		}
	}
	ampTok.m[t] = exp
	ampTok.mu.Unlock()
	return t, exp
}

// AMPTokenValid reports whether an upload token is known and fresh.
func (m *Manager) AMPTokenValid(t string) bool {
	if len(t) != 36 {
		return false
	}
	ampTok.mu.Lock()
	defer ampTok.mu.Unlock()
	for k, e := range ampTok.m {
		if subtle.ConstantTimeCompare([]byte(k), []byte(t)) == 1 && time.Now().Before(e) {
			return true
		}
	}
	return false
}

func (m *Manager) ampDir() string { return filepath.Join(m.dataDir, "import", "amp") }

var ampID = regexp.MustCompile(`^[0-9a-f]{16}$`)

// ampArchive is what the panel reads from an export.
type ampArchive struct {
	kvp      map[string]string
	settings map[string]string
	meta     struct {
		Instance string `json:"instance"`
		Friendly string `json:"friendly"`
		Game     string `json:"game"`
	}
	files []string // below AMP's base directory
	bytes int64
}

func readAMPArchive(p string) (*ampArchive, error) {
	f, err := os.Open(p)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return nil, errors.New("Das ist kein AMP-Export (kein gzip)")
	}
	a := &ampArchive{settings: map[string]string{}}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return nil, fmt.Errorf("Archiv beschädigt: %w", err)
		}
		switch {
		case h.Name == "amp/GenericModule.kvp":
			b, _ := io.ReadAll(io.LimitReader(tr, 4<<20))
			a.kvp = parseKVP(string(b))
		case h.Name == "amp/instance.json":
			b, _ := io.ReadAll(io.LimitReader(tr, 1<<20))
			_ = json.Unmarshal(b, &a.meta)
		case strings.HasPrefix(h.Name, "data/") && h.Typeflag == tar.TypeReg:
			a.files = append(a.files, strings.TrimPrefix(h.Name, "data/"))
			a.bytes += h.Size
		}
	}
	if a.kvp == nil {
		return nil, errors.New("Im Archiv fehlt GenericModule.kvp. Ist das ein Export aus AMP?")
	}
	var raw map[string]any
	if json.Unmarshal([]byte(a.kvp["App.AppSettings"]), &raw) == nil {
		for k, v := range raw {
			a.settings[k] = fmt.Sprint(v)
		}
	}
	return a, nil
}

func parseKVP(s string) map[string]string {
	out := map[string]string{}
	for _, line := range strings.Split(strings.TrimPrefix(s, "\ufeff"), "\n") {
		line = strings.TrimRight(line, "\r")
		if strings.HasPrefix(line, "#") {
			continue
		}
		if k, v, ok := strings.Cut(line, "="); ok {
			out[strings.TrimSpace(k)] = strings.TrimSpace(v)
		}
	}
	return out
}

// steamApp returns the Steam app ID AMP downloads for the instance.
func (a *ampArchive) steamApp() int {
	var stages []struct {
		UpdateSource     int    `json:"UpdateSource"`
		UpdateSourceData string `json:"UpdateSourceData"`
	}
	_ = json.Unmarshal([]byte(a.kvp["App.UpdateSources"]), &stages)
	for _, s := range stages {
		if s.UpdateSource == 4 { // SteamCMD
			if n, err := strconv.Atoi(strings.TrimSpace(s.UpdateSourceData)); err == nil {
				return n
			}
		}
	}
	return 0
}

// pickTemplate prefers hand-made templates over generated ones.
func (m *Manager) ampTemplate(app int) (templates.Template, bool) {
	var c []templates.Template
	for _, t := range m.tpls.All() {
		if app > 0 && t.SteamAppID == app && !t.Custom && t.Status != "defekt" {
			c = append(c, t)
		}
	}
	sort.SliceStable(c, func(i, j int) bool {
		return !strings.HasPrefix(c[i].ID, "amp-") && strings.HasPrefix(c[j].ID, "amp-")
	})
	if len(c) == 0 {
		return templates.Template{}, false
	}
	return c[0], true
}

// ReceiveAMP stores one uploaded export.
func (m *Manager) ReceiveAMP(r io.Reader, instance string) (AMPImport, error) {
	dir := m.ampDir()
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return AMPImport{}, err
	}
	b := make([]byte, 8)
	_, _ = rand.Read(b)
	id := hex.EncodeToString(b)
	p := filepath.Join(dir, id+".tar.gz")
	f, err := os.OpenFile(p, os.O_CREATE|os.O_WRONLY|os.O_EXCL, 0o600)
	if err != nil {
		return AMPImport{}, err
	}
	n, err := io.Copy(f, io.LimitReader(r, ampMaxArchive+1))
	f.Close()
	if err == nil && n > ampMaxArchive {
		err = errors.New("Das Archiv ist größer als 20 GB")
	}
	if err != nil {
		os.Remove(p)
		return AMPImport{}, err
	}
	a, err := readAMPArchive(p)
	if err != nil {
		os.Remove(p)
		return AMPImport{}, err
	}
	it := AMPImport{ID: id, Instance: firstNonEmpty(a.meta.Instance, instance), Name: firstNonEmpty(a.meta.Friendly, a.meta.Instance, instance),
		Game: firstNonEmpty(a.meta.Game, a.kvp["Meta.DisplayName"]), AppID: a.steamApp(), Files: len(a.files), Bytes: a.bytes,
		Settings: len(a.settings), Received: time.Now()}
	if len([]rune(a.meta.Friendly)) < 4 || a.meta.Friendly == a.meta.Instance {
		it.Name = firstNonEmpty(it.Game, it.Name) // "pw01" says less than "Palworld"
	}
	it.Name = strings.TrimSpace(templates.Clean(it.Name))
	if len([]rune(it.Name)) > 40 {
		it.Name = string([]rune(it.Name)[:40])
	}
	if t, ok := m.ampTemplate(it.AppID); ok {
		it.Template, it.TemplateName = t.ID, t.Name
	} else {
		it.Problem = fmt.Sprintf("Für %s (Steam-App %d) hat das Panel noch keine Vorlage.", firstNonEmpty(it.Game, "dieses Spiel"), it.AppID)
	}
	j, _ := json.MarshalIndent(it, "", " ")
	if err := os.WriteFile(filepath.Join(dir, id+".json"), j, 0o600); err != nil {
		os.Remove(p)
		return AMPImport{}, err
	}
	return it, nil
}

func firstNonEmpty(s ...string) string {
	for _, x := range s {
		if strings.TrimSpace(x) != "" {
			return strings.TrimSpace(x)
		}
	}
	return ""
}

// AMPImports lists the received exports, newest first.
func (m *Manager) AMPImports() []AMPImport {
	out := []AMPImport{}
	files, _ := filepath.Glob(filepath.Join(m.ampDir(), "*.json"))
	for _, f := range files {
		var it AMPImport
		if b, err := os.ReadFile(f); err == nil && json.Unmarshal(b, &it) == nil && ampID.MatchString(it.ID) {
			out = append(out, it)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Received.After(out[j].Received) })
	return out
}

func (m *Manager) ampItem(id string) (AMPImport, error) {
	var it AMPImport
	if !ampID.MatchString(id) {
		return it, errors.New("Unbekannter Export")
	}
	b, err := os.ReadFile(filepath.Join(m.ampDir(), id+".json"))
	if err != nil || json.Unmarshal(b, &it) != nil {
		return it, errors.New("Unbekannter Export")
	}
	return it, nil
}

// DeleteAMPImport throws an export away.
func (m *Manager) DeleteAMPImport(id string) error {
	if _, err := m.ampItem(id); err != nil {
		return err
	}
	os.Remove(filepath.Join(m.ampDir(), id+".tar.gz"))
	return os.Remove(filepath.Join(m.ampDir(), id+".json"))
}

var nonAlnum = regexp.MustCompile(`[^a-z0-9]`)

func normKey(s string) string { return nonAlnum.ReplaceAllString(strings.ToLower(s), "") }

// ampVars turns AMP's instance settings into values for the template.
func ampVars(tpl templates.Template, a *ampArchive) map[string]string {
	byNorm := map[string]string{}
	for k, v := range a.settings {
		byNorm[normKey(k)] = v
	}
	if v, ok := a.settings["$MaxUsers"]; ok {
		byNorm["maxplayers"] = v
	}
	vars := map[string]string{}
	for _, tv := range tpl.Vars {
		v, ok := byNorm[normKey(tv.Key)]
		if !ok || strings.Contains(v, "{{") {
			continue
		}
		switch tv.Type {
		case "bool":
			vars[tv.Key] = strconv.FormatBool(strings.EqualFold(strings.TrimSpace(v), "true"))
		case "select":
			for _, o := range tv.Options {
				if o == strings.TrimSpace(v) {
					vars[tv.Key] = o
				}
			}
		case "number":
			if f, err := strconv.ParseFloat(strings.TrimSpace(v), 64); err == nil && f == float64(int64(f)) {
				vars[tv.Key] = strconv.FormatInt(int64(f), 10)
			}
		default:
			vars[tv.Key] = v
		}
	}
	if imp := tpl.AMPImport; imp != nil {
		for k, pat := range imp.VarsFromFiles {
			re, err := regexp.Compile(pat)
			if err != nil {
				continue
			}
			for _, f := range a.files {
				if mt := re.FindStringSubmatch(f); len(mt) > 1 {
					vars[k] = mt[1]
					break
				}
			}
		}
		for k, v := range imp.Vars {
			vars[k] = v
		}
	}
	return vars
}

// ampTarget maps a path below AMP's base directory to the server folder.
func ampTarget(tpl templates.Template, rel string) string {
	best, to := "", "server/"
	if tpl.AMPImport != nil {
		for from, dst := range tpl.AMPImport.Map {
			if strings.HasPrefix(rel, from) && len(from) > len(best) {
				best, to = from, dst
			}
		}
	}
	return path.Clean(to + strings.TrimPrefix(rel, best))
}

// ImportAMP creates a server from an export and puts its files in place.
func (m *Manager) ImportAMP(id, name, by string) (store.Server, error) {
	it, err := m.ampItem(id)
	if err != nil {
		return store.Server{}, err
	}
	if it.Template == "" {
		return store.Server{}, errors.New(it.Problem)
	}
	tpl, ok := m.tpls.Get(it.Template)
	if !ok {
		return store.Server{}, fmt.Errorf("Vorlage %s fehlt", it.Template)
	}
	archive := filepath.Join(m.ampDir(), id+".tar.gz")
	a, err := readAMPArchive(archive)
	if err != nil {
		return store.Server{}, err
	}
	if strings.TrimSpace(name) == "" {
		name = it.Name
	}
	req := CreateReq{Template: tpl.ID, Name: name, Vars: ampVars(tpl, a), CreatedBy: by}
	req.before = func(dir string, uid int) error {
		m.setJob(filepath.Base(dir), "Spielstände aus AMP werden übernommen")
		if err := extractAMP(archive, dir, tpl, uid); err != nil {
			return fmt.Errorf("Übernahme der Spielstände: %w", err)
		}
		os.Remove(archive)
		os.Remove(filepath.Join(m.ampDir(), id+".json"))
		return nil
	}
	sv, err := m.Create(req)
	if err != nil {
		return sv, err
	}
	m.Event(sv.ID, "info", fmt.Sprintf("Aus AMP übernommen (Instanz %s): %d Dateien, %d Einstellungen", it.Instance, it.Files, len(req.Vars)))
	return sv, nil
}

// extractAMP writes the data/ part of an export into the server folder and
// hands the files to the container user.
func extractAMP(archive, dir string, tpl templates.Template, uid int) error {
	f, err := os.Open(archive)
	if err != nil {
		return err
	}
	defer f.Close()
	gz, err := gzip.NewReader(f)
	if err != nil {
		return err
	}
	base := filepath.Clean(dir) + string(os.PathSeparator)
	roots := map[string]bool{}
	tr := tar.NewReader(gz)
	for {
		h, err := tr.Next()
		if err == io.EOF {
			break
		}
		if err != nil {
			return err
		}
		if !strings.HasPrefix(h.Name, "data/") || h.Typeflag != tar.TypeReg {
			continue
		}
		rel := ampTarget(tpl, strings.TrimPrefix(h.Name, "data/"))
		target := filepath.Join(dir, filepath.FromSlash(rel))
		if !strings.HasPrefix(target, base) || strings.HasPrefix(rel, "..") {
			continue
		}
		roots[strings.SplitN(rel, "/", 2)[0]] = true
		if err := os.MkdirAll(filepath.Dir(target), 0o755); err != nil {
			return err
		}
		out, err := os.OpenFile(target, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, os.FileMode(h.Mode).Perm()|0o600)
		if err != nil {
			return err
		}
		_, err = io.Copy(out, tr)
		out.Close()
		if err != nil {
			return err
		}
		_ = os.Chtimes(target, h.ModTime, h.ModTime)
	}
	var werr error
	for r := range roots {
		werr = errors.Join(werr, filepath.Walk(filepath.Join(dir, r), func(p string, _ os.FileInfo, err error) error {
			if err != nil {
				return err
			}
			return os.Lchown(p, uid, uid)
		}))
	}
	if os.Geteuid() != 0 {
		return nil // tests run without root
	}
	return werr
}
