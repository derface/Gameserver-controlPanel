package https

import (
	"context"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"io"
	"math/big"
	"net"
	"net/http"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func start(t *testing.T, m *Manager) string {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)
	h := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		io.WriteString(w, "panel tls="+map[bool]string{true: "ja", false: "nein"}[r.TLS != nil])
	})
	go func() { _ = m.Serve(ctx, ln, h) }()
	return ln.Addr().String()
}

func get(t *testing.T, url string) (int, string, string) {
	t.Helper()
	c := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     &http.Transport{TLSClientConfig: &tls.Config{InsecureSkipVerify: true}},
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}
	resp, err := c.Get(url)
	if err != nil {
		t.Fatal(err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	return resp.StatusCode, string(b), resp.Header.Get("Location")
}

func TestOnePort(t *testing.T) {
	m := New(t.TempDir())
	m.HTTPAddr = ""
	if err := m.Apply(store.TLSConfig{}, "play.example.de", "192.168.178.54:8420"); err != nil {
		t.Fatal(err)
	}
	addr := start(t, m)
	if c, body, _ := get(t, "http://"+addr+"/x"); c != 200 || body != "panel tls=nein" {
		t.Fatal(c, body)
	}
	if c, body, _ := get(t, "https://"+addr+"/x"); c != 200 || body != "panel tls=ja" {
		t.Fatal(c, body)
	}
	st := m.Status()
	if st.Subject != "Gamepanel" || !strings.Contains(strings.Join(st.Names, " "), "play.example.de") || !strings.Contains(strings.Join(st.Names, " "), "192.168.178.54") {
		t.Fatalf("%+v", st)
	}

	// with HTTPS switched on, plain HTTP is redirected – but not from the machine itself
	if err := m.Apply(store.TLSConfig{Mode: "self"}, "play.example.de"); err != nil {
		t.Fatal(err)
	}
	if c, _, _ := get(t, "http://"+addr+"/x"); c != 200 {
		t.Fatal("von localhost umgeleitet", c)
	}
	localExempt = false
	defer func() { localExempt = true }()
	if c, _, loc := get(t, "http://"+addr+"/x?a=1"); c != 301 || loc != "https://"+addr+"/x?a=1" {
		t.Fatal(c, loc)
	}
	m.Off = true
	if c, _, _ := get(t, "http://"+addr+"/x"); c != 200 {
		t.Fatal("Notausgang wirkt nicht", c)
	}
}

func selfCert(t *testing.T, cn string, notAfter time.Time) (string, string) {
	k, _ := ecdsa.GenerateKey(elliptic.P256(), rand.Reader)
	tpl := &x509.Certificate{SerialNumber: big.NewInt(7), Subject: pkix.Name{CommonName: cn}, DNSNames: []string{cn},
		NotBefore: time.Now().Add(-48 * time.Hour), NotAfter: notAfter}
	der, err := x509.CreateCertificate(rand.Reader, tpl, tpl, &k.PublicKey, k)
	if err != nil {
		t.Fatal(err)
	}
	kb, _ := x509.MarshalECPrivateKey(k)
	return string(pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: der})), string(pem.EncodeToMemory(&pem.Block{Type: "EC PRIVATE KEY", Bytes: kb}))
}

func TestCustom(t *testing.T) {
	dir := t.TempDir()
	m := New(dir)
	m.HTTPAddr = ""
	crt, key := selfCert(t, "panel.example.de", time.Now().Add(90*24*time.Hour))
	if err := m.SetCustom(crt, "kein schlüssel"); err == nil {
		t.Fatal("kaputter Schlüssel angenommen")
	}
	old, oldKey := selfCert(t, "alt.example.de", time.Now().Add(-time.Hour))
	if err := m.SetCustom(old, oldKey); err == nil {
		t.Fatal("abgelaufenes Zertifikat angenommen")
	}
	if err := m.SetCustom(crt, key); err != nil {
		t.Fatal(err)
	}
	if err := m.Apply(store.TLSConfig{Mode: "custom"}); err != nil {
		t.Fatal(err)
	}
	if st := m.Status(); st.Subject != "panel.example.de" || st.Error != "" {
		t.Fatalf("%+v", st)
	}
	addr := start(t, m)
	conn, err := tls.Dial("tcp", addr, &tls.Config{InsecureSkipVerify: true, ServerName: "panel.example.de"})
	if err != nil {
		t.Fatal(err)
	}
	if cn := conn.ConnectionState().PeerCertificates[0].Subject.CommonName; cn != "panel.example.de" {
		t.Fatal(cn)
	}
	conn.Close()

	// a fresh manager loads the stored certificate again
	m2 := New(dir)
	m2.HTTPAddr = ""
	_ = m2.Apply(store.TLSConfig{Mode: "custom"})
	if m2.Status().Subject != "panel.example.de" {
		t.Fatal("eigenes Zertifikat nach Neustart weg")
	}
}

func TestValidate(t *testing.T) {
	for _, c := range []store.TLSConfig{{Mode: "acme", Domain: "192.168.178.54"}, {Mode: "acme"}, {Mode: "acme", Domain: "localhost"}, {Mode: "x"}, {Mode: "acme", Domain: "a.de", Email: "keine"}} {
		if Validate(c) == nil {
			t.Errorf("%+v angenommen", c)
		}
	}
	if err := Validate(store.TLSConfig{Mode: "acme", Domain: "panel.example.de", Email: "a@b.de"}); err != nil {
		t.Error(err)
	}
}

func TestAcmeFallback(t *testing.T) {
	// Let's Encrypt is unreachable here: the panel must still answer with the
	// self-signed certificate instead of failing the handshake.
	m := New(t.TempDir())
	m.HTTPAddr = ""
	if err := m.Apply(store.TLSConfig{Mode: "acme", Domain: "panel.example.invalid"}); err != nil {
		t.Fatal(err)
	}
	m.acm.Client = nil
	m.acm.HostPolicy = func(context.Context, string) error { return io.EOF } // refuse at once, no network
	c, err := m.GetCertificate(&tls.ClientHelloInfo{ServerName: "panel.example.invalid"})
	if err != nil || c != m.self {
		t.Fatalf("kein Rückfall: %v", err)
	}
	if m.Status().Error == "" {
		t.Fatal("Fehler nicht angezeigt")
	}
}
