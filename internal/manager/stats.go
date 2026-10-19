package manager

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/query"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Player statistics: once a minute the panel notes how many players are
// on each running server (hourly buckets, 90 days) and, where the game tells
// names, how long each player plays. From that it derives hints like the
// quietest hour for restarts or servers nobody uses.

type hourBucket struct {
	H   int64 `json:"h"` // unix time of the full hour
	Sum int   `json:"s"` // sum of player counts over the samples
	N   int   `json:"n"` // samples (minutes the server ran)
	Max int   `json:"m"`
}

type playerStat struct {
	Seconds  int64     `json:"seconds"`
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
	Sessions int       `json:"sessions"`
}

type serverStats struct {
	Hours   []hourBucket           `json:"hours"`
	Players map[string]*playerStat `json:"players"`
	Online  map[string]time.Time   `json:"online"`
	Since   time.Time              `json:"since"`
	Geo     map[string]*geoStat    `json:"geo,omitempty"` // where players connect from
}

const statsKeep = 90 * 24 * time.Hour

var statsMu sync.Mutex

func (m *Manager) statsFile(id string) string {
	return filepath.Join(m.dataDir, "stats", id+".json")
}

func (m *Manager) loadStats(id string) *serverStats {
	s := &serverStats{Players: map[string]*playerStat{}, Online: map[string]time.Time{}}
	if b, err := os.ReadFile(m.statsFile(id)); err == nil {
		_ = json.Unmarshal(b, s)
	}
	if s.Players == nil {
		s.Players = map[string]*playerStat{}
	}
	if s.Online == nil {
		s.Online = map[string]time.Time{}
	}
	return s
}

func (m *Manager) saveStats(id string, s *serverStats) {
	_ = os.MkdirAll(filepath.Dir(m.statsFile(id)), 0o700)
	if b, err := json.Marshal(s); err == nil {
		tmp := m.statsFile(id) + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, m.statsFile(id))
		}
	}
}

// recordPlayers adds one minute sample. names may be nil (game tells none).
// It returns who joined and who left since the last sample.
func (m *Manager) recordPlayers(id string, now time.Time, count int, names []string) (joined, left []string) {
	statsMu.Lock()
	defer statsMu.Unlock()
	s := m.loadStats(id)
	if s.Since.IsZero() {
		s.Since = now
	}
	h := now.Truncate(time.Hour).Unix()
	if n := len(s.Hours); n > 0 && s.Hours[n-1].H == h {
		b := &s.Hours[n-1]
		b.Sum, b.N = b.Sum+count, b.N+1
		b.Max = max(b.Max, count)
	} else {
		s.Hours = append(s.Hours, hourBucket{H: h, Sum: count, N: 1, Max: count})
	}
	cut := now.Add(-statsKeep).Unix()
	i := 0
	for i < len(s.Hours) && s.Hours[i].H < cut {
		i++
	}
	s.Hours = s.Hours[i:]
	if names != nil {
		seen := map[string]bool{}
		for _, n := range names {
			n = strings.TrimSpace(n)
			if n == "" || seen[n] {
				continue
			}
			seen[n] = true
			p := s.Players[n]
			if p == nil {
				p = &playerStat{First: now}
				s.Players[n] = p
			}
			if _, on := s.Online[n]; !on {
				p.Sessions++
				s.Online[n] = now
				joined = append(joined, n)
			}
			p.Seconds += 60
			p.Last = now
		}
		for n := range s.Online {
			if !seen[n] {
				delete(s.Online, n)
				left = append(left, n)
			}
		}
	}
	m.saveStats(id, s)
	return joined, left
}

// runStats samples all running servers once a minute.
func (m *Manager) runStats(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			m.recordHistory(now)
			if now.Minute() == 7 && now.Hour() == 4 {
				m.pruneHistory(now)
			}
			if now.Minute() == 23 {
				m.checkDisk()
			}
			for _, sv := range m.st.Servers() {
				if sv.CreatedBy == testCreator || sv.ContainerID == "" {
					continue
				}
				m.sampleStats(ctx, sv, now)
			}
		}
	}
}

func (m *Manager) sampleStats(ctx context.Context, sv store.Server, now time.Time) {
	m.mu.RLock()
	l := m.live[sv.ID]
	running := l != nil && l.Phase == "running"
	count := -1
	if l != nil {
		count = l.Players
	}
	m.mu.RUnlock()
	if !running {
		return
	}
	tpl, ok := m.tpls.Get(sv.Template)
	if !ok {
		return
	}
	var names []string
	switch {
	case tpl.Query != nil && tpl.Query.Type == "a2s" && tpl.Query.Port >= 0 && tpl.Query.Port < len(sv.Ports):
		if ps, err := query.A2SPlayers("127.0.0.1", sv.Ports[tpl.Query.Port].Host); err == nil {
			names = []string{}
			for _, p := range ps {
				if strings.TrimSpace(p.Name) != "" {
					names = append(names, p.Name)
				}
			}
			if len(names) == 0 && len(ps) > 0 {
				names = nil // the game hides names
			}
		}
	case tpl.Console == "exec": // Minecraft: rcon-cli, does not clutter the console
		if p := profileFor(tpl); p != nil && p.ListCmd != "" {
			cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
			if lines, err := m.CommandOutput(cctx, sv.ID, p.ListCmd); err == nil {
				names = p.Parse(lines)
				if names == nil {
					names = []string{}
				}
			}
			cancel()
		}
	}
	if count < 0 && names != nil {
		count = len(names)
	}
	if count < 0 {
		return // nothing known about players
	}
	joined, left := m.recordPlayers(sv.ID, now, count, names)
	for _, n := range joined {
		m.webhook("players", "player.join", sv.ID, "info", n+" ist dem Server beigetreten", map[string]any{"player": n, "online": count})
	}
	for _, n := range left {
		m.webhook("players", "player.leave", sv.ID, "info", n+" hat den Server verlassen", map[string]any{"player": n, "online": count})
	}
}

// StatsView is what the statistics section shows.
type StatsView struct {
	Since      time.Time        `json:"since"`
	Days       int              `json:"days"`        // days with data in the heat map window
	Heat       [7][24]float64   `json:"heat"`        // average players, [weekday 0=Sunday][hour]
	HeatMax    float64          `json:"heat_max"`    // for the colour scale
	LastPlayed time.Time        `json:"last_played"` // last minute with someone online
	Players    []PlayerStatView `json:"players"`
	Insights   []string         `json:"insights"`
	HasNames   bool             `json:"has_names"`
}

type PlayerStatView struct {
	Name     string    `json:"name"`
	Seconds  int64     `json:"seconds"`
	Last     time.Time `json:"last"`
	Sessions int       `json:"sessions"`
	Online   bool      `json:"online"`
}

// Stats evaluates the last 30 days of a server.
func (m *Manager) Stats(id string) (StatsView, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return StatsView{}, err
	}
	statsMu.Lock()
	s := m.loadStats(id)
	statsMu.Unlock()
	v := StatsView{Since: s.Since, Players: []PlayerStatView{}, Insights: []string{}}
	now := time.Now()
	from := now.Add(-30 * 24 * time.Hour).Unix()
	var sum [7][24]float64
	var n [7][24]int
	days := map[string]bool{}
	var hourSum [24]float64
	var hourN [24]int
	recent14 := now.Add(-14 * 24 * time.Hour).Unix()
	ran14, max14 := 0, 0
	for _, b := range s.Hours {
		t := time.Unix(b.H, 0)
		if b.Max > 0 && (v.LastPlayed.IsZero() || t.After(v.LastPlayed)) {
			v.LastPlayed = t
		}
		if b.H >= recent14 {
			ran14 += b.N
			max14 = max(max14, b.Max)
		}
		if b.H < from || b.N == 0 {
			continue
		}
		wd, hr := int(t.Weekday()), t.Hour()
		sum[wd][hr] += float64(b.Sum) / float64(b.N)
		n[wd][hr]++
		hourSum[hr] += float64(b.Sum) / float64(b.N)
		hourN[hr]++
		days[t.Format("2006-01-02")] = true
	}
	for d := 0; d < 7; d++ {
		for h := 0; h < 24; h++ {
			if n[d][h] > 0 {
				v.Heat[d][h] = sum[d][h] / float64(n[d][h])
				v.HeatMax = max(v.HeatMax, v.Heat[d][h])
			} else {
				v.Heat[d][h] = -1 // no data
			}
		}
	}
	v.Days = len(days)
	for name, p := range s.Players {
		_, on := s.Online[name]
		v.Players = append(v.Players, PlayerStatView{Name: name, Seconds: p.Seconds, Last: p.Last, Sessions: p.Sessions, Online: on})
	}
	v.HasNames = len(v.Players) > 0
	sort.Slice(v.Players, func(i, j int) bool { return v.Players[i].Seconds > v.Players[j].Seconds })
	if len(v.Players) > 50 {
		v.Players = v.Players[:50]
	}

	// hints
	if v.Days >= 7 {
		best, bestAvg := -1, 0.0
		for h := 0; h < 24; h++ {
			if hourN[h] < 3 {
				continue
			}
			avg := hourSum[h] / float64(hourN[h])
			if best < 0 || avg < bestAvg {
				best, bestAvg = h, avg
			}
		}
		if best >= 0 {
			if bestAvg == 0 {
				v.Insights = append(v.Insights, fmt.Sprintf("Um %02d:00 Uhr war in den letzten %d Tagen nie jemand online. Gut für Neustarts und Updates.", best, v.Days))
			} else {
				v.Insights = append(v.Insights, fmt.Sprintf("Am ruhigsten ist es um %02d:00 Uhr (im Schnitt %s Spieler). Gut für Neustarts und Updates.", best, deNum(bestAvg)))
			}
			for _, t := range sv.Tasks {
				if !t.Enabled || t.EveryHours > 0 || t.Quiet || (t.Action != "restart" && t.Action != "update") {
					continue
				}
				var hh int
				if _, err := fmt.Sscanf(t.At, "%d:", &hh); err == nil && hourN[hh] >= 3 {
					if avg := hourSum[hh] / float64(hourN[hh]); avg >= 0.5 && hh != best {
						v.Insights = append(v.Insights, fmt.Sprintf("Dein geplanter %s um %s trifft oft Spieler (im Schnitt %s online). Besser %02d:00 Uhr?", TaskLabel(t.Action), t.At, deNum(avg), best))
					}
				}
			}
		}
	}
	if !s.Since.IsZero() && now.Sub(s.Since) >= 14*24*time.Hour && ran14 > 60*24*3 && max14 == 0 {
		v.Insights = append(v.Insights, "Seit 14 Tagen hat niemand auf diesem Server gespielt, er läuft aber. Stoppen spart Arbeitsspeicher ("+fmt.Sprintf("%d MB", sv.MemoryMB)+").")
	}
	if len(v.Insights) == 0 && v.Days < 7 {
		v.Insights = append(v.Insights, fmt.Sprintf("Noch zu wenig Daten für Empfehlungen: %d von 7 Tagen gesammelt.", v.Days))
	}
	_ = tpl
	return v, nil
}

// Insights collects the hints of all servers for the overview.
func (m *Manager) Insights() []map[string]string {
	var out []map[string]string
	for _, sv := range m.st.Servers() {
		if sv.CreatedBy == testCreator {
			continue
		}
		v, err := m.Stats(sv.ID)
		if err != nil || v.Days < 7 {
			continue
		}
		for _, t := range v.Insights {
			out = append(out, map[string]string{"server": sv.ID, "name": sv.Name, "text": t})
		}
	}
	return out
}

// deNum formats one decimal the German way (0,3).
func deNum(f float64) string { return strings.Replace(fmt.Sprintf("%.1f", f), ".", ",", 1) }

// PlayerTotal sums a player over several servers (public profiles).
type PlayerTotal struct {
	Name     string    `json:"name"`
	Seconds  int64     `json:"seconds"`
	Sessions int       `json:"sessions"`
	First    time.Time `json:"first"`
	Last     time.Time `json:"last"`
	Fav      string    `json:"fav"` // server with the most time
	Servers  int       `json:"servers"`
	Online   bool      `json:"online,omitempty"`
}

// PlayerTotals returns the players of the given servers, most time first.
// names maps server id to its display name.
func (m *Manager) PlayerTotals(names map[string]string, limit int) []PlayerTotal {
	statsMu.Lock()
	type acc struct {
		PlayerTotal
		best int64
	}
	all := map[string]*acc{}
	for id, sname := range names {
		s := m.loadStats(id)
		for n, p := range s.Players {
			a := all[strings.ToLower(n)]
			if a == nil {
				a = &acc{PlayerTotal: PlayerTotal{Name: n, First: p.First}}
				all[strings.ToLower(n)] = a
			}
			a.Seconds += p.Seconds
			a.Sessions += p.Sessions
			a.Servers++
			if p.First.Before(a.First) {
				a.First = p.First
			}
			if p.Last.After(a.Last) {
				a.Last = p.Last
			}
			if p.Seconds > a.best {
				a.best, a.Fav = p.Seconds, sname
			}
			if _, on := s.Online[n]; on && time.Since(p.Last) < 3*time.Minute {
				a.Online = true
			}
		}
	}
	statsMu.Unlock()
	out := make([]PlayerTotal, 0, len(all))
	for _, a := range all {
		out = append(out, a.PlayerTotal)
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Seconds > out[j].Seconds })
	if limit > 0 && len(out) > limit {
		out = out[:limit]
	}
	return out
}
