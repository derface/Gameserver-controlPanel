// Package reach answers "can players get in from the internet?" with help
// from outside: the public IPv4 of this connection, a DNS lookup that skips
// the local resolver (AdGuard, Fritzbox), a real TCP connect from servers
// abroad (check-host.net) and, for Minecraft Bedrock, a game ping from
// outside (mcsrvstat.us). Nothing here runs on its own: the panel asks only
// when someone presses "Prüfen".
package reach

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"
)

// Services can be replaced in tests.
type Services struct {
	IPURLs    []string // return the caller's IPv4 as plain text
	CheckHost string   // base URL of check-host.net
	McSrv     string   // base URL of api.mcsrvstat.us
	Resolvers []string // public DNS servers, host:port
	HTTP      *http.Client
	Poll      time.Duration
	// LookupFn replaces the DNS lookup in tests.
	LookupFn func(ctx context.Context, host string) (v4, v6 []string, err error)
}

var Default = &Services{
	IPURLs:    []string{"https://api.ipify.org", "https://ipv4.icanhazip.com"},
	CheckHost: "https://check-host.net",
	McSrv:     "https://api.mcsrvstat.us",
	Resolvers: []string{"1.1.1.1:53", "9.9.9.9:53", "8.8.8.8:53"},
	HTTP:      &http.Client{Timeout: 10 * time.Second},
	Poll:      2 * time.Second,
}

const userAgent = "gamepanel-reach/1 (self-hosted game server panel)"

func (s *Services) get(ctx context.Context, u string, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", userAgent)
	resp, err := s.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode != 200 {
		return fmt.Errorf("HTTP %d", resp.StatusCode)
	}
	if p, ok := out.(*string); ok {
		*p = strings.TrimSpace(string(body))
		return nil
	}
	return json.Unmarshal(body, out)
}

// PublicIP returns the IPv4 address the internet sees.
func (s *Services) PublicIP(ctx context.Context) (string, error) {
	var last error
	for _, u := range s.IPURLs {
		var txt string
		if err := s.get(ctx, u, &txt); err != nil {
			last = err
			continue
		}
		if ip := net.ParseIP(txt); ip != nil && ip.To4() != nil {
			return ip.String(), nil
		}
		last = fmt.Errorf("unerwartete Antwort %q", txt)
	}
	return "", fmt.Errorf("Öffentliche IP nicht ermittelbar: %v", last)
}

// Lookup resolves host with public DNS servers (not the local AdGuard/Fritzbox,
// which may answer with an internal address).
func (s *Services) Lookup(ctx context.Context, host string) (v4, v6 []string, err error) {
	if s.LookupFn != nil {
		return s.LookupFn(ctx, host)
	}
	for _, srv := range s.Resolvers {
		r := &net.Resolver{PreferGo: true, Dial: func(ctx context.Context, network, _ string) (net.Conn, error) {
			var d net.Dialer
			return d.DialContext(ctx, network, srv)
		}}
		c, cancel := context.WithTimeout(ctx, 5*time.Second)
		ips, e := r.LookupIPAddr(c, host)
		cancel()
		if e != nil {
			var de *net.DNSError
			if errors.As(e, &de) && de.IsNotFound {
				return nil, nil, fmt.Errorf("%s ist im öffentlichen DNS nicht eingetragen", host)
			}
			err = e
			continue
		}
		for _, ip := range ips {
			if ip.IP.To4() != nil {
				v4 = append(v4, ip.IP.String())
			} else {
				v6 = append(v6, ip.IP.String())
			}
		}
		return v4, v6, nil
	}
	return nil, nil, fmt.Errorf("DNS-Abfrage fehlgeschlagen: %v", err)
}

// TCPResult is the outcome of a connect from outside.
type TCPResult struct {
	Open    bool     // at least one node connected
	Refused bool     // reached the router/host, but nobody accepts on that port
	From    []string // countries of the nodes that connected
	Error   string
}

// CheckTCP asks check-host.net to connect to ip:port from a few countries.
func (s *Services) CheckTCP(ctx context.Context, ip string, port int) TCPResult {
	var start struct {
		OK        int                 `json:"ok"`
		RequestID string              `json:"request_id"`
		Nodes     map[string][]string `json:"nodes"`
		Error     string              `json:"error"`
	}
	u := fmt.Sprintf("%s/check-tcp?host=%s&max_nodes=3", s.CheckHost, url.QueryEscape(fmt.Sprintf("%s:%d", ip, port)))
	if err := s.get(ctx, u, &start); err != nil {
		return TCPResult{Error: "check-host.net nicht erreichbar: " + err.Error()}
	}
	if start.RequestID == "" {
		msg := start.Error
		if msg == "" {
			msg = "keine Prüfung gestartet"
		}
		return TCPResult{Error: "check-host.net: " + msg}
	}
	country := func(node string) string {
		if n := start.Nodes[node]; len(n) > 1 {
			return n[1]
		}
		return node
	}
	var res TCPResult
	for {
		select {
		case <-ctx.Done():
			if res.Error == "" {
				res.Error = "check-host.net hat nicht rechtzeitig geantwortet"
			}
			return res
		case <-time.After(s.Poll):
		}
		var out map[string]json.RawMessage
		if err := s.get(ctx, s.CheckHost+"/check-result/"+url.PathEscape(start.RequestID), &out); err != nil {
			res.Error = "check-host.net: " + err.Error()
			continue
		}
		res = TCPResult{}
		pending, errs := 0, []string{}
		for node, raw := range out {
			var items []map[string]any
			if string(raw) == "null" || json.Unmarshal(raw, &items) != nil || len(items) == 0 || items[0] == nil {
				pending++
				continue
			}
			if _, ok := items[0]["time"]; ok {
				res.Open = true
				if c := country(node); !slices.Contains(res.From, c) {
					res.From = append(res.From, c)
				}
			} else if e, ok := items[0]["error"].(string); ok {
				errs = append(errs, e)
				if strings.Contains(strings.ToLower(e), "refused") {
					res.Refused = true
				}
			}
		}
		if res.Open {
			slices.Sort(res.From)
			return res // one success is enough
		}
		if pending == 0 && len(out) > 0 {
			if len(errs) > 0 {
				res.Error = errs[0]
			}
			return res
		}
	}
}

// BedrockOnline asks mcsrvstat.us whether a Minecraft Bedrock server answers
// at ip:port from the internet.
func (s *Services) BedrockOnline(ctx context.Context, ip string, port int) (bool, error) {
	var out struct {
		Online bool `json:"online"`
	}
	if err := s.get(ctx, fmt.Sprintf("%s/bedrock/3/%s:%d", s.McSrv, ip, port), &out); err != nil {
		return false, fmt.Errorf("mcsrvstat.us nicht erreichbar: %v", err)
	}
	return out.Online, nil
}

// Private reports addresses that are not reachable from the internet
// (home networks, carrier-grade NAT of DS-Lite/mobile connections).
func Private(ip string) bool {
	a := net.ParseIP(ip)
	if a == nil {
		return false
	}
	if a.IsPrivate() || a.IsLoopback() || a.IsLinkLocalUnicast() || a.IsUnspecified() {
		return true
	}
	_, cgn, _ := net.ParseCIDR("100.64.0.0/10")
	return cgn.Contains(a)
}

// Parallel runs fns concurrently and waits for all.
func Parallel(fns ...func()) {
	var wg sync.WaitGroup
	for _, f := range fns {
		wg.Add(1)
		go func() { defer wg.Done(); f() }()
	}
	wg.Wait()
}
