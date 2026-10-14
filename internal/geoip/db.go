package geoip

import (
	"compress/gzip"
	"context"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

// Location is what the panel keeps of an address: never the address itself.
type Location struct {
	Country     string  `json:"country"`      // ISO code, e.g. DE
	CountryName string  `json:"country_name"` // German name if the database has one
	City        string  `json:"city,omitempty"`
	Region      string  `json:"region,omitempty"`
	Lat         float64 `json:"lat"`
	Lon         float64 `json:"lon"`
}

// DB holds the current database and downloads new ones.
type DB struct {
	dir string
	mu  sync.RWMutex
	r   *Reader
	// Status for the UI
	File    string    `json:"file"`
	Built   time.Time `json:"built"`
	Err     string    `json:"error,omitempty"`
	Loading bool      `json:"loading"`
	last    time.Time
}

// own is the file the panel downloads; custom.mmdb (e.g. GeoLite2-City
// renamed) wins if the admin puts one there.
const own, custom = "dbip-city-lite.mmdb", "custom.mmdb"

func New(dir string) *DB {
	d := &DB{dir: dir}
	d.reopen()
	return d
}

func (d *DB) reopen() {
	for _, name := range []string{custom, own} {
		p := filepath.Join(d.dir, name)
		if _, err := os.Stat(p); err != nil {
			continue
		}
		r, err := Open(p)
		if err != nil {
			d.mu.Lock()
			d.Err = name + ": " + err.Error()
			d.mu.Unlock()
			continue
		}
		d.mu.Lock()
		if d.r != nil {
			d.r.Close()
		}
		d.r, d.File, d.Err = r, name, ""
		d.Built = time.Unix(int64(toU64(r.Meta["build_epoch"])), 0)
		d.mu.Unlock()
		return
	}
}

// Ready says whether lookups work.
func (d *DB) Ready() bool {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return d.r != nil
}

// Status is a copy for the API.
type Status struct {
	Ready   bool      `json:"ready"`
	File    string    `json:"file,omitempty"`
	Built   time.Time `json:"built"`
	Err     string    `json:"error,omitempty"`
	Loading bool      `json:"loading"`
	Custom  bool      `json:"custom"`
}

func (d *DB) Status() Status {
	d.mu.RLock()
	defer d.mu.RUnlock()
	return Status{Ready: d.r != nil, File: d.File, Built: d.Built, Err: d.Err, Loading: d.Loading, Custom: d.File == custom}
}

// Lookup finds the location of a public address.
func (d *DB) Lookup(ip net.IP) (Location, bool) {
	d.mu.RLock()
	r := d.r
	d.mu.RUnlock()
	if r == nil {
		return Location{}, false
	}
	m, err := r.Lookup(ip)
	if err != nil || m == nil {
		return Location{}, false
	}
	var l Location
	if c, ok := m["country"].(map[string]any); ok {
		l.Country, _ = c["iso_code"].(string)
		l.CountryName = name(c)
	}
	if c, ok := m["city"].(map[string]any); ok {
		l.City = name(c)
	}
	if subs, ok := m["subdivisions"].([]any); ok && len(subs) > 0 {
		if s, ok := subs[0].(map[string]any); ok {
			l.Region = name(s)
		}
	}
	if loc, ok := m["location"].(map[string]any); ok {
		l.Lat, _ = loc["latitude"].(float64)
		l.Lon, _ = loc["longitude"].(float64)
	}
	if l.CountryName == "" {
		l.CountryName = l.Country
	}
	return l, l.Country != "" || l.Lat != 0 || l.Lon != 0
}

func name(m map[string]any) string {
	n, _ := m["names"].(map[string]any)
	for _, k := range []string{"de", "en"} {
		if s, ok := n[k].(string); ok && s != "" {
			return s
		}
	}
	return ""
}

// URL of the free DB-IP database (CC BY 4.0, no account needed).
func dbipURL(t time.Time) string {
	return fmt.Sprintf("https://download.db-ip.com/free/dbip-city-lite-%s.mmdb.gz", t.Format("2006-01"))
}

var client = &http.Client{Timeout: 15 * time.Minute}

// Update downloads the database if there is none or it is older than
// 35 days. With force it downloads anyway.
func (d *DB) Update(ctx context.Context, force bool) error {
	st := d.Status()
	if st.Custom || st.Loading {
		return nil
	}
	if !force && st.Ready && time.Since(st.Built) < 35*24*time.Hour {
		return nil
	}
	d.mu.Lock()
	if !force && time.Since(d.last) < 6*time.Hour {
		d.mu.Unlock()
		return nil
	}
	d.Loading, d.last = true, time.Now()
	d.mu.Unlock()
	err := d.download(ctx)
	d.mu.Lock()
	d.Loading = false
	if err != nil {
		d.Err = err.Error()
	}
	d.mu.Unlock()
	if err == nil {
		d.reopen()
	}
	return err
}

func (d *DB) download(ctx context.Context) error {
	if err := os.MkdirAll(d.dir, 0o755); err != nil {
		return err
	}
	now := time.Now().UTC()
	var lastErr error
	// the new month's file appears in the first days of the month
	for _, t := range []time.Time{now, now.AddDate(0, -1, 0)} {
		req, _ := http.NewRequestWithContext(ctx, "GET", dbipURL(t), nil)
		req.Header.Set("User-Agent", "Bytewise-Panel")
		res, err := client.Do(req)
		if err != nil {
			lastErr = fmt.Errorf("DB-IP nicht erreichbar: %v", err)
			continue
		}
		if res.StatusCode != 200 {
			res.Body.Close()
			lastErr = fmt.Errorf("DB-IP antwortet %s", res.Status)
			continue
		}
		err = d.save(res.Body)
		res.Body.Close()
		if err != nil {
			return err
		}
		return nil
	}
	return lastErr
}

func (d *DB) save(body io.Reader) error {
	zr, err := gzip.NewReader(body)
	if err != nil {
		return fmt.Errorf("Download kaputt: %v", err)
	}
	tmp := filepath.Join(d.dir, own+".tmp")
	f, err := os.Create(tmp)
	if err != nil {
		return err
	}
	_, err = io.Copy(f, io.LimitReader(zr, 1<<30))
	if cerr := f.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("Download abgebrochen: %v", err)
	}
	r, err := Open(tmp)
	if err != nil {
		os.Remove(tmp)
		return fmt.Errorf("Heruntergeladene Datenbank unlesbar: %v", err)
	}
	r.Close()
	d.mu.Lock()
	if d.r != nil && d.File == own {
		d.r.Close()
		d.r = nil
	}
	d.mu.Unlock()
	return os.Rename(tmp, filepath.Join(d.dir, own))
}

// Kind sorts an address: "lan" for private networks, "relay" for Valve's
// Steam Datagram Relay (the real player hides behind it), "" for a normal
// public address.
func Kind(ip net.IP) string {
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsUnspecified() || cgnat.Contains(ip) {
		return "lan"
	}
	for _, n := range valve {
		if n.Contains(ip) {
			return "relay"
		}
	}
	return ""
}

var cgnat = mustNet("100.64.0.0/10")

// Valve (AS32590) networks used by the Steam relays.
var valve = func() []*net.IPNet {
	var out []*net.IPNet
	for _, s := range strings.Fields(`103.10.124.0/23 103.28.54.0/23 146.66.152.0/21 155.133.224.0/19 162.254.192.0/21
		185.25.180.0/22 190.217.32.0/22 192.69.96.0/22 205.196.6.0/24 208.64.200.0/22 208.78.164.0/22 45.121.184.0/22`) {
		out = append(out, mustNet(s))
	}
	return out
}()

func mustNet(s string) *net.IPNet {
	_, n, err := net.ParseCIDR(s)
	if err != nil {
		panic(err)
	}
	return n
}

// ErrNoDB is returned while no database is there.
var ErrNoDB = errors.New("Noch keine Ortsdatenbank")
