package manager

import (
	"context"
	"fmt"
	"log"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/upnp"
)

// Automatic port forwarding: while a server runs, its ports are open in the
// router (UPnP); when it stops or is deleted, they are closed again. Only
// forwardings the panel created itself are ever removed.

// RouterPort is the state of one forwarded port.
type RouterPort struct {
	Server string `json:"server"`
	Name   string `json:"name"`
	Port   int    `json:"port"`
	Proto  string `json:"proto"`
	OK     bool   `json:"ok"`
	Manual bool   `json:"manual,omitempty"` // forwarded by hand to this machine, the panel leaves it alone
	Error  string `json:"error,omitempty"`
}

// RouterState is shown in the settings.
type RouterState struct {
	Enabled    bool         `json:"enabled"`
	Found      bool         `json:"found"`
	Name       string       `json:"name,omitempty"`
	LocalIP    string       `json:"local_ip,omitempty"`
	ExternalIP string       `json:"external_ip,omitempty"`
	Error      string       `json:"error,omitempty"`
	Checked    time.Time    `json:"checked"`
	Ports      []RouterPort `json:"ports"`
}

type routerCtl struct {
	mu     sync.Mutex // one sync at a time
	smu    sync.RWMutex
	client *upnp.Client
	lease  int // 0 = permanent; some routers want a lease, then we renew
	state  RouterState
	kick   chan struct{}
}

// RouterDiscover finds the router; replaced in tests.
var RouterDiscover = func(ctx context.Context) (*upnp.Client, error) { return upnp.Discover(ctx, 3*time.Second) }

var routerEvery = 2 * time.Minute

func (m *Manager) routerInit() {
	m.rt.kick = make(chan struct{}, 1)
	m.rt.state.Ports = []RouterPort{}
}

// KickRouter asks for a sync soon (after starts, stops, deletes, settings).
func (m *Manager) KickRouter() {
	select {
	case m.rt.kick <- struct{}{}:
	default:
	}
}

func (m *Manager) runRouter(ctx context.Context) {
	t := time.NewTicker(routerEvery)
	defer t.Stop()
	time.Sleep(5 * time.Second) // let the first status refresh happen
	for {
		m.SyncRouter(ctx)
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		case <-m.rt.kick:
			// containers need a moment until Docker reports them running
			select {
			case <-ctx.Done():
				return
			case <-time.After(4 * time.Second):
			}
		}
	}
}

// RouterStatus returns the last known state.
func (m *Manager) RouterStatus() RouterState {
	m.rt.smu.RLock()
	defer m.rt.smu.RUnlock()
	s := m.rt.state
	s.Ports = slices.Clone(s.Ports)
	if s.Ports == nil {
		s.Ports = []RouterPort{}
	}
	return s
}

func (m *Manager) routerPorts(id string) []RouterPort {
	m.rt.smu.RLock()
	defer m.rt.smu.RUnlock()
	var out []RouterPort
	for _, p := range m.rt.state.Ports {
		if p.Server == id {
			out = append(out, p)
		}
	}
	return out
}

type portKey struct {
	Port  int
	Proto string
}

// SyncRouter brings the router in line with the running servers.
func (m *Manager) SyncRouter(ctx context.Context) RouterState {
	m.rt.mu.Lock()
	defer m.rt.mu.Unlock()
	ctx, cancel := context.WithTimeout(ctx, 60*time.Second)
	defer cancel()

	var enabled bool
	var tracked []store.RouterMap
	m.st.Read(func(d *store.Data) { enabled, tracked = d.Settings.UPnP, slices.Clone(d.Router) })

	// what should be open
	want := map[portKey]store.Server{}
	if enabled {
		for _, sv := range m.st.Servers() {
			if sv.NoRouter || sv.CreatedBy == testCreator || !m.containerRunning(sv.ID) {
				continue
			}
			for _, p := range sv.Ports {
				want[portKey{p.Host, strings.ToUpper(p.Proto)}] = sv
			}
		}
	}
	st := RouterState{Enabled: enabled, Checked: time.Now(), Ports: []RouterPort{}}
	if len(want) == 0 && len(tracked) == 0 {
		if enabled {
			// still show whether a router is there
			if c, err := m.routerClient(ctx); err != nil {
				st.Error = err.Error()
			} else {
				st.Found, st.Name, st.LocalIP = true, c.Name, c.LocalIP
				st.ExternalIP, _ = c.ExternalIP(ctx)
			}
		}
		m.setRouterState(st)
		return st
	}
	c, err := m.routerClient(ctx)
	if err != nil {
		st.Error = err.Error()
		for k, sv := range want {
			st.Ports = append(st.Ports, RouterPort{Server: sv.ID, Name: sv.Name, Port: k.Port, Proto: k.Proto, Error: "Router nicht erreichbar"})
		}
		sortPorts(st.Ports)
		m.setRouterState(st)
		return st
	}
	st.Found, st.Name, st.LocalIP = true, c.Name, c.LocalIP
	st.ExternalIP, _ = c.ExternalIP(ctx)

	isTracked := func(k portKey) bool {
		return slices.ContainsFunc(tracked, func(r store.RouterMap) bool { return r.Port == k.Port && strings.EqualFold(r.Proto, k.Proto) })
	}
	var newTracked []store.RouterMap
	broken := false
	for k, sv := range want {
		rp := RouterPort{Server: sv.ID, Name: sv.Name, Port: k.Port, Proto: k.Proto}
		cur, gerr := c.Get(ctx, k.Port, k.Proto)
		switch {
		case gerr == nil && cur.Client == c.LocalIP && !isTracked(k) && !strings.HasPrefix(cur.Description, "gamepanel"):
			rp.OK, rp.Manual = true, true // forwarded by hand to us: fine, and never removed by the panel
		case gerr == nil && cur.Client == c.LocalIP && m.rt.lease == 0:
			rp.OK = true
		case gerr == nil && cur.Client != c.LocalIP:
			rp.Error = fmt.Sprintf("Port ist im Router an %s vergeben", cur.Client)
		default:
			if gerr != nil && upnp.Code(gerr) == 0 && !strings.Contains(gerr.Error(), "HTTP") {
				broken = true // network error: find the router again next time
			}
			desc := "gamepanel " + sv.ID + " " + sv.Name
			if len(desc) > 60 {
				desc = desc[:60]
			}
			aerr := c.Add(ctx, k.Port, k.Proto, desc, m.rt.lease)
			if aerr != nil && m.rt.lease == 0 && upnp.Code(aerr) != 606 && upnp.Code(aerr) != 718 {
				// some routers refuse permanent forwardings: use one hour and renew
				if aerr2 := c.Add(ctx, k.Port, k.Proto, desc, 3600); aerr2 == nil {
					m.rt.lease, aerr = 3600, nil
				}
			}
			if upnp.Code(aerr) == 718 {
				rp.Error = "Port ist im Router schon freigegeben, wohl von Hand. Lösch die alte Freigabe in der Fritzbox, dann übernimmt das Panel."
			} else if aerr != nil {
				rp.Error = aerr.Error()
			} else {
				rp.OK = true
			}
		}
		if rp.OK && !rp.Manual {
			newTracked = append(newTracked, store.RouterMap{Server: sv.ID, Port: k.Port, Proto: k.Proto})
		}
		st.Ports = append(st.Ports, rp)
	}
	// close what the panel opened and is no longer wanted
	for _, r := range tracked {
		k := portKey{r.Port, strings.ToUpper(r.Proto)}
		if _, ok := want[k]; ok {
			continue
		}
		if cur, gerr := c.Get(ctx, r.Port, r.Proto); gerr == nil && cur.Client != c.LocalIP {
			continue // meanwhile someone else uses that port: not ours to delete
		}
		if derr := c.Delete(ctx, r.Port, r.Proto); derr != nil && upnp.Code(derr) != 714 {
			log.Printf("[Router] Port %d/%s schließen: %v", r.Port, r.Proto, derr)
			newTracked = append(newTracked, r) // try again next time
		}
	}
	if broken {
		m.rt.client = nil
	}
	_ = m.st.Update(func(d *store.Data) error { d.Router = newTracked; return nil })
	sortPorts(st.Ports)
	m.setRouterState(st)
	return st
}

func sortPorts(p []RouterPort) {
	slices.SortFunc(p, func(a, b RouterPort) int {
		if a.Name != b.Name {
			return strings.Compare(a.Name, b.Name)
		}
		if a.Port != b.Port {
			return a.Port - b.Port
		}
		return strings.Compare(a.Proto, b.Proto)
	})
}

func (m *Manager) setRouterState(st RouterState) {
	m.rt.smu.Lock()
	m.rt.state = st
	m.rt.smu.Unlock()
}

func (m *Manager) routerClient(ctx context.Context) (*upnp.Client, error) {
	if m.rt.client != nil {
		return m.rt.client, nil
	}
	c, err := RouterDiscover(ctx)
	if err != nil {
		return nil, err
	}
	m.rt.client = c
	return c, nil
}

func (m *Manager) containerRunning(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l := m.live[id]
	return l != nil && l.State.Running
}
