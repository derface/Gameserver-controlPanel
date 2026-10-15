package manager

import (
	"bufio"
	"fmt"
	"math"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/hoststats"
)

// Performance history: one line per minute and server in a file per day,
// kept 30 days. Appending keeps the cost tiny; reading a month is about
// 43 000 short lines per server.

const historyKeep = 30 * 24 * time.Hour

// HostID is the history name of the machine itself.
const HostID = "_host"

var historyMu sync.Mutex

func (m *Manager) historyDir(id string) string {
	return filepath.Join(m.dataDir, "history", id)
}

func (m *Manager) appendHistory(id string, now time.Time, line string) {
	historyMu.Lock()
	defer historyMu.Unlock()
	dir := m.historyDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return
	}
	f, err := os.OpenFile(filepath.Join(dir, now.Format("2006-01-02")+".csv"), os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	fmt.Fprintf(f, "%d,%s\n", now.Unix(), line)
	f.Close()
}

// pruneHistory deletes day files older than 30 days.
func (m *Manager) pruneHistory(now time.Time) {
	historyMu.Lock()
	defer historyMu.Unlock()
	cut := now.Add(-historyKeep - 24*time.Hour).Format("2006-01-02")
	dirs, _ := os.ReadDir(filepath.Join(m.dataDir, "history"))
	for _, d := range dirs {
		if !d.IsDir() {
			continue
		}
		if d.Name() != HostID {
			if _, ok := m.st.Server(d.Name()); !ok {
				os.RemoveAll(filepath.Join(m.dataDir, "history", d.Name()))
				continue
			}
		}
		files, _ := os.ReadDir(filepath.Join(m.dataDir, "history", d.Name()))
		for _, f := range files {
			if strings.TrimSuffix(f.Name(), ".csv") < cut {
				os.Remove(filepath.Join(m.dataDir, "history", d.Name(), f.Name()))
			}
		}
	}
}

// HostSnapshot is set by main to the host sampler.
type hostFunc func() hoststats.Snapshot

// recordHistory writes one minute for every server and the host.
func (m *Manager) recordHistory(now time.Time) {
	for _, sv := range m.st.Servers() {
		if sv.CreatedBy == testCreator {
			continue
		}
		m.mu.RLock()
		l := m.live[sv.ID]
		var st, cpu, mem, limit string
		pl := -1
		if l != nil {
			switch l.Phase {
			case "running":
				st = "r"
			case "error":
				st = "e"
			case "stopped", "":
				st = "s"
			default:
				st = "b" // starting, installing, updating
			}
			if l.State.Running {
				cpu = fmt.Sprintf("%.1f", l.Stats.CPUPercent/m.cores(sv))
				mem = fmt.Sprintf("%.0f", float64(l.Stats.MemUsed)/1048576)
				limit = fmt.Sprintf("%.0f", float64(l.Stats.MemLimit)/1048576)
			}
			pl = l.Players
		} else {
			st = "s"
		}
		m.mu.RUnlock()
		m.appendHistory(sv.ID, now, fmt.Sprintf("%s,%s,%s,%s,%d", st, cpu, mem, limit, pl))
	}
	if m.Host != nil {
		h := m.Host()
		if h.MemTotalMB > 0 {
			m.appendHistory(HostID, now, fmt.Sprintf("r,%.1f,%.0f,%.0f,-1,%.1f", h.CPUPercent, h.MemUsedMB, h.MemTotalMB, h.DiskUsedG))
		}
	}
}

type histRow struct {
	t       int64
	state   byte
	cpu     float64 // NaN when unknown
	mem     float64
	limit   float64
	players int
	disk    float64
}

func parseHistLine(s string) (histRow, bool) {
	f := strings.Split(s, ",")
	if len(f) < 6 {
		return histRow{}, false
	}
	t, err := strconv.ParseInt(f[0], 10, 64)
	if err != nil || f[1] == "" {
		return histRow{}, false
	}
	num := func(x string) float64 {
		if v, err := strconv.ParseFloat(x, 64); err == nil {
			return v
		}
		return math.NaN()
	}
	r := histRow{t: t, state: f[1][0], cpu: num(f[2]), mem: num(f[3]), limit: num(f[4]), players: -1, disk: math.NaN()}
	if p, err := strconv.Atoi(f[5]); err == nil {
		r.players = p
	}
	if len(f) > 6 {
		r.disk = num(f[6])
	}
	return r, true
}

func (m *Manager) readHistory(id string, from, to time.Time) []histRow {
	historyMu.Lock()
	defer historyMu.Unlock()
	var out []histRow
	for d := from.Truncate(24 * time.Hour).Add(-24 * time.Hour); !d.After(to); d = d.Add(24 * time.Hour) {
		f, err := os.Open(filepath.Join(m.historyDir(id), d.Local().Format("2006-01-02")+".csv"))
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(f)
		for sc.Scan() {
			if r, ok := parseHistLine(sc.Text()); ok && r.t >= from.Unix() && r.t <= to.Unix() {
				out = append(out, r)
			}
		}
		f.Close()
	}
	sort.Slice(out, func(i, j int) bool { return out[i].t < out[j].t })
	return out
}

// HistPoint is one bucket of the chart. Nil values mean no data.
type HistPoint struct {
	T       int64    `json:"t"`
	CPU     *float64 `json:"cpu"`     // percent of the server's share (host: of all cores)
	MemMB   *float64 `json:"mem"`     // average
	MemMax  *float64 `json:"mem_max"` // peak in the bucket
	LimitMB *float64 `json:"limit"`
	Players *int     `json:"players"` // maximum in the bucket
	Up      float64  `json:"up"`      // share of minutes running (0..1)
	Known   bool     `json:"k"`       // there is data for this bucket
	Err     bool     `json:"err,omitempty"`
}

type HistView struct {
	From   int64       `json:"from"`
	To     int64       `json:"to"`
	Step   int64       `json:"step"`
	Points []HistPoint `json:"points"`
	Peak   struct {
		CPU     float64 `json:"cpu"`
		MemMB   float64 `json:"mem"`
		Players int     `json:"players"`
	} `json:"peak"`
	Notes []string `json:"notes"`
}

// History returns the chart for the last span (24h, 7d or 30d).
func (m *Manager) History(id string, span time.Duration) HistView {
	to := time.Now()
	from := to.Add(-span)
	step := int64(5 * 60)
	switch {
	case span > 8*24*time.Hour:
		step = 2 * 3600
	case span > 2*24*time.Hour:
		step = 30 * 60
	}
	rows := m.readHistory(id, from, to)
	v := HistView{From: from.Unix(), To: to.Unix(), Step: step, Points: []HistPoint{}, Notes: []string{}}
	type acc struct {
		cpu, mem      float64
		nCPU, nMem    int
		memMax, limit float64
		players       int
		hasPl         bool
		up, n         int
		err           bool
	}
	buckets := map[int64]*acc{}
	for _, r := range rows {
		k := (r.t - from.Unix()) / step
		a := buckets[k]
		if a == nil {
			a = &acc{players: -1}
			buckets[k] = a
		}
		a.n++
		if r.state == 'r' {
			a.up++
		}
		if r.state == 'e' {
			a.err = true
		}
		if !math.IsNaN(r.cpu) {
			a.cpu += r.cpu
			a.nCPU++
			v.Peak.CPU = max(v.Peak.CPU, r.cpu)
		}
		if !math.IsNaN(r.mem) {
			a.mem += r.mem
			a.nMem++
			a.memMax = max(a.memMax, r.mem)
			v.Peak.MemMB = max(v.Peak.MemMB, r.mem)
		}
		if !math.IsNaN(r.limit) {
			a.limit = r.limit
		}
		if r.players >= 0 {
			a.hasPl = true
			a.players = max(a.players, r.players)
			v.Peak.Players = max(v.Peak.Players, r.players)
		}
	}
	n := (to.Unix() - from.Unix()) / step
	for k := int64(0); k <= n; k++ {
		p := HistPoint{T: from.Unix() + k*step}
		if a := buckets[k]; a != nil {
			if a.nCPU > 0 {
				x := math.Round(a.cpu/float64(a.nCPU)*10) / 10
				p.CPU = &x
			}
			if a.nMem > 0 {
				x, y := math.Round(a.mem/float64(a.nMem)), a.memMax
				p.MemMB, p.MemMax = &x, &y
			}
			if a.limit > 0 {
				x := a.limit
				p.LimitMB = &x
			}
			if a.hasPl {
				x := a.players
				p.Players = &x
			}
			p.Up = float64(a.up) / float64(a.n)
			p.Known = true
			p.Err = a.err
		}
		v.Points = append(v.Points, p)
	}
	v.Notes = histNotes(rows)
	return v
}

// histNotes finds simple patterns worth a sentence.
func histNotes(rows []histRow) []string {
	var notes []string
	if len(rows) < 60 {
		return []string{"Noch wenig Daten. Das Panel zeichnet ab jetzt jede Minute auf."}
	}
	var nearLimit, full int
	for _, r := range rows {
		if r.state != 'r' || math.IsNaN(r.mem) || math.IsNaN(r.limit) || r.limit <= 0 {
			continue
		}
		if r.mem/r.limit > 0.9 {
			nearLimit++
		}
		if r.mem/r.limit > 0.98 {
			full++
		}
	}
	if nearLimit > 30 {
		notes = append(notes, fmt.Sprintf("Der Arbeitsspeicher war %s lang über 90 %% voll. Mehr RAM zuweisen, sonst drohen Ruckler und Abstürze.", durText(nearLimit)))
	}
	// CPU by players: does load follow the player count?
	var busyCPU, idleCPU float64
	var nb, ni int
	for _, r := range rows {
		if r.state != 'r' || math.IsNaN(r.cpu) || r.players < 0 {
			continue
		}
		if r.players == 0 {
			idleCPU += r.cpu
			ni++
		} else {
			busyCPU += r.cpu
			nb++
		}
	}
	if ni > 60 && nb > 60 && idleCPU/float64(ni) > 50 {
		notes = append(notes, fmt.Sprintf("Auch ohne Spieler braucht der Server im Schnitt %.0f %% CPU. Das ist ungewöhnlich viel, eventuell eine Mod oder eine sehr große Welt.", idleCPU/float64(ni)))
	}
	if nb > 60 && busyCPU/float64(nb) > 85 {
		notes = append(notes, fmt.Sprintf("Mit Spielern liegt die CPU im Schnitt bei %.0f %%. Das ist am Limit, Ruckler sind wahrscheinlich.", busyCPU/float64(nb)))
	}
	return notes
}

func durText(min int) string {
	if min >= 120 {
		return fmt.Sprintf("%d Stunden", min/60)
	}
	return fmt.Sprintf("%d Minuten", min)
}

// UptimeDay is one bar of the online history.
type UptimeDay struct {
	Day     string  `json:"day"`     // 2026-10-15
	Up      float64 `json:"up"`      // share of the day the server ran
	Known   float64 `json:"known"`   // share of the day with data
	Crashes int     `json:"crashes"` // crashes that day
}

// Uptime returns the last n days for the status page.
func (m *Manager) Uptime(id string, days int) ([]UptimeDay, float64) {
	now := time.Now()
	start := time.Date(now.Year(), now.Month(), now.Day(), 0, 0, 0, 0, time.Local).AddDate(0, 0, -(days - 1))
	rows := m.readHistory(id, start, now)
	byDay := map[string][2]int{}
	for _, r := range rows {
		d := time.Unix(r.t, 0).Format("2006-01-02")
		c := byDay[d]
		c[1]++
		if r.state == 'r' {
			c[0]++
		}
		byDay[d] = c
	}
	crashes := map[string]int{}
	for _, e := range m.EventsSince(id, start) {
		if e.Kind == "crash" {
			crashes[e.Time.Local().Format("2006-01-02")]++
		}
	}
	var out []UptimeDay
	var up, known int
	for d := start; !d.After(now); d = d.AddDate(0, 0, 1) {
		k := d.Format("2006-01-02")
		c := byDay[k]
		mins := 1440
		if k == now.Format("2006-01-02") {
			mins = max(1, now.Hour()*60+now.Minute())
		}
		day := UptimeDay{Day: k, Crashes: crashes[k]}
		if c[1] > 0 {
			day.Up = float64(c[0]) / float64(mins)
			day.Known = min(1, float64(c[1])/float64(mins))
		}
		up += c[0]
		known += c[1]
		out = append(out, day)
	}
	pct := 0.0
	if known > 0 {
		pct = float64(up) / float64(known) * 100
	}
	return out, pct
}

var uptimeCache struct {
	sync.Mutex
	m map[string]uptimeEntry
}

type uptimeEntry struct {
	at   time.Time
	days []UptimeDay
	pct  float64
}

// UptimeCached is Uptime with a 10-minute cache, for the public page.
func (m *Manager) UptimeCached(id string) ([]UptimeDay, float64) {
	uptimeCache.Lock()
	if uptimeCache.m == nil {
		uptimeCache.m = map[string]uptimeEntry{}
	}
	e, ok := uptimeCache.m[id]
	uptimeCache.Unlock()
	if ok && time.Since(e.at) < 10*time.Minute {
		return e.days, e.pct
	}
	days, pct := m.Uptime(id, 30)
	uptimeCache.Lock()
	uptimeCache.m[id] = uptimeEntry{time.Now(), days, pct}
	uptimeCache.Unlock()
	return days, pct
}
