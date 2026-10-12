package manager

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/query"
	"github.com/derface/Gameserver-controlPanel/internal/reach"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/upnp"
	"github.com/derface/Gameserver-controlPanel/internal/upnp/upnptest"
)

// fakeOutside plays api.ipify.org, check-host.net and mcsrvstat.us.
type fakeOutside struct {
	mu      sync.Mutex
	ip      string
	tcp     map[int]string // port → "open" | "refused" | "timeout"
	bedrock bool
	asked   []string
}

func (f *fakeOutside) start(t *testing.T) *reach.Services {
	mux := http.NewServeMux()
	mux.HandleFunc("/ip", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		fmt.Fprintln(w, f.ip)
	})
	mux.HandleFunc("/check-tcp", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		f.asked = append(f.asked, r.URL.Query().Get("host"))
		f.mu.Unlock()
		host := r.URL.Query().Get("host")
		fmt.Fprintf(w, `{"ok":1,"request_id":"%s","nodes":{"de1.node.check-host.net":["de","Germany","Frankfurt","1.2.3.4","AS1"],"pl1.node.check-host.net":["pl","Poland","Warsaw","1.2.3.5","AS2"]}}`, strings.ReplaceAll(host, ":", "_"))
	})
	polls := map[string]int{}
	mux.HandleFunc("/check-result/{id}", func(w http.ResponseWriter, r *http.Request) {
		id := r.PathValue("id")
		var port int
		fmt.Sscanf(id[strings.LastIndex(id, "_")+1:], "%d", &port)
		f.mu.Lock()
		polls[id]++
		first := polls[id] == 1
		mode := f.tcp[port]
		f.mu.Unlock()
		if first { // still running
			fmt.Fprint(w, `{"de1.node.check-host.net":null,"pl1.node.check-host.net":null}`)
			return
		}
		switch mode {
		case "open":
			fmt.Fprint(w, `{"de1.node.check-host.net":[{"time":0.03,"address":"x"}],"pl1.node.check-host.net":[{"error":"Connection timed out"}]}`)
		case "refused":
			fmt.Fprint(w, `{"de1.node.check-host.net":[{"error":"Connection refused"}],"pl1.node.check-host.net":[{"error":"Connection refused"}]}`)
		default:
			fmt.Fprint(w, `{"de1.node.check-host.net":[{"error":"Connection timed out"}],"pl1.node.check-host.net":[{"error":"Connection timed out"}]}`)
		}
	})
	mux.HandleFunc("/bedrock/3/{addr}", func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		fmt.Fprintf(w, `{"online":%v}`, f.bedrock)
	})
	srv := httptest.NewServer(mux)
	t.Cleanup(srv.Close)
	return &reach.Services{IPURLs: []string{srv.URL + "/ip"}, CheckHost: srv.URL, McSrv: srv.URL,
		HTTP: srv.Client(), Poll: 10 * time.Millisecond,
		LookupFn: func(ctx context.Context, host string) ([]string, []string, error) {
			switch host {
			case "play.example.com":
				return []string{"203.0.113.7"}, []string{"2001:db8::1"}, nil
			case "alt.example.com":
				return []string{"198.51.100.9"}, nil, nil
			}
			return nil, nil, errors.New("play.unknown ist im öffentlichen DNS nicht eingetragen")
		}}
}

func TestCheckReach(t *testing.T) {
	m, st := settingsMgr(t)
	r := upnptest.New()
	defer r.Close()
	oldD, oldS, oldA := RouterDiscover, ReachServices, reachAsk
	defer func() { RouterDiscover, ReachServices, reachAsk = oldD, oldS, oldA }()
	RouterDiscover = func(ctx context.Context) (*upnp.Client, error) { return upnp.New(ctx, r.Desc()) }
	out := &fakeOutside{ip: "203.0.113.7", tcp: map[int]string{}}
	ReachServices = out.start(t)
	loop := false
	reachAsk = func(typ, host string, port int) (query.Result, error) {
		if host != "203.0.113.7" || port != 15637 || typ != "a2s" {
			t.Errorf("Abfrage über %s:%d (%s)", host, port, typ)
		}
		if loop {
			return query.Result{}, nil
		}
		return query.Result{}, errors.New("timeout")
	}
	ctx := context.Background()
	_ = st.Update(func(d *store.Data) error {
		d.Settings.PublicHost = "play.example.com"
		d.Settings.UPnP = true
		return nil
	})
	_ = st.SaveServer(store.Server{ID: "ens", Name: "Enshrouded", Template: "enshrouded", ContainerID: "c1",
		Ports: []store.PortMap{{Name: "Spiel", Host: 15636, Proto: "udp"}, {Name: "Abfrage", Host: 15637, Proto: "udp"},
			{Name: "Spiel (TCP)", Host: 15636, Proto: "tcp"}, {Name: "RCON", Host: 25575, Proto: "tcp"}}})
	setLive := func(phase string) {
		m.mu.Lock()
		m.live["ens"] = &live{State: docker.State{Running: phase == "running"}, Phase: phase}
		m.mu.Unlock()
	}
	port := func(rep ReachReport, p int, proto string) ReachPort {
		for _, x := range rep.Ports {
			if x.Port == p && x.Proto == proto {
				return x
			}
		}
		t.Fatalf("Port %d/%s fehlt: %+v", p, proto, rep.Ports)
		return ReachPort{}
	}

	// stopped
	setLive("stopped")
	rep, err := m.CheckReach(ctx, "ens")
	if err != nil || rep.Verdict != "fail" || !strings.Contains(rep.Summary, "läuft gerade nicht") || len(rep.Steps) != 0 {
		t.Fatalf("gestoppt: %+v %v", rep, err)
	}
	if _, err := m.CheckReach(ctx, "nix"); err == nil {
		t.Fatal("unbekannter Server")
	}

	// running, router opens the ports itself; RCON must stay closed
	setLive("running")
	r.Set("UDP/15636", upnp.Mapping{Client: "127.0.0.1", InternalPort: 15636, Enabled: true, Description: "Enshrouded"}) // by hand
	m.SyncRouter(ctx)
	if _, ok := r.Get("TCP/25575"); ok {
		t.Fatal("RCON im Router freigegeben")
	}
	if _, ok := r.Get("TCP/15636"); !ok {
		t.Fatal("Spielport nicht freigegeben")
	}
	out.tcp[15636] = "open"
	rep, _ = m.CheckReach(ctx, "ens")
	if rep.Verdict != "ok" || rep.Address != "play.example.com:15636" || rep.PublicIP != "203.0.113.7" {
		t.Fatalf("erreichbar erwartet: %+v", rep)
	}
	if p := port(rep, 15636, "TCP"); p.Outside != "ok" || !strings.Contains(p.OutsideText, "Germany") || p.RouterText != "Freigegeben (automatisch)" {
		t.Fatalf("TCP: %+v", p)
	}
	if p := port(rep, 15636, "UDP"); p.RouterText != "Freigegeben (von Hand)" || p.Outside != "skip" {
		t.Fatalf("UDP von Hand: %+v", p)
	}
	if p := port(rep, 25575, "TCP"); !p.Internal || p.Outside != "skip" {
		t.Fatalf("RCON: %+v", p)
	}
	for _, a := range out.asked {
		if strings.HasSuffix(a, ":25575") {
			t.Fatal("RCON von außen geprüft")
		}
	}
	if len(rep.Services) != 1 || rep.Services[0] != "check-host.net" {
		t.Fatalf("Dienste %v", rep.Services)
	}

	// TCP refused, no other outside proof, loopback answers → still ok
	out.tcp[15636] = "refused"
	loop = true
	rep, _ = m.CheckReach(ctx, "ens")
	if rep.Verdict != "ok" || port(rep, 15636, "TCP").Outside != "warn" {
		t.Fatalf("refused+loop: %+v", rep)
	}
	// refused and no loopback → likely
	loop = false
	rep, _ = m.CheckReach(ctx, "ens")
	if rep.Verdict != "likely" {
		t.Fatalf("likely erwartet: %+v", rep)
	}
	// TCP timeout → fail
	out.tcp[15636] = "timeout"
	rep, _ = m.CheckReach(ctx, "ens")
	if rep.Verdict != "fail" || !strings.Contains(rep.Summary, "15636 TCP") {
		t.Fatalf("timeout: %+v", rep)
	}
	out.tcp[15636] = "open"

	// forwarding points to another machine
	r.Set("UDP/15637", upnp.Mapping{Client: "192.168.178.19", InternalPort: 15637, Enabled: true})
	rep, _ = m.CheckReach(ctx, "ens")
	if rep.Verdict != "fail" || !strings.Contains(rep.Summary, "192.168.178.19") {
		t.Fatalf("anderes Gerät: %+v", rep)
	}
	r.Set("UDP/15637", upnp.Mapping{Client: "127.0.0.1", InternalPort: 15637, Enabled: true, Description: "gamepanel ens"})

	// DNS points elsewhere
	_ = st.Update(func(d *store.Data) error { d.Settings.PublicHost = "alt.example.com"; return nil })
	rep, _ = m.CheckReach(ctx, "ens")
	if rep.Verdict != "fail" || !strings.Contains(rep.Summary, "198.51.100.9") {
		t.Fatalf("DNS: %+v", rep)
	}
	// no address set: warning only
	_ = st.Update(func(d *store.Data) error { d.Settings.PublicHost = ""; return nil })
	rep, _ = m.CheckReach(ctx, "ens")
	if rep.Verdict != "ok" || rep.Address != "203.0.113.7:15636" || rep.Steps[2].State != "warn" {
		t.Fatalf("ohne Adresse: %+v", rep)
	}

	// DS-Lite: the router only has a carrier-grade NAT address
	r.WAN = "100.72.1.2"
	rep, _ = m.CheckReach(ctx, "ens")
	if rep.Verdict != "fail" || !strings.Contains(rep.Summary, "DS-Lite") {
		t.Fatalf("CGNAT: %+v", rep)
	}
	if p := port(rep, 15636, "TCP"); p.Outside != "skip" {
		t.Fatalf("bei CGNAT trotzdem von außen geprüft: %+v", p)
	}
}

func TestPrivatePort(t *testing.T) {
	for name, want := range map[string]bool{"RCON": true, "Telnet": true, "Web-Admin": true, "ServerQuery": true, "Web Interface": true,
		"Spiel": false, "Query": false, "Abfrage": false, "Web": false, "Dateien": false, "": false,
		"Port für Spiel und RCON": false, "Fernverwaltungs-Port": true, "Admin-Port": true, "Konsolen-Port": true} {
		if privatePort(name) != want {
			t.Errorf("%q: %v", name, !want)
		}
	}
}
