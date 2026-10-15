// Package nodes lets one panel control the servers of other machines that
// run Bytewise Panel too. The other panel ("Rechner", node) hands out a key;
// this panel asks it every few seconds for its servers and passes all
// requests for those servers on, with the key and the name of the user.
//
// Server ids from a node look like "<node id>~<server id>" here.
package nodes

import (
	"bytes"
	"context"
	"crypto/rand"
	"crypto/sha256"
	"crypto/tls"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Sep separates node id and server id.
const Sep = "~"

// Split splits "<node>~<server>".
func Split(id string) (node, server string, ok bool) {
	node, server, ok = strings.Cut(id, Sep)
	return node, server, ok && node != "" && server != ""
}

// Status is what the panel knows about a node right now.
type Status struct {
	ID       string           `json:"id"`
	Name     string           `json:"name"`
	URL      string           `json:"url"`
	FP       string           `json:"fp,omitempty"`
	Added    time.Time        `json:"added"`
	Online   bool             `json:"online"`
	Error    string           `json:"error,omitempty"`
	Version  string           `json:"version,omitempty"`
	Hostname string           `json:"hostname,omitempty"`
	Seen     time.Time        `json:"seen,omitempty"`
	Host     json.RawMessage  `json:"host,omitempty"`
	Docker   string           `json:"docker_error,omitempty"`
	Count    int              `json:"servers"`
	servers  []map[string]any // as the node sent them
}

// Hello is the node's answer when a panel connects.
type Hello struct {
	Version  string `json:"version"`
	Hostname string `json:"hostname"`
	Servers  int    `json:"servers"`
}

// Pool keeps the connections to all nodes.
type Pool struct {
	st      *store.Store
	mu      sync.Mutex
	status  map[string]*Status
	clients map[string]*http.Client
	keys    map[string]string // node id → url|token|fp the client was built for
	wake    chan struct{}
}

func New(st *store.Store) *Pool {
	return &Pool{st: st, status: map[string]*Status{}, clients: map[string]*http.Client{}, keys: map[string]string{}, wake: make(chan struct{}, 1)}
}

// Nodes returns the configured nodes.
func (p *Pool) Nodes() []store.Node {
	var out []store.Node
	p.st.Read(func(d *store.Data) { out = append(out, d.Nodes...) })
	return out
}

// Get returns one node.
func (p *Pool) Get(id string) (store.Node, bool) {
	for _, n := range p.Nodes() {
		if n.ID == id {
			return n, true
		}
	}
	return store.Node{}, false
}

// Run asks all nodes for their state every 5 seconds.
func (p *Pool) Run(ctx context.Context) {
	t := time.NewTicker(5 * time.Second)
	defer t.Stop()
	for {
		p.pollAll(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-p.wake:
		}
	}
}

// Refresh asks the nodes again right away (after adding one, for example).
func (p *Pool) Refresh() {
	select {
	case p.wake <- struct{}{}:
	default:
	}
}

func (p *Pool) pollAll(ctx context.Context) {
	nodes := p.Nodes()
	var wg sync.WaitGroup
	for _, n := range nodes {
		wg.Add(1)
		go func(n store.Node) {
			defer wg.Done()
			p.poll(ctx, n)
		}(n)
	}
	wg.Wait()
	p.mu.Lock()
	for id := range p.status { // removed nodes
		found := false
		for _, n := range nodes {
			found = found || n.ID == id
		}
		if !found {
			delete(p.status, id)
		}
	}
	p.mu.Unlock()
}

func (p *Pool) poll(ctx context.Context, n store.Node) {
	ctx, cancel := context.WithTimeout(ctx, 4*time.Second)
	defer cancel()
	var state struct {
		Version string `json:"version"`
		Docker  string `json:"docker_error"`
	}
	var hello Hello
	var servers []map[string]any
	var host json.RawMessage
	err := p.getJSON(ctx, n, "/api/node/hello", &hello)
	if err == nil {
		err = p.getJSON(ctx, n, "/api/servers", &servers)
	}
	if err == nil {
		_ = p.getJSON(ctx, n, "/api/state", &state)
		_ = p.getJSON(ctx, n, "/api/host", &host)
	}
	p.mu.Lock()
	defer p.mu.Unlock()
	s := p.status[n.ID]
	if s == nil {
		s = &Status{}
		p.status[n.ID] = s
	}
	s.ID, s.Name, s.URL, s.FP, s.Added = n.ID, n.Name, n.URL, n.FP, n.Added
	if err != nil {
		s.Online, s.Error = false, err.Error()
		return
	}
	s.Online, s.Error, s.Seen = true, "", time.Now()
	s.Version, s.Hostname, s.Docker, s.Host = hello.Version, hello.Hostname, state.Docker, host
	s.servers, s.Count = servers, len(servers)
}

// Status lists all nodes with their state.
func (p *Pool) Status() []Status {
	nodes := p.Nodes()
	p.mu.Lock()
	defer p.mu.Unlock()
	out := make([]Status, 0, len(nodes))
	for _, n := range nodes {
		if s, ok := p.status[n.ID]; ok {
			c := *s
			c.Name, c.URL = n.Name, n.URL
			out = append(out, c)
		} else {
			out = append(out, Status{ID: n.ID, Name: n.Name, URL: n.URL, FP: n.FP, Added: n.Added, Error: "wird abgefragt"})
		}
	}
	return out
}

// Servers returns the servers of all nodes with their ids rewritten. Servers
// of a node that does not answer stay in the list, marked as unreachable.
func (p *Pool) Servers() []map[string]any {
	nodes := p.Nodes()
	p.mu.Lock()
	defer p.mu.Unlock()
	var out []map[string]any
	for _, n := range nodes {
		s := p.status[n.ID]
		if s == nil {
			continue
		}
		for _, sv := range s.servers {
			c := make(map[string]any, len(sv)+3)
			for k, v := range sv {
				c[k] = v
			}
			if id, _ := sv["id"].(string); id != "" {
				c["id"] = n.ID + Sep + id
			}
			c["node"], c["node_name"] = n.ID, n.Name
			if !s.Online {
				c["status"], c["message"], c["cpu"], c["mem_mb"], c["players"] = "error", "Rechner „"+n.Name+"“ antwortet nicht", 0, 0, -1
				c["node_offline"] = true
			}
			out = append(out, c)
		}
	}
	return out
}

// client returns the HTTP client for a node: with a pinned certificate for
// https, so a self-signed certificate works but a different one does not.
func (p *Pool) client(n store.Node) *http.Client {
	key := n.URL + "|" + n.Token + "|" + n.FP
	p.mu.Lock()
	defer p.mu.Unlock()
	if c, ok := p.clients[n.ID]; ok && p.keys[n.ID] == key {
		return c
	}
	c := newClient(n.FP, nil)
	p.clients[n.ID], p.keys[n.ID] = c, key
	return c
}

func newClient(fp string, seen *string) *http.Client {
	tr := http.DefaultTransport.(*http.Transport).Clone()
	tr.TLSClientConfig = &tls.Config{
		InsecureSkipVerify: true, // checked below against the pinned fingerprint
		VerifyConnection: func(cs tls.ConnectionState) error {
			if len(cs.PeerCertificates) == 0 {
				return errors.New("kein Zertifikat")
			}
			got := Fingerprint(cs.PeerCertificates[0].Raw)
			if seen != nil {
				*seen = got
				return nil
			}
			if fp == "" || got != fp {
				return fmt.Errorf("Zertifikat des Rechners hat sich geändert (erwartet %s, bekommen %s). Rechner entfernen und neu hinzufügen, wenn das so gewollt ist", short(fp), short(got))
			}
			return nil
		},
	}
	tr.ResponseHeaderTimeout = 0
	return &http.Client{Transport: tr, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
}

// Fingerprint is the sha256 of a certificate, as hex.
func Fingerprint(der []byte) string {
	s := sha256.Sum256(der)
	return hex.EncodeToString(s[:])
}

func short(fp string) string {
	if len(fp) > 16 {
		return fp[:16] + "…"
	}
	return fp
}

func (p *Pool) getJSON(ctx context.Context, n store.Node, path string, v any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", strings.TrimRight(n.URL, "/")+path, nil)
	if err != nil {
		return err
	}
	authorize(req, n.Token, "")
	resp, err := p.client(n).Do(req)
	if err != nil {
		return friendly(err)
	}
	defer resp.Body.Close()
	return decodeAnswer(resp, v)
}

func authorize(req *http.Request, token, user string) {
	req.Header.Set("Authorization", "Bearer "+token)
	req.Header.Set("X-Panel", "1")
	if user != "" {
		req.Header.Set("X-Node-User", user)
	} else {
		req.Header.Del("X-Node-User")
	}
}

func decodeAnswer(resp *http.Response, v any) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if resp.StatusCode == http.StatusUnauthorized {
		return errors.New("Der Rechner kennt den Schlüssel nicht (mehr). Dort einen neuen erzeugen und den Rechner hier neu hinzufügen")
	}
	if resp.StatusCode >= 300 {
		var e struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(b, &e) == nil && e.Error != "" {
			return errors.New(e.Error)
		}
		return fmt.Errorf("Antwort %d", resp.StatusCode)
	}
	if err := json.Unmarshal(b, v); err != nil {
		return errors.New("Das ist kein Bytewise Panel (oder eine zu alte Version)")
	}
	return nil
}

func friendly(err error) error {
	s := err.Error()
	switch {
	case strings.Contains(s, "Zertifikat des Rechners"):
		if i := strings.Index(s, "Zertifikat des Rechners"); i >= 0 {
			return errors.New(s[i:])
		}
	case strings.Contains(s, "connection refused"):
		return errors.New("Verbindung abgelehnt: läuft das Panel dort, stimmt der Port?")
	case strings.Contains(s, "no such host"):
		return errors.New("Adresse unbekannt")
	case strings.Contains(s, "deadline exceeded"), strings.Contains(s, "timeout"):
		return errors.New("keine Antwort (Zeitüberschreitung)")
	case strings.Contains(s, "server gave HTTP response to HTTPS client"):
		return errors.New("Der Rechner spricht nur HTTP: Adresse mit http:// angeben")
	}
	return err
}

// Code is what the node shows for pairing: address and key in one string.
type Code struct {
	URL   string `json:"u"`
	Token string `json:"t"`
}

// EncodeCode builds the pairing code.
func EncodeCode(c Code) string {
	b, _ := json.Marshal(c)
	return "gpnode:" + base64.RawURLEncoding.EncodeToString(b)
}

// DecodeCode reads a pairing code.
func DecodeCode(s string) (Code, error) {
	s = strings.TrimSpace(s)
	raw, ok := strings.CutPrefix(s, "gpnode:")
	b, err := base64.RawURLEncoding.DecodeString(raw)
	var c Code
	if !ok || err != nil || json.Unmarshal(b, &c) != nil || c.URL == "" || !strings.HasPrefix(c.Token, "gpn_") {
		return c, errors.New("Das ist kein gültiger Kopplungscode. Er beginnt mit „gpnode:“")
	}
	return c, nil
}

// CleanURL checks and normalizes a node address.
func CleanURL(s string) (string, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "/")
	if !strings.Contains(s, "://") {
		s = "https://" + s
	}
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || strings.HasSuffix(u.Host, ":") || u.User != nil || (u.Path != "" && u.Path != "/") || u.RawQuery != "" {
		return "", errors.New("Adresse bitte als https://rechner:8420 angeben")
	}
	return u.Scheme + "://" + u.Host, nil
}

// Probe connects to a node for the first time: it notes the certificate
// (trust on first use) and checks the key.
func Probe(ctx context.Context, rawURL, token string) (fp string, h Hello, err error) {
	ctx, cancel := context.WithTimeout(ctx, 8*time.Second)
	defer cancel()
	c := newClient("", &fp)
	req, err := http.NewRequestWithContext(ctx, "GET", rawURL+"/api/node/hello", nil)
	if err != nil {
		return "", h, err
	}
	authorize(req, token, "")
	resp, err := c.Do(req)
	if err != nil {
		return "", h, friendly(err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusPermanentRedirect || resp.StatusCode == http.StatusMovedPermanently || resp.StatusCode == http.StatusFound || resp.StatusCode == http.StatusTemporaryRedirect {
		return "", h, errors.New("Der Rechner leitet auf HTTPS um: Adresse mit https:// angeben")
	}
	if err := decodeAnswer(resp, &h); err != nil {
		return "", h, err
	}
	if !strings.HasPrefix(rawURL, "https://") {
		fp = ""
	}
	return fp, h, nil
}

// NewID makes a short node id.
func NewID() string {
	b := make([]byte, 3)
	_, _ = rand.Read(b)
	return "n" + hex.EncodeToString(b)
}

// Proxy passes a request for a node's server on. path is the path on the
// node (with the node's own server id). The ids in JSON answers are
// rewritten back to "<node>~<id>".
func (p *Pool) Proxy(w http.ResponseWriter, r *http.Request, n store.Node, path, serverID, user string) {
	target, err := url.Parse(strings.TrimRight(n.URL, "/"))
	if err != nil {
		http.Error(w, "Rechner-Adresse ungültig", http.StatusBadGateway)
		return
	}
	rp := &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.Out.URL.Scheme, pr.Out.URL.Host = target.Scheme, target.Host
			pr.Out.URL.Path, pr.Out.URL.RawPath = path, ""
			pr.Out.URL.RawQuery = r.URL.RawQuery
			pr.Out.Host = target.Host
			pr.Out.Header.Del("Cookie")
			pr.Out.Header.Del("X-Forwarded-For")
			authorize(pr.Out, n.Token, user)
		},
		Transport:     p.client(n).Transport,
		FlushInterval: -1,
		ModifyResponse: func(resp *http.Response) error {
			resp.Header.Del("Set-Cookie")
			if serverID == "" || !strings.HasPrefix(resp.Header.Get("Content-Type"), "application/json") || resp.ContentLength > 8<<20 {
				return nil
			}
			b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20+1))
			resp.Body.Close()
			if err != nil {
				return err
			}
			b = RewriteIDs(b, serverID, n.ID+Sep+serverID)
			resp.Body = io.NopCloser(bytes.NewReader(b))
			resp.ContentLength = int64(len(b))
			resp.Header.Set("Content-Length", fmt.Sprint(len(b)))
			return nil
		},
		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			w.Header().Set("Content-Type", "application/json; charset=utf-8")
			w.WriteHeader(http.StatusBadGateway)
			_ = json.NewEncoder(w).Encode(map[string]string{"error": "Rechner „" + n.Name + "“ nicht erreichbar: " + friendly(err).Error()})
		},
	}
	rp.ServeHTTP(w, r)
}

// RewriteIDs replaces the node's server id in JSON by the id used here.
func RewriteIDs(b []byte, from, to string) []byte {
	for _, k := range []string{"id", "server", "server_id"} {
		b = bytes.ReplaceAll(b, []byte(`"`+k+`":"`+from+`"`), []byte(`"`+k+`":"`+to+`"`))
	}
	return b
}

// Do sends one request to a node and returns the answer as JSON.
func (p *Pool) Do(ctx context.Context, n store.Node, method, path string, body []byte, user string) (int, []byte, error) {
	req, err := http.NewRequestWithContext(ctx, method, strings.TrimRight(n.URL, "/")+path, bytes.NewReader(body))
	if err != nil {
		return 0, nil, err
	}
	authorize(req, n.Token, user)
	req.Header.Set("Content-Type", "application/json")
	resp, err := p.client(n).Do(req)
	if err != nil {
		return 0, nil, fmt.Errorf("Rechner „%s“ nicht erreichbar: %w", n.Name, friendly(err))
	}
	defer resp.Body.Close()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, b, err
}

// ServerName finds the name of a node's server for the protocol.
func (p *Pool) ServerName(id string) string {
	for _, s := range p.Servers() {
		if s["id"] == id {
			if n, _ := s["name"].(string); n != "" {
				return n
			}
		}
	}
	return id
}

// PollNode asks one node right away, e.g. after creating a server there, so
// the list here already has it.
func (p *Pool) PollNode(ctx context.Context, n store.Node) { p.poll(ctx, n) }
