package manager

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"net"
	"os"
	"os/exec"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/geoip"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Player origin map: the kernel's connection table (conntrack) tells which
// addresses talk to a game port. Valid player connections are those that
// stay busy for a while; server-browser pings and port scanners are gone
// after one exchange. Only the place (country, city) is stored, never the
// address.

const (
	geoEvery     = 20 * time.Second
	geoMinActive = 3                // busy samples before a connection counts
	geoSession   = 30 * time.Minute // a reconnect within this time is the same visit
)

type ctEntry struct {
	proto   string
	src     net.IP
	dport   int
	timeout int
	state   string
	assured bool
}

// parseConntrack reads /proc/net/nf_conntrack or `conntrack -L` lines.
func parseConntrack(b []byte) []ctEntry {
	var out []ctEntry
	sc := bufio.NewScanner(bytes.NewReader(b))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		pi := -1
		for i, x := range f {
			if x == "tcp" || x == "udp" {
				pi = i
				break
			}
			if strings.Contains(x, "=") {
				break
			}
		}
		if pi < 0 || pi+2 >= len(f) {
			continue
		}
		e := ctEntry{proto: f[pi]}
		e.timeout, _ = strconv.Atoi(f[pi+2])
		if e.proto == "tcp" && pi+3 < len(f) && !strings.Contains(f[pi+3], "=") {
			e.state = f[pi+3]
		}
		seen := map[string]bool{}
		for _, x := range f[pi+2:] {
			if x == "[ASSURED]" {
				e.assured = true
				continue
			}
			k, v, ok := strings.Cut(x, "=")
			if !ok || seen[k] {
				continue
			}
			seen[k] = true // only the original direction
			switch k {
			case "src":
				e.src = net.ParseIP(v)
			case "dport":
				e.dport, _ = strconv.Atoi(v)
			}
		}
		if e.src != nil && e.dport > 0 {
			out = append(out, e)
		}
	}
	return out
}

// readConntrack returns the connection table and where it came from.
func readConntrack(ctx context.Context) ([]ctEntry, string, error) {
	if b, err := os.ReadFile("/proc/net/nf_conntrack"); err == nil {
		return parseConntrack(b), "proc", nil
	}
	path, err := exec.LookPath("conntrack")
	if err != nil {
		return nil, "", errors.New("Das Programm conntrack fehlt. Einmal auf dem Server ausführen: apt install conntrack")
	}
	cctx, cancel := context.WithTimeout(ctx, 15*time.Second)
	defer cancel()
	var out []ctEntry
	for _, fam := range []string{"ipv4", "ipv6"} {
		b, err := exec.CommandContext(cctx, path, "-L", "-f", fam, "-o", "extended").Output()
		if err != nil && len(b) == 0 {
			if fam == "ipv4" {
				return nil, "", errors.New("conntrack konnte die Verbindungen nicht lesen (läuft das Panel als root?)")
			}
			continue
		}
		out = append(out, parseConntrack(b)...)
	}
	return out, "conntrack", nil
}

// busy says whether the connection carries traffic right now.
func (e ctEntry) busy(udpMax int) bool {
	if e.proto == "tcp" {
		return e.state == "ESTABLISHED"
	}
	return e.assured && e.timeout >= udpMax-int(geoEvery/time.Second)-5
}

func udpStreamTimeout() int {
	if b, err := os.ReadFile("/proc/sys/net/netfilter/nf_conntrack_udp_timeout_stream"); err == nil {
		if n, err := strconv.Atoi(strings.TrimSpace(string(b))); err == nil && n > 30 {
			return n
		}
	}
	return 120
}

type geoConn struct {
	first, lastBusy time.Time
	busy            int
	counted         bool
	key             string
	loc             geoip.Location
	kind            string
}

type geoTracker struct {
	mu     sync.Mutex
	conns  map[string]*geoConn  // ip|server
	recent map[string]time.Time // ip|server -> last counted visit
	source string
	err    string
}

func newGeoTracker() *geoTracker {
	return &geoTracker{conns: map[string]*geoConn{}, recent: map[string]time.Time{}}
}

// geoStat is the stored, address-free summary per place.
type geoStat struct {
	Country     string    `json:"cc,omitempty"`
	CountryName string    `json:"cn,omitempty"`
	City        string    `json:"city,omitempty"`
	Lat         float64   `json:"lat,omitempty"`
	Lon         float64   `json:"lon,omitempty"`
	Kind        string    `json:"kind,omitempty"` // lan | relay | unknown | ""
	Sessions    int       `json:"sessions"`
	Seconds     int64     `json:"seconds"`
	Last        time.Time `json:"last"`
}

func geoKey(kind string, l geoip.Location) string {
	if kind != "" {
		return kind
	}
	return l.Country + "|" + l.City
}

// GeoEnabled says whether the admin switched the map on.
func (m *Manager) GeoEnabled() bool {
	on := false
	m.st.Read(func(d *store.Data) { on = d.Settings.Geo })
	return on
}

// GeoDB gives the API the database status.
func (m *Manager) GeoDB() *geoip.DB { return m.geo }

// SetGeo switches the origin map on or off. Switching on loads the
// database in the background.
func (m *Manager) SetGeo(on bool) error {
	if err := m.st.Update(func(d *store.Data) error { d.Settings.Geo = on; return nil }); err != nil {
		return err
	}
	if on {
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Minute)
			defer cancel()
			_ = m.geo.Update(ctx, false)
		}()
	} else {
		m.gt.mu.Lock()
		m.gt.conns, m.gt.recent = map[string]*geoConn{}, map[string]time.Time{}
		m.gt.mu.Unlock()
	}
	return nil
}

// runGeo samples connections and keeps the database fresh.
func (m *Manager) runGeo(ctx context.Context) {
	t := time.NewTicker(geoEvery)
	defer t.Stop()
	var lastDB time.Time
	for {
		select {
		case <-ctx.Done():
			return
		case now := <-t.C:
			if !m.GeoEnabled() {
				continue
			}
			if time.Since(lastDB) > 6*time.Hour {
				lastDB = time.Now()
				go func() { _ = m.geo.Update(ctx, false) }()
			}
			m.sampleGeo(ctx, now)
		}
	}
}

func (m *Manager) sampleGeo(ctx context.Context, now time.Time) {
	entries, src, err := readConntrack(ctx)
	m.gt.mu.Lock()
	m.gt.source = src
	m.gt.err = ""
	if err != nil {
		m.gt.err = err.Error()
	}
	m.gt.mu.Unlock()
	if err != nil {
		return
	}
	// host port -> {server, players}, only running servers
	ports := map[string][2]any{}
	m.mu.RLock()
	running := map[string]int{}
	for id, l := range m.live {
		if l != nil && l.Phase == "running" {
			running[id] = l.Players
		}
	}
	m.mu.RUnlock()
	for _, sv := range m.st.Servers() {
		pl, ok := running[sv.ID]
		if !ok || sv.CreatedBy == testCreator {
			continue
		}
		for _, p := range sv.Ports {
			for _, pr := range []string{"tcp", "udp"} {
				if p.Proto == "" || p.Proto == pr || p.Proto == "both" {
					ports[pr+"/"+strconv.Itoa(p.Host)] = [2]any{sv.ID, pl}
				}
			}
		}
	}
	m.recordGeoSample(entries, ports, now, udpStreamTimeout())
}

// recordGeoSample updates the tracker with one connection table snapshot.
// ports maps "udp/2456" to {server id, players (-1 unknown)}.
func (m *Manager) recordGeoSample(entries []ctEntry, ports map[string][2]any, now time.Time, udpMax int) {
	type delta struct {
		key      string
		st       geoStat
		sessions int
		seconds  int64
	}
	deltas := map[string]map[string]*delta{} // server -> key -> delta
	add := func(id string, c *geoConn, sess int, secs int64) {
		if deltas[id] == nil {
			deltas[id] = map[string]*delta{}
		}
		d := deltas[id][c.key]
		if d == nil {
			d = &delta{key: c.key, st: geoStat{Country: c.loc.Country, CountryName: c.loc.CountryName, City: c.loc.City, Lat: c.loc.Lat, Lon: c.loc.Lon, Kind: c.kind}}
			deltas[id][c.key] = d
		}
		d.sessions += sess
		d.seconds += secs
	}
	g := m.gt
	g.mu.Lock()
	seen := map[string]bool{}
	for _, e := range entries {
		t, ok := ports[e.proto+"/"+strconv.Itoa(e.dport)]
		if !ok || !e.busy(udpMax) {
			continue
		}
		id, players := t[0].(string), t[1].(int)
		k := e.src.String() + "|" + id
		if seen[k] {
			continue // several ports of the same game: one player
		}
		seen[k] = true
		c := g.conns[k]
		if c == nil {
			if players == 0 {
				continue // the game says nobody plays: a ping, not a player
			}
			c = &geoConn{first: now}
			g.conns[k] = c
		}
		c.busy++
		c.lastBusy = now
		if !c.counted && c.busy >= geoMinActive && now.Sub(c.first) >= 2*geoEvery {
			c.counted = true
			c.kind = geoip.Kind(e.src)
			if c.kind == "" {
				if l, ok := m.geo.Lookup(e.src); ok {
					c.loc = l
				} else {
					c.kind = "unknown"
				}
			}
			c.key = geoKey(c.kind, c.loc)
			sess := 0
			if last, ok := g.recent[k]; !ok || now.Sub(last) > geoSession {
				sess = 1
			}
			add(id, c, sess, int64(now.Sub(c.first)/time.Second))
		} else if c.counted {
			add(id, c, 0, int64(geoEvery/time.Second))
		}
		if c.counted {
			g.recent[k] = now
		}
	}
	for k, c := range g.conns {
		if now.Sub(c.lastBusy) > 3*time.Minute || (c.busy == 0 && now.Sub(c.first) > 3*time.Minute) {
			delete(g.conns, k)
		}
	}
	for k, t := range g.recent {
		if now.Sub(t) > geoSession {
			delete(g.recent, k)
		}
	}
	g.mu.Unlock()
	if len(deltas) == 0 {
		return
	}
	statsMu.Lock()
	defer statsMu.Unlock()
	for id, ds := range deltas {
		s := m.loadStats(id)
		if s.Geo == nil {
			s.Geo = map[string]*geoStat{}
		}
		for k, d := range ds {
			gs := s.Geo[k]
			if gs == nil {
				st := d.st
				gs = &st
				s.Geo[k] = gs
			}
			gs.Sessions += d.sessions
			gs.Seconds += d.seconds
			gs.Last = now
		}
		m.saveStats(id, s)
	}
}

// GeoPoint is one place on the map.
type GeoPoint struct {
	Country     string    `json:"country"`
	CountryName string    `json:"country_name"`
	City        string    `json:"city"`
	Lat         float64   `json:"lat"`
	Lon         float64   `json:"lon"`
	Sessions    int       `json:"sessions"`
	Seconds     int64     `json:"seconds"`
	Last        time.Time `json:"last"`
}

type GeoCount struct {
	Sessions int   `json:"sessions"`
	Seconds  int64 `json:"seconds"`
}

type GeoCountry struct {
	Country  string `json:"country"`
	Name     string `json:"name"`
	Sessions int    `json:"sessions"`
	Seconds  int64  `json:"seconds"`
}

type GeoLive struct {
	Server      string    `json:"server"`
	City        string    `json:"city,omitempty"`
	Country     string    `json:"country,omitempty"`
	CountryName string    `json:"country_name,omitempty"`
	Lat         float64   `json:"lat,omitempty"`
	Lon         float64   `json:"lon,omitempty"`
	Kind        string    `json:"kind,omitempty"`
	Since       time.Time `json:"since"`
}

type GeoView struct {
	Enabled   bool         `json:"enabled"`
	DB        geoip.Status `json:"db"`
	Source    string       `json:"source,omitempty"`
	SourceErr string       `json:"source_error,omitempty"`
	Points    []GeoPoint   `json:"points"`
	Countries []GeoCountry `json:"countries"`
	LAN       GeoCount     `json:"lan"`
	Relay     GeoCount     `json:"relay"`
	Unknown   GeoCount     `json:"unknown"`
	Live      []GeoLive    `json:"live"`
	Total     int          `json:"total"`
}

// Geo summarizes the origin of players on the given servers.
func (m *Manager) Geo(ids []string) GeoView {
	v := GeoView{Enabled: m.GeoEnabled(), DB: m.geo.Status(), Points: []GeoPoint{}, Countries: []GeoCountry{}, Live: []GeoLive{}}
	want := map[string]bool{}
	for _, id := range ids {
		want[id] = true
	}
	m.gt.mu.Lock()
	v.Source, v.SourceErr = m.gt.source, m.gt.err
	now := time.Now()
	for k, c := range m.gt.conns {
		_, id, _ := strings.Cut(k, "|")
		if !want[id] || !c.counted || now.Sub(c.lastBusy) > 90*time.Second {
			continue
		}
		v.Live = append(v.Live, GeoLive{Server: id, City: c.loc.City, Country: c.loc.Country, CountryName: c.loc.CountryName, Lat: c.loc.Lat, Lon: c.loc.Lon, Kind: c.kind, Since: c.first})
	}
	m.gt.mu.Unlock()
	sort.Slice(v.Live, func(i, j int) bool { return v.Live[i].Since.Before(v.Live[j].Since) })

	points := map[string]*GeoPoint{}
	countries := map[string]*GeoCountry{}
	statsMu.Lock()
	for _, id := range ids {
		s := m.loadStats(id)
		for k, g := range s.Geo {
			switch g.Kind {
			case "lan":
				v.LAN.Sessions += g.Sessions
				v.LAN.Seconds += g.Seconds
				continue
			case "relay":
				v.Relay.Sessions += g.Sessions
				v.Relay.Seconds += g.Seconds
				continue
			case "unknown":
				v.Unknown.Sessions += g.Sessions
				v.Unknown.Seconds += g.Seconds
				continue
			}
			p := points[k]
			if p == nil {
				p = &GeoPoint{Country: g.Country, CountryName: g.CountryName, City: g.City, Lat: g.Lat, Lon: g.Lon}
				points[k] = p
			}
			p.Sessions += g.Sessions
			p.Seconds += g.Seconds
			if g.Last.After(p.Last) {
				p.Last = g.Last
			}
			c := countries[g.Country]
			if c == nil {
				c = &GeoCountry{Country: g.Country, Name: g.CountryName}
				countries[g.Country] = c
			}
			c.Sessions += g.Sessions
			c.Seconds += g.Seconds
		}
	}
	statsMu.Unlock()
	for _, p := range points {
		v.Points = append(v.Points, *p)
		v.Total += p.Sessions
	}
	sort.Slice(v.Points, func(i, j int) bool { return v.Points[i].Sessions > v.Points[j].Sessions })
	for _, c := range countries {
		v.Countries = append(v.Countries, *c)
	}
	sort.Slice(v.Countries, func(i, j int) bool { return v.Countries[i].Sessions > v.Countries[j].Sessions })
	v.Total += v.LAN.Sessions + v.Relay.Sessions + v.Unknown.Sessions
	return v
}
