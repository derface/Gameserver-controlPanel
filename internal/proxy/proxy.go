// Package proxy knows which reverse proxies (Traefik, nginx, Caddy …) the
// panel trusts. Only their X-Forwarded-For and X-Forwarded-Proto headers are
// believed; anyone else could send them to hide or fake an address.
package proxy

import (
	"errors"
	"net"
	"net/http"
	"net/netip"
	"strings"
	"sync"
	"sync/atomic"
	"time"
)

var (
	trusted atomic.Pointer[[]netip.Prefix]
	seenMu  sync.Mutex
	seenIP  string
	seenAt  time.Time
)

// Parse checks a list of IPs or networks (CIDR) and returns them cleaned up.
func Parse(in []string) ([]string, error) {
	var out []string
	for _, s := range in {
		for _, x := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == ';' || r == '\n' }) {
			p, err := prefix(x)
			if err != nil {
				return nil, errors.New("„" + x + "“ ist keine IP-Adresse und kein Netz (z. B. 192.168.178.50 oder 10.10.10.0/24)")
			}
			if p.IsSingleIP() {
				out = append(out, p.Addr().String())
			} else {
				out = append(out, p.String())
			}
		}
	}
	if len(out) > 20 {
		return nil, errors.New("Höchstens 20 Einträge")
	}
	return out, nil
}

func prefix(s string) (netip.Prefix, error) {
	s = strings.TrimSpace(s)
	if strings.Contains(s, "/") {
		p, err := netip.ParsePrefix(s)
		return p.Masked(), err
	}
	a, err := netip.ParseAddr(s)
	if err != nil {
		return netip.Prefix{}, err
	}
	a = a.Unmap()
	return netip.PrefixFrom(a, a.BitLen()), nil
}

// Set replaces the trusted proxies (invalid entries are skipped).
func Set(list []string) {
	var ps []netip.Prefix
	for _, s := range list {
		if p, err := prefix(s); err == nil {
			ps = append(ps, p)
		}
	}
	trusted.Store(&ps)
}

func host(addr string) string {
	h, _, err := net.SplitHostPort(addr)
	if err != nil {
		return addr
	}
	return h
}

func isTrusted(ip string) bool {
	ps := trusted.Load()
	if ps == nil || len(*ps) == 0 {
		return false
	}
	a, err := netip.ParseAddr(strings.TrimSpace(ip))
	if err != nil {
		return false
	}
	a = a.Unmap()
	for _, p := range *ps {
		if p.Contains(a) {
			return true
		}
	}
	return false
}

// Trusted says whether the request came straight from a trusted proxy.
func Trusted(r *http.Request) bool { return isTrusted(host(r.RemoteAddr)) }

// ClientIP is the address of the browser: the connection's address, or, if
// that is a trusted proxy, the last untrusted entry of X-Forwarded-For.
func ClientIP(r *http.Request) string {
	h := host(r.RemoteAddr)
	xff := r.Header.Values("X-Forwarded-For")
	if !isTrusted(h) {
		if len(xff) > 0 || r.Header.Get("X-Real-Ip") != "" {
			seenMu.Lock()
			seenIP, seenAt = h, time.Now()
			seenMu.Unlock()
		}
		return h
	}
	var hops []string
	for _, v := range xff {
		hops = append(hops, strings.Split(v, ",")...)
	}
	for i := len(hops) - 1; i >= 0; i-- {
		ip := strings.TrimSpace(hops[i])
		if _, err := netip.ParseAddr(ip); err != nil {
			break
		}
		if !isTrusted(ip) {
			return ip
		}
		h = ip
	}
	if ip := strings.TrimSpace(r.Header.Get("X-Real-Ip")); len(hops) == 0 && ip != "" {
		if _, err := netip.ParseAddr(ip); err == nil {
			return ip
		}
	}
	return h
}

// HTTPS says whether the browser talks HTTPS (directly or to the proxy).
func HTTPS(r *http.Request) bool {
	if r.TLS != nil {
		return true
	}
	return Trusted(r) && strings.EqualFold(r.Header.Get("X-Forwarded-Proto"), "https")
}

// Seen returns an untrusted address that recently sent proxy headers (a
// proxy the admin has not entered yet).
func Seen() string {
	seenMu.Lock()
	defer seenMu.Unlock()
	if time.Since(seenAt) > 24*time.Hour || isTrusted(seenIP) {
		return ""
	}
	return seenIP
}
