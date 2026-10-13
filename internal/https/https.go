// Package https gives the panel built-in HTTPS: a self-signed certificate,
// Let's Encrypt or an own certificate. HTTPS and HTTP share the panel port;
// the first byte of a connection tells them apart (0x16 starts every TLS
// handshake), so plain http:// can be redirected to https:// on the same port.
package https

import (
	"bufio"
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"errors"
	"fmt"
	"github.com/derface/Gameserver-controlPanel/internal/proxy"
	"log"
	"math/big"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/thirdparty/acme"
	"github.com/derface/Gameserver-controlPanel/internal/thirdparty/acme/autocert"
)

type Manager struct {
	dir      string
	Off      bool   // GP_NO_TLS: never redirect to HTTPS (way back in after a bad setting)
	HTTPAddr string // where Let's Encrypt looks for the HTTP-01 challenge, normally ":80"

	mu      sync.RWMutex
	cfg     store.TLSConfig
	hosts   []string // extra names for the self-signed certificate
	self    *tls.Certificate
	custom  *tls.Certificate
	acm     *autocert.Manager
	acmFor  string // domain the acm manager was made for
	http80  *http.Server
	lastErr string
	errAt   time.Time
}

func New(dataDir string) *Manager {
	return &Manager{dir: filepath.Join(dataDir, "tls"), HTTPAddr: ":80"}
}

var domainRe = regexp.MustCompile(`^(?i)[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?(\.[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?)+$`)

// Validate checks a configuration before it is saved.
func Validate(c store.TLSConfig) error {
	switch c.Mode {
	case "", "self", "custom":
	case "acme":
		if net.ParseIP(c.Domain) != nil || !domainRe.MatchString(c.Domain) || !strings.Contains(c.Domain, ".") {
			return errors.New("Für Let's Encrypt brauchst du eine Domain (z. B. panel.example.de), keine IP-Adresse.")
		}
		if c.Email != "" && !strings.Contains(c.Email, "@") {
			return errors.New("Die E-Mail-Adresse sieht nicht richtig aus.")
		}
	default:
		return errors.New("Unbekannte HTTPS-Art")
	}
	return nil
}

// Apply switches to a configuration. hosts are additional names or IPs for the
// self-signed certificate (e.g. the public address of the panel).
func (m *Manager) Apply(c store.TLSConfig, hosts ...string) error {
	if err := Validate(c); err != nil {
		return err
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	m.cfg, m.hosts = c, hosts
	m.lastErr = ""
	if err := m.ensureSelf(); err != nil {
		log.Printf("[HTTPS] selbst signiertes Zertifikat: %v", err)
	}
	m.custom = nil
	if c.Mode == "custom" {
		cert, err := tls.LoadX509KeyPair(filepath.Join(m.dir, "custom.crt"), filepath.Join(m.dir, "custom.key"))
		if err != nil {
			m.lastErr = "Eigenes Zertifikat fehlt oder ist ungültig – das Panel nutzt solange das selbst signierte."
		} else {
			m.custom = &cert
		}
	}
	if c.Mode == "acme" {
		if m.acm == nil || m.acm.Email != c.Email || m.acmFor != c.Domain {
			m.acmFor = c.Domain
			m.acm = &autocert.Manager{
				Prompt:     autocert.AcceptTOS,
				Cache:      autocert.DirCache(filepath.Join(m.dir, "acme")),
				HostPolicy: autocert.HostWhitelist(c.Domain),
				Email:      c.Email,
			}
		}
		m.startHTTP80()
	} else {
		m.acm = nil
		m.stopHTTP80()
	}
	return nil
}

// startHTTP80 answers the Let's Encrypt HTTP-01 challenge and redirects
// everything else on port 80 to HTTPS. Called with m.mu held.
func (m *Manager) startHTTP80() {
	if m.http80 != nil || m.HTTPAddr == "" {
		return
	}
	ln, err := net.Listen("tcp", m.HTTPAddr)
	if err != nil {
		m.lastErr = fmt.Sprintf("Port %s ist nicht frei (%v). Let's Encrypt kann dann nur über Port 443 prüfen.", strings.TrimPrefix(m.HTTPAddr, ":"), err)
		return
	}
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		m.mu.RLock()
		cur := m.acm
		m.mu.RUnlock()
		if cur == nil {
			http.NotFound(w, r)
			return
		}
		cur.HTTPHandler(nil).ServeHTTP(w, r)
	})
	m.http80 = &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second}
	go func(s *http.Server) { _ = s.Serve(ln) }(m.http80)
}

func (m *Manager) stopHTTP80() {
	if m.http80 != nil {
		_ = m.http80.Close()
		m.http80 = nil
	}
}

// Redirect reports whether plain HTTP should be sent to HTTPS.
func (m *Manager) Redirect() bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	return m.cfg.Mode != "" && !m.Off
}

func (m *Manager) fail(err error) {
	m.mu.Lock()
	if time.Since(m.errAt) > 10*time.Second || m.lastErr == "" {
		m.lastErr, m.errAt = err.Error(), time.Now()
		log.Printf("[HTTPS] %v", err)
	}
	m.mu.Unlock()
}

// GetCertificate picks the certificate for a TLS handshake. When the chosen
// one is not available, it falls back to the self-signed certificate so the
// panel always stays reachable.
func (m *Manager) GetCertificate(hello *tls.ClientHelloInfo) (*tls.Certificate, error) {
	m.mu.RLock()
	mode, domain, acm, custom, self := m.cfg.Mode, m.cfg.Domain, m.acm, m.custom, m.self
	m.mu.RUnlock()
	switch {
	case mode == "acme" && acm != nil && strings.EqualFold(hello.ServerName, domain):
		c, err := acm.GetCertificate(hello)
		if err == nil {
			return c, nil
		}
		m.fail(fmt.Errorf("Let's Encrypt: %v", err))
	case mode == "acme" && acm != nil && slices.Contains(hello.SupportedProtos, acme.ALPNProto):
		return acm.GetCertificate(hello) // TLS-ALPN-01 challenge
	case mode == "custom" && custom != nil:
		return custom, nil
	}
	if self == nil {
		return nil, errors.New("kein Zertifikat")
	}
	return self, nil
}

// Renew asks Let's Encrypt for the certificate now (or loads it from the cache).
func (m *Manager) Renew(ctx context.Context) error {
	m.mu.RLock()
	acm, domain := m.acm, m.cfg.Domain
	m.mu.RUnlock()
	if acm == nil {
		return errors.New("Let's Encrypt ist nicht eingestellt.")
	}
	done := make(chan error, 1)
	go func() {
		_, err := acm.GetCertificate(&tls.ClientHelloInfo{
			ServerName:        domain,
			CipherSuites:      []uint16{tls.TLS_ECDHE_ECDSA_WITH_AES_128_GCM_SHA256},
			SupportedCurves:   []tls.CurveID{tls.CurveP256},
			SignatureSchemes:  []tls.SignatureScheme{tls.ECDSAWithP256AndSHA256},
			SupportedVersions: []uint16{tls.VersionTLS13, tls.VersionTLS12},
		})
		done <- err
	}()
	select {
	case err := <-done:
		if err != nil {
			m.fail(fmt.Errorf("Let's Encrypt: %v", err))
			return err
		}
		m.mu.Lock()
		m.lastErr = ""
		m.mu.Unlock()
		return nil
	case <-ctx.Done():
		return errors.New("Let's Encrypt antwortet nicht rechtzeitig. Ist die Domain von außen erreichbar?")
	}
}

// SetCustom stores an own certificate (PEM, with chain) and its key.
func (m *Manager) SetCustom(certPEM, keyPEM string) error {
	cert, err := tls.X509KeyPair([]byte(certPEM), []byte(keyPEM))
	if err != nil {
		return fmt.Errorf("Zertifikat und Schlüssel passen nicht zusammen oder sind kein PEM: %v", err)
	}
	leaf, err := x509.ParseCertificate(cert.Certificate[0])
	if err != nil {
		return err
	}
	if time.Now().After(leaf.NotAfter) {
		return errors.New("Das Zertifikat ist abgelaufen.")
	}
	if err := os.MkdirAll(m.dir, 0o700); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(m.dir, "custom.crt"), []byte(certPEM), 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(m.dir, "custom.key"), []byte(keyPEM), 0o600); err != nil {
		return err
	}
	m.mu.Lock()
	m.custom = &cert
	m.mu.Unlock()
	return nil
}

// Status is what the settings page shows.
type Status struct {
	Mode     string    `json:"mode"`
	Domain   string    `json:"domain"`
	Email    string    `json:"email"`
	Off      bool      `json:"off"` // switched off with GP_NO_TLS
	Subject  string    `json:"subject,omitempty"`
	Issuer   string    `json:"issuer,omitempty"`
	Names    []string  `json:"names,omitempty"`
	NotAfter time.Time `json:"not_after,omitempty"`
	Error    string    `json:"error,omitempty"`
	HasOwn   bool      `json:"has_custom"`
}

func (m *Manager) Status() Status {
	m.mu.RLock()
	defer m.mu.RUnlock()
	s := Status{Mode: m.cfg.Mode, Domain: m.cfg.Domain, Email: m.cfg.Email, Off: m.Off, Error: m.lastErr, HasOwn: m.custom != nil}
	var cert *tls.Certificate
	switch m.cfg.Mode {
	case "custom":
		cert = m.custom
	case "acme":
		if b, err := os.ReadFile(filepath.Join(m.dir, "acme", m.cfg.Domain)); err == nil {
			// autocert's cache holds the key first, then the chain
			for blk, rest := pem.Decode(b); blk != nil; blk, rest = pem.Decode(rest) {
				if blk.Type == "CERTIFICATE" {
					cert = &tls.Certificate{Certificate: [][]byte{blk.Bytes}}
					break
				}
			}
		}
	}
	if cert == nil {
		cert = m.self
	}
	if cert != nil && len(cert.Certificate) > 0 {
		if leaf, err := x509.ParseCertificate(cert.Certificate[0]); err == nil {
			s.Subject, s.Issuer, s.NotAfter = leaf.Subject.CommonName, leaf.Issuer.CommonName, leaf.NotAfter
			s.Names = append(s.Names, leaf.DNSNames...)
			for _, ip := range leaf.IPAddresses {
				s.Names = append(s.Names, ip.String())
			}
		}
	}
	return s
}

// ---------- self-signed ----------

func (m *Manager) selfNames() (dns []string, ips []net.IP) {
	add := func(h string) {
		h = strings.TrimSpace(h)
		if h == "" {
			return
		}
		if host, _, err := net.SplitHostPort(h); err == nil {
			h = host
		}
		if ip := net.ParseIP(h); ip != nil {
			if !slices.ContainsFunc(ips, ip.Equal) {
				ips = append(ips, ip)
			}
		} else if !slices.Contains(dns, strings.ToLower(h)) {
			dns = append(dns, strings.ToLower(h))
		}
	}
	add("localhost")
	if hn, err := os.Hostname(); err == nil {
		add(hn)
	}
	if addrs, err := net.InterfaceAddrs(); err == nil {
		for _, a := range addrs {
			if n, ok := a.(*net.IPNet); ok && !n.IP.IsLinkLocalUnicast() {
				add(n.IP.String())
			}
		}
	}
	for _, h := range m.hosts {
		add(h)
	}
	if m.cfg.Domain != "" {
		add(m.cfg.Domain)
	}
	return
}

// ensureSelf loads the self-signed certificate and makes a new one when it is
// missing, runs out within 30 days or does not cover all current addresses.
// Called with m.mu held.
func (m *Manager) ensureSelf() error {
	crt, key := filepath.Join(m.dir, "self.crt"), filepath.Join(m.dir, "self.key")
	dns, ips := m.selfNames()
	if c, err := tls.LoadX509KeyPair(crt, key); err == nil {
		if leaf, err := x509.ParseCertificate(c.Certificate[0]); err == nil && time.Until(leaf.NotAfter) > 30*24*time.Hour && covers(leaf, dns, ips) {
			m.self = &c
			return nil
		}
	}
	pk, err := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	if err != nil {
		return err
	}
	serial, _ := rand.Int(rand.Reader, new(big.Int).Lsh(big.NewInt(1), 120))
	tpl := &x509.Certificate{
		SerialNumber:          serial,
		Subject:               pkix.Name{CommonName: "Gamepanel", Organization: []string{"Gamepanel (selbst signiert)"}},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().AddDate(0, 0, 397), // browsers refuse longer ones
		KeyUsage:              x509.KeyUsageDigitalSignature,
		ExtKeyUsage:           []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
		BasicConstraintsValid: true,
		DNSNames:              dns,
		IPAddresses:           ips,
	}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &pk.PublicKey, pk)
	if err != nil {
		return err
	}
	kb, err := x509.MarshalECPrivateKey(pk)
	if err != nil {
		return err
	}
	cp := pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})
	kp := pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb})
	if err := os.WriteFile(key, kp, 0o600); err != nil {
		return err
	}
	if err := os.WriteFile(crt, cp, 0o600); err != nil {
		return err
	}
	c, err := tls.X509KeyPair(cp, kp)
	if err != nil {
		return err
	}
	m.self = &c
	return nil
}

func covers(leaf *x509.Certificate, dns []string, ips []net.IP) bool {
	for _, d := range dns {
		if !slices.Contains(leaf.DNSNames, d) {
			return false
		}
	}
	for _, ip := range ips {
		if !slices.ContainsFunc(leaf.IPAddresses, ip.Equal) {
			return false
		}
	}
	return true
}

// ---------- one port for HTTP and HTTPS ----------

// Serve answers HTTPS and HTTP on ln until ctx ends.
func (m *Manager) Serve(ctx context.Context, ln net.Listener, h http.Handler) error {
	m.mu.Lock()
	if m.self == nil {
		_ = os.MkdirAll(m.dir, 0o700)
		if err := m.ensureSelf(); err != nil {
			log.Printf("[HTTPS] %v", err)
		}
	}
	m.mu.Unlock()
	tlsL, plainL := newChanListener(ln.Addr()), newChanListener(ln.Addr())
	tlsSrv := &http.Server{Handler: h, ReadHeaderTimeout: 10 * time.Second, ErrorLog: quietLog(),
		TLSConfig: &tls.Config{GetCertificate: m.GetCertificate, MinVersion: tls.VersionTLS12, NextProtos: []string{"h2", "http/1.1", acme.ALPNProto}}}
	plainSrv := &http.Server{Handler: m.plain(h), ReadHeaderTimeout: 10 * time.Second}
	go func() { _ = tlsSrv.ServeTLS(tlsL, "", "") }()
	go func() { _ = plainSrv.Serve(plainL) }()
	go func() {
		<-ctx.Done()
		_ = ln.Close()
		sctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = tlsSrv.Shutdown(sctx)
		_ = plainSrv.Shutdown(sctx)
		m.mu.Lock()
		m.stopHTTP80()
		m.mu.Unlock()
	}()
	for {
		c, err := ln.Accept()
		if err != nil {
			if ctx.Err() != nil {
				return nil
			}
			var ne net.Error
			if errors.As(err, &ne) && ne.Timeout() {
				time.Sleep(50 * time.Millisecond)
				continue
			}
			return err
		}
		go func(c net.Conn) {
			_ = c.SetReadDeadline(time.Now().Add(10 * time.Second))
			br := bufio.NewReader(c)
			b, err := br.Peek(1)
			if err != nil {
				_ = c.Close()
				return
			}
			_ = c.SetReadDeadline(time.Time{})
			pc := &peekConn{Conn: c, r: br}
			if b[0] == 0x16 {
				tlsL.push(pc)
			} else {
				plainL.push(pc)
			}
		}(c)
	}
}

// plain serves unencrypted requests: redirected to HTTPS when it is switched
// on, except from the machine itself (a way back in via SSH tunnel).
func (m *Manager) plain(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if m.Redirect() && !(localExempt && loopback(r.RemoteAddr)) && !proxy.HTTPS(r) {
			code := http.StatusMovedPermanently
			if r.Method != http.MethodGet && r.Method != http.MethodHead {
				code = http.StatusPermanentRedirect
			}
			http.Redirect(w, r, "https://"+r.Host+r.URL.RequestURI(), code)
			return
		}
		h.ServeHTTP(w, r)
	})
}

var localExempt = true // tests switch it off

func loopback(addr string) bool {
	host, _, err := net.SplitHostPort(addr)
	if err != nil {
		host = addr
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}

// quietLog drops the "TLS handshake error" lines that every browser
// produces while it asks the user about a self-signed certificate.
func quietLog() *log.Logger {
	return log.New(filterWriter{}, "", 0)
}

type filterWriter struct{}

func (filterWriter) Write(b []byte) (int, error) {
	if !strings.Contains(string(b), "TLS handshake error") {
		log.Print(string(b))
	}
	return len(b), nil
}

type peekConn struct {
	net.Conn
	r *bufio.Reader
}

func (c *peekConn) Read(b []byte) (int, error) { return c.r.Read(b) }

type chanListener struct {
	addr net.Addr
	ch   chan net.Conn
	done chan struct{}
	once sync.Once
}

func newChanListener(a net.Addr) *chanListener {
	return &chanListener{addr: a, ch: make(chan net.Conn), done: make(chan struct{})}
}

func (l *chanListener) push(c net.Conn) {
	select {
	case l.ch <- c:
	case <-l.done:
		_ = c.Close()
	}
}

func (l *chanListener) Accept() (net.Conn, error) {
	select {
	case c := <-l.ch:
		return c, nil
	case <-l.done:
		return nil, net.ErrClosed
	}
}

func (l *chanListener) Close() error {
	l.once.Do(func() { close(l.done) })
	return nil
}

func (l *chanListener) Addr() net.Addr { return l.addr }
