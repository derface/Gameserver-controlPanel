package manager

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Seasons: the world starts over, the old one goes into the archive (a
// backup that is never deleted) together with a hall of fame.

// saveDirNames are folder names that usually hold the world.
var saveDirNames = map[string]bool{"saves": true, "savegame": true, "savegames": true, "savedarks": true, "worlds": true, "worlds_local": true,
	"save-data": true, "world": true, "world_nether": true, "world_the_end": true, "db": true}

// WorldCandidates suggests the folders to reset: known save folders found
// in the server directory (up to 6 levels deep).
func (m *Manager) WorldCandidates(id string) ([]string, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return nil, err
	}
	dir := m.ServerDir(sv.ID)
	var out []string
	if sv.Seasons != nil && len(sv.Seasons.Wipe) > 0 {
		for _, p := range sv.Seasons.Wipe {
			if _, err := os.Stat(filepath.Join(dir, p)); err == nil {
				out = append(out, p)
			}
		}
		if len(out) > 0 {
			return out, nil
		}
	}
	// only what backups cover: the game files themselves are left out
	roots, _ := scopeRoots(dir, tpl)
	seen := map[string]bool{}
	for _, root := range roots {
		_ = filepath.WalkDir(filepath.Join(dir, root), func(p string, d fs.DirEntry, err error) error {
			if err != nil || !d.IsDir() {
				return nil
			}
			rel, _ := filepath.Rel(dir, p)
			rel = filepath.ToSlash(rel)
			if strings.Count(rel, "/") > 6 {
				return filepath.SkipDir
			}
			name := strings.ToLower(d.Name())
			if saveDirNames[name] && !(name == "db" && !strings.Contains(strings.ToLower(rel), "zomboid")) {
				if !seen[rel] {
					seen[rel] = true
					out = append(out, rel)
				}
			}
			return nil
		})
	}
	// the deepest match wins: Valheim's "saves" also holds the admin and
	// ban lists, "saves/worlds_local" only the world
	var deep []string
	for _, a := range out {
		inner := false
		for _, b := range out {
			if strings.HasPrefix(b, a+"/") {
				inner = true
				break
			}
		}
		if !inner {
			deep = append(deep, a)
		}
	}
	out = deep
	sort.Strings(out)
	return out, nil
}

// checkWipe validates the folders to reset.
func (m *Manager) checkWipe(id string, wipe []string) ([]string, error) {
	dir := m.ServerDir(id)
	var out []string
	for _, p := range wipe {
		p = filepath.ToSlash(filepath.Clean(strings.TrimSpace(p)))
		if p == "" || p == "." || strings.HasPrefix(p, "../") || p == ".." || filepath.IsAbs(p) || strings.HasPrefix(p, "/") {
			return nil, fmt.Errorf("Ungültiger Pfad „%s“", p)
		}
		if _, err := os.Lstat(filepath.Join(dir, p)); err != nil {
			continue // already gone: nothing to reset
		}
		out = append(out, p)
	}
	if len(wipe) == 0 {
		return nil, errors.New("Bitte mindestens einen Ordner wählen, der zurückgesetzt wird")
	}
	return out, nil
}

// playerSeconds is the play time per player so far (for the season tally).
func (m *Manager) playerSeconds(id string) map[string]int64 {
	statsMu.Lock()
	defer statsMu.Unlock()
	s := m.loadStats(id)
	out := map[string]int64{}
	for n, p := range s.Players {
		out[n] = p.Seconds
	}
	return out
}

// seasonTally ranks the players by time played since the season began.
func seasonTally(now, base map[string]int64) ([]store.SeasonPlayer, float64) {
	var top []store.SeasonPlayer
	var total int64
	for n, sec := range now {
		d := sec - base[n]
		if d <= 0 {
			continue
		}
		total += d
		top = append(top, store.SeasonPlayer{Name: n, Seconds: d})
	}
	sort.Slice(top, func(i, j int) bool { return top[i].Seconds > top[j].Seconds })
	if len(top) > 10 {
		top = top[:10]
	}
	return top, float64(total) / 3600
}

// StartSeason archives the world and resets the chosen folders. It runs in
// the background (stop, backup, reset, start).
func (m *Manager) StartSeason(id, name string, wipe []string, by string) error {
	name = strings.TrimSpace(name)
	if name == "" || len(name) > 60 {
		return errors.New("Bitte einen Namen für die neue Saison (höchstens 60 Zeichen)")
	}
	wipe, err := m.checkWipe(id, wipe)
	if err != nil {
		return err
	}
	if _, ok := m.st.Server(id); !ok {
		return errors.New("Server nicht gefunden")
	}
	m.setJob(id, "Neue Saison wird vorbereitet")
	go func() {
		unlock := m.lock(id)
		defer unlock()
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Hour)
		defer cancel()
		if err := m.newSeason(ctx, id, name, wipe, by); err != nil {
			m.failJob(id, fmt.Errorf("Neue Saison fehlgeschlagen: %w", err))
			m.EventKind(id, "error", "error", "Neue Saison fehlgeschlagen: "+err.Error())
			return
		}
		m.refresh(id)
		m.clearJob(id)
	}()
	return nil
}

func (m *Manager) newSeason(ctx context.Context, id, name string, wipe []string, by string) error {
	sv, tpl, err := m.get(id)
	if err != nil {
		return err
	}
	dir := m.ServerDir(id)
	running := m.isRunning(ctx, sv)
	if running {
		m.setJob(id, "Server wird gestoppt")
		m.expect(id)
		if err := m.dk.Stop(ctx, sv.ContainerID, tpl.StopTimeout); err != nil {
			return err
		}
	}
	m.setJob(id, "Alte Welt wird archiviert")
	b, err := m.createBackup(ctx, id, KindSeason)
	if err != nil {
		if running {
			_ = m.dk.Start(ctx, sv.ContainerID)
		}
		return err // nothing was deleted
	}
	now := time.Now()
	secs := m.playerSeconds(id)
	m.setJob(id, "Welt wird zurückgesetzt")
	for _, p := range wipe {
		if err := os.RemoveAll(filepath.Join(dir, p)); err != nil {
			return fmt.Errorf("%s: %w", p, err)
		}
	}
	var prev store.Season
	err = m.st.ModifyServer(id, func(s *store.Server) error {
		if s.Seasons == nil {
			s.Seasons = &store.SeasonState{}
		}
		ss := s.Seasons
		cur := ss.Current
		if cur.N == 0 {
			cur = store.Season{N: 1, Name: "Saison 1", Start: s.Created}
		}
		cur.End, cur.Backup = now, b.Name
		cur.Top, cur.Hours = seasonTally(secs, cur.Base)
		cur.Base = nil
		prev = cur
		ss.Past = append([]store.Season{cur}, ss.Past...)
		ss.Current = store.Season{N: cur.N + 1, Name: name, Start: now, Base: secs}
		ss.Wipe = wipe
		ss.Plan = nil
		return nil
	})
	if err != nil {
		return err
	}
	if running {
		m.setJob(id, "Server startet mit neuer Welt")
		if err := m.dk.Start(ctx, sv.ContainerID); err != nil {
			return err
		}
	}
	txt := fmt.Sprintf("Neue Saison „%s“ hat begonnen. Die Welt von „%s“ ist archiviert", name, prev.Name)
	if len(prev.Top) > 0 {
		txt += fmt.Sprintf("; am meisten gespielt hat %s (%s)", prev.Top[0].Name, humanDur(time.Duration(prev.Top[0].Seconds)*time.Second))
	}
	if by != "" {
		txt += " (von " + by + ")"
	}
	m.EventKind(id, "info", "event", txt+".")
	return nil
}

// PlanSeason schedules the next season (nil at removes the plan).
func (m *Manager) PlanSeason(id string, at time.Time, name string, wipe []string) error {
	if !at.IsZero() {
		if at.Before(time.Now().Add(5 * time.Minute)) {
			return errors.New("Der Termin muss mindestens 5 Minuten in der Zukunft liegen")
		}
		if strings.TrimSpace(name) == "" {
			return errors.New("Bitte einen Namen für die neue Saison")
		}
		var err error
		if wipe, err = m.checkWipe(id, wipe); err != nil {
			return err
		}
	}
	return m.st.ModifyServer(id, func(s *store.Server) error {
		if s.Seasons == nil {
			s.Seasons = &store.SeasonState{Current: store.Season{N: 1, Name: "Saison 1", Start: s.Created}}
		}
		if at.IsZero() {
			s.Seasons.Plan = nil
			return nil
		}
		s.Seasons.Plan = &store.SeasonPlan{At: at, Name: strings.TrimSpace(name), Wipe: wipe}
		return nil
	})
}

// checkSeasons warns before a planned season and starts it on time.
func (m *Manager) checkSeasons(now time.Time) {
	for _, sv := range m.st.Servers() {
		if sv.Seasons == nil || sv.Seasons.Plan == nil {
			continue
		}
		p := sv.Seasons.Plan
		left := int(p.At.Sub(now).Minutes() + 0.5)
		if now.Before(p.At) {
			for _, w := range []int{60, 10, 1} {
				if left <= w && (p.Warned == 0 || p.Warned > w) {
					m.Broadcast(sv.ID, fmt.Sprintf("In %d Minute%s beginnt die neue Saison „%s“, die Welt startet neu!", w, map[bool]string{true: "", false: "n"}[w == 1], p.Name))
					_ = m.st.ModifyServer(sv.ID, func(s *store.Server) error {
						if s.Seasons != nil && s.Seasons.Plan != nil {
							s.Seasons.Plan.Warned = w
						}
						return nil
					})
					break
				}
			}
			continue
		}
		if now.Sub(p.At) > 6*time.Hour {
			// the panel was off far too long: do not wipe by surprise
			_ = m.st.ModifyServer(sv.ID, func(s *store.Server) error {
				if s.Seasons != nil {
					s.Seasons.Plan = nil
				}
				return nil
			})
			m.EventKind(sv.ID, "warn", "error", fmt.Sprintf("Geplante Saison „%s“ ausgelassen: das Panel lief zum Termin nicht. Bitte neu planen.", p.Name))
			continue
		}
		_ = m.st.ModifyServer(sv.ID, func(s *store.Server) error {
			if s.Seasons != nil {
				s.Seasons.Plan = nil // only once, even if it fails
			}
			return nil
		})
		if err := m.StartSeason(sv.ID, p.Name, p.Wipe, "Zeitplan"); err != nil {
			m.EventKind(sv.ID, "error", "error", "Geplante Saison konnte nicht starten: "+err.Error())
		}
	}
}

// SeasonView is what the panel shows.
type SeasonView struct {
	Current    store.Season         `json:"current"`
	Standings  []store.SeasonPlayer `json:"standings"`
	Hours      float64              `json:"hours"`
	Past       []store.Season       `json:"past"`
	Plan       *store.SeasonPlan    `json:"plan,omitempty"`
	Candidates []string             `json:"candidates"`
	Roots      []string             `json:"roots"` // top-level entries, to pick by hand
}

func (m *Manager) Seasons(id string) (SeasonView, error) {
	sv, _, err := m.get(id)
	if err != nil {
		return SeasonView{}, err
	}
	v := SeasonView{Past: []store.Season{}, Standings: []store.SeasonPlayer{}}
	if sv.Seasons != nil {
		v.Current, v.Past, v.Plan = sv.Seasons.Current, sv.Seasons.Past, sv.Seasons.Plan
	}
	if v.Current.N == 0 {
		v.Current = store.Season{N: 1, Name: "Saison 1", Start: sv.Created}
	}
	v.Standings, v.Hours = seasonTally(m.playerSeconds(id), v.Current.Base)
	if v.Standings == nil {
		v.Standings = []store.SeasonPlayer{}
	}
	v.Current.Base = nil
	if v.Past == nil {
		v.Past = []store.Season{}
	}
	v.Candidates, _ = m.WorldCandidates(id)
	if v.Candidates == nil {
		v.Candidates = []string{}
	}
	roots, _ := m.BackupScope(id)
	v.Roots = []string{}
	for _, r := range roots {
		if !strings.HasPrefix(r, ".") { // the panel's own marker files
			v.Roots = append(v.Roots, r)
		}
	}
	return v, nil
}
