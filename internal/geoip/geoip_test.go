package geoip

import (
	"net"
	"os"
	"path/filepath"
	"testing"
)

func TestLookup(t *testing.T) {
	for _, f := range []string{"test.mmdb", "test28.mmdb", "test32.mmdb"} {
		t.Run(f, func(t *testing.T) { lookupFile(t, f) })
	}
}

func lookupFile(t *testing.T, file string) {
	dir := t.TempDir()
	b, err := os.ReadFile("testdata/" + file)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, custom), b, 0o644); err != nil {
		t.Fatal(err)
	}
	d := New(dir)
	if !d.Ready() {
		t.Fatalf("not ready: %+v", d.Status())
	}
	cases := map[string]string{"81.0.3.4": "Berlin/DE/Deutschland", "83.0.200.1": "Wien/AT/Österreich", "85.0.0.9": "São Paulo/BR/Brasilien", "2001:db8::1": "Tokio/JP/Japan"}
	for ip, want := range cases {
		l, ok := d.Lookup(net.ParseIP(ip))
		if !ok || l.City+"/"+l.Country+"/"+l.CountryName != want {
			t.Errorf("%s: got %+v %v, want %s", ip, l, ok, want)
		}
		if l.Lat == 0 || l.Lon == 0 {
			t.Errorf("%s: no coordinates", ip)
		}
	}
	if _, ok := d.Lookup(net.ParseIP("9.9.9.9")); ok {
		t.Error("unknown address found")
	}
}

func TestKind(t *testing.T) {
	for ip, want := range map[string]string{"192.168.178.20": "lan", "10.0.0.1": "lan", "100.70.1.1": "lan", "155.133.230.10": "relay", "162.254.193.5": "relay", "81.0.0.1": "", "::1": "lan"} {
		if got := Kind(net.ParseIP(ip)); got != want {
			t.Errorf("%s: %q, want %q", ip, got, want)
		}
	}
}
