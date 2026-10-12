package reach

import "testing"

func TestPrivate(t *testing.T) {
	for ip, want := range map[string]bool{"192.168.178.1": true, "10.0.0.1": true, "100.64.0.1": true, "100.127.255.1": true,
		"100.128.0.1": false, "149.172.50.15": false, "203.0.113.7": false, "nonsense": false} {
		if Private(ip) != want {
			t.Errorf("%s: %v", ip, !want)
		}
	}
}
