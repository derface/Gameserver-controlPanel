package manager

import (
	"context"
	"strings"
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/upnp"
	"github.com/derface/Gameserver-controlPanel/internal/upnp/upnptest"
)

func TestRouterSync(t *testing.T) {
	m, st := settingsMgr(t)
	r := upnptest.New()
	defer r.Close()
	old := RouterDiscover
	defer func() { RouterDiscover = old }()
	RouterDiscover = func(ctx context.Context) (*upnp.Client, error) { return upnp.New(ctx, r.Desc()) }
	ctx := context.Background()

	_ = st.SaveServer(store.Server{ID: "ens", Name: "Enshrouded", Template: "enshrouded", ContainerID: "c1",
		Ports: []store.PortMap{{Host: 15636, Proto: "udp"}, {Host: 15637, Proto: "udp"}, {Host: 15636, Proto: "tcp"}}})
	_ = st.SaveServer(store.Server{ID: "pal", Name: "Palworld", Template: "palworld", ContainerID: "c2",
		Ports: []store.PortMap{{Host: 8211, Proto: "udp"}}})
	_ = st.SaveServer(store.Server{ID: "zu", Name: "Privat", Template: "valheim", ContainerID: "c3", NoRouter: true,
		Ports: []store.PortMap{{Host: 2456, Proto: "udp"}}})
	run := func(ids ...string) {
		m.mu.Lock()
		for _, id := range []string{"ens", "pal", "zu"} {
			m.live[id] = &live{State: docker.State{Running: false}}
		}
		for _, id := range ids {
			m.live[id] = &live{State: docker.State{Running: true}}
		}
		m.mu.Unlock()
	}

	// switched off: nothing happens
	run("ens", "pal", "zu")
	if s := m.SyncRouter(ctx); s.Enabled || r.Count() != 0 {
		t.Fatalf("aus, aber %d Freigaben", r.Count())
	}
	_ = st.Update(func(d *store.Data) error { d.Settings.UPnP = true; return nil })
	// Palworld's port belongs to another machine (AMP)
	r.Set("UDP/8211", upnp.Mapping{Client: "192.168.178.19", InternalPort: 8211})
	s := m.SyncRouter(ctx)
	if !s.Found || s.Name != "FRITZ!Box Test" || s.ExternalIP != "203.0.113.7" || s.LocalIP != "127.0.0.1" {
		t.Fatalf("%+v", s)
	}
	if _, ok := r.Get("UDP/15637"); !ok || r.Count() != 4 {
		t.Fatalf("Enshrouded nicht freigegeben: %d", r.Count())
	}
	if _, ok := r.Get("UDP/2456"); ok {
		t.Fatal("ausgenommener Server freigegeben")
	}
	var palErr string
	okCount := 0
	for _, p := range s.Ports {
		if p.Server == "pal" {
			palErr = p.Error
		}
		if p.OK {
			okCount++
		}
	}
	if okCount != 3 || !strings.Contains(palErr, "192.168.178.19") {
		t.Fatalf("Status: %+v", s.Ports)
	}
	if in := m.Info(store.Server{ID: "ens", Template: "enshrouded"}); len(in.Router) != 3 {
		t.Fatalf("Info: %+v", in.Router)
	}
	// a manual forwarding to this machine is accepted and never removed
	r.Set("UDP/27015", upnp.Mapping{Client: "127.0.0.1", InternalPort: 27015, Description: "von Hand"})
	_ = st.SaveServer(store.Server{ID: "man", Name: "Hand", Template: "valheim", ContainerID: "c4", Ports: []store.PortMap{{Host: 27015, Proto: "udp"}}})
	m.mu.Lock()
	m.live["man"] = &live{State: docker.State{Running: true}}
	m.mu.Unlock()
	s = m.SyncRouter(ctx)
	for _, p := range s.Ports {
		if p.Server == "man" && (!p.OK || !p.Manual) {
			t.Fatalf("manuelle Freigabe: %+v", p)
		}
	}

	// Enshrouded stops: its ports close, the others stay
	run("pal", "man")
	m.SyncRouter(ctx)
	if _, ok := r.Get("UDP/15636"); ok {
		t.Fatal("Port nach Stopp noch offen")
	}
	if _, ok := r.Get("UDP/8211"); !ok {
		t.Fatal("fremde Freigabe gelöscht")
	}
	m.mu.Lock()
	m.live["man"] = &live{State: docker.State{Running: false}}
	m.mu.Unlock()
	m.SyncRouter(ctx)
	if _, ok := r.Get("UDP/27015"); !ok {
		t.Fatal("manuelle Freigabe gelöscht")
	}

	// switched off: the panel closes what it opened
	run("ens")
	m.SyncRouter(ctx)
	if r.Count() != 5 { // 3 Enshrouded + AMP + manual
		t.Fatalf("%d", r.Count())
	}
	_ = st.Update(func(d *store.Data) error { d.Settings.UPnP = false; return nil })
	m.SyncRouter(ctx)
	if r.Count() != 2 {
		t.Fatalf("nach Ausschalten noch %d", r.Count())
	}
	st.Read(func(d *store.Data) {
		if len(d.Router) != 0 {
			t.Errorf("gemerkt: %+v", d.Router)
		}
	})

	// router refuses permanent forwardings: one hour, renewed
	r.Lease0No = true
	_ = st.Update(func(d *store.Data) error { d.Settings.UPnP = true; return nil })
	m.SyncRouter(ctx)
	if m.rt.lease != 3600 || r.Count() != 5 {
		t.Fatalf("lease %d, %d Freigaben", m.rt.lease, r.Count())
	}

	// router forbids it (Fritzbox without "Selbstständige Portfreigaben")
	r2 := upnptest.New()
	defer r2.Close()
	r2.Deny = true
	m.rt.client = nil
	RouterDiscover = func(ctx context.Context) (*upnp.Client, error) { return upnp.New(ctx, r2.Desc()) }
	s = m.SyncRouter(ctx)
	if len(s.Ports) == 0 || !strings.Contains(s.Ports[0].Error, "Selbstständige Portfreigaben") {
		t.Fatalf("%+v", s.Ports)
	}
}
