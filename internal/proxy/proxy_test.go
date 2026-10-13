package proxy

import (
	"crypto/tls"
	"net/http/httptest"
	"testing"
)

func TestClientIP(t *testing.T) {
	Set(nil)
	r := httptest.NewRequest("GET", "/", nil)
	r.RemoteAddr = "192.168.178.50:4711"
	r.Header.Set("X-Forwarded-For", "1.2.3.4")
	r.Header.Set("X-Forwarded-Proto", "https")
	if ClientIP(r) != "192.168.178.50" || HTTPS(r) || Seen() != "192.168.178.50" {
		t.Fatal("ungeprüfter Proxy wurde geglaubt")
	}
	l, err := Parse([]string{"192.168.178.50, 10.10.10.0/24"})
	if err != nil || len(l) != 2 || l[0] != "192.168.178.50" || l[1] != "10.10.10.0/24" {
		t.Fatalf("%v %v", l, err)
	}
	Set(l)
	if ClientIP(r) != "1.2.3.4" || !HTTPS(r) || Seen() != "" {
		t.Fatal(ClientIP(r))
	}
	// a client cannot push a fake address in front: the rightmost untrusted one counts
	r.Header.Set("X-Forwarded-For", "9.9.9.9, 5.6.7.8, 10.10.10.100")
	if ClientIP(r) != "5.6.7.8" {
		t.Fatal(ClientIP(r))
	}
	r.Header.Del("X-Forwarded-For")
	if ClientIP(r) != "192.168.178.50" {
		t.Fatal(ClientIP(r))
	}
	d := httptest.NewRequest("GET", "/", nil)
	d.RemoteAddr = "8.8.8.8:1"
	d.TLS = &tls.ConnectionState{}
	if !HTTPS(d) || ClientIP(d) != "8.8.8.8" {
		t.Fatal("direkt")
	}
	if _, err := Parse([]string{"panel.example"}); err == nil {
		t.Fatal("Name angenommen")
	}
	Set(nil)
}
