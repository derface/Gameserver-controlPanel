package upnp_test

import (
	"context"
	"strings"
	"testing"

	. "github.com/derface/Gameserver-controlPanel/internal/upnp"
	"github.com/derface/Gameserver-controlPanel/internal/upnp/upnptest"
)

func TestClient(t *testing.T) {
	f := upnptest.New()
	defer f.Close()
	ctx := context.Background()
	c, err := New(ctx, f.Desc())
	if err != nil {
		t.Fatal(err)
	}
	if c.Name != "FRITZ!Box Test" || c.LocalIP != "127.0.0.1" || !strings.HasSuffix(c.ControlURL, "/igdupnp/control/WANIPConn1") {
		t.Fatalf("%+v", c)
	}
	if ip, err := c.ExternalIP(ctx); err != nil || ip != "203.0.113.7" {
		t.Fatal(ip, err)
	}
	if _, err := c.Get(ctx, 15636, "udp"); Code(err) != 714 {
		t.Fatalf("leer: %v", err)
	}
	if err := c.Add(ctx, 15636, "udp", "gamepanel x", 0); err != nil {
		t.Fatal(err)
	}
	m, err := c.Get(ctx, 15636, "UDP")
	if err != nil || m.Client != "127.0.0.1" || m.InternalPort != 15636 || m.Description != "gamepanel x" {
		t.Fatalf("%+v %v", m, err)
	}
	f.Set("TCP/8211", Mapping{Client: "192.168.178.19", InternalPort: 8211})
	if err := c.Add(ctx, 8211, "tcp", "gamepanel y", 0); Code(err) != 718 || err.Error() == "" {
		t.Fatalf("Konflikt: %v", err)
	}
	f.Deny = true
	if err := c.Add(ctx, 2456, "udp", "gamepanel z", 0); Code(err) != 606 {
		t.Fatalf("verweigert: %v", err)
	}
	if err := c.Delete(ctx, 15636, "udp"); err != nil {
		t.Fatal(err)
	}
	if f.Count() != 1 {
		t.Fatal(f.Count())
	}
}
