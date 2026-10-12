package manager

import (
	"context"
	"errors"
	"fmt"
	"net"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/query"
	"github.com/derface/Gameserver-controlPanel/internal/reach"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/upnp"
)

// "Von außen erreichbar?": checks the way from the internet to a server step
// by step, so the answer says where it breaks (no own IPv4, DNS points
// elsewhere, port not forwarded, server not answering …).

// ReachStep is one general check.
type ReachStep struct {
	Title  string `json:"title"`
	State  string `json:"state"` // ok | warn | fail | skip
	Detail string `json:"detail"`
}

// ReachPort is the result for one port of the server.
type ReachPort struct {
	Port        int    `json:"port"`
	Proto       string `json:"proto"`
	Name        string `json:"name,omitempty"`
	Internal    bool   `json:"internal,omitempty"` // admin access, deliberately not forwarded
	Router      string `json:"router"`             // ok | warn | fail | skip
	RouterText  string `json:"router_text"`
	Outside     string `json:"outside"` // ok | warn | fail | skip
	OutsideText string `json:"outside_text"`
}

// ReachReport is the answer shown to the user.
type ReachReport struct {
	Verdict  string      `json:"verdict"` // ok | likely | fail
	Summary  string      `json:"summary"`
	Address  string      `json:"address,omitempty"`
	PublicIP string      `json:"public_ip,omitempty"`
	Checked  time.Time   `json:"checked"`
	Steps    []ReachStep `json:"steps"`
	Ports    []ReachPort `json:"ports"`
	Services []string    `json:"services"` // outside helpers that were asked
}

// ReachServices are the outside helpers; replaced in tests.
var ReachServices = reach.Default

// reachAsk queries the game through the public address; replaced in tests.
var reachAsk = query.Ask

// privatePort tells admin ports that must not be reachable from the
// internet (RCON, Telnet, web admin …). They are never forwarded.
func privatePort(name string) bool {
	n := strings.ToLower(name)
	// a shared port ("Port für Spiel und RCON") carries game traffic
	if strings.Contains(n, "spiel") || strings.Contains(n, "game") {
		return false
	}
	for _, w := range []string{"rcon", "telnet", "web-admin", "webadmin", "web interface", "serverquery", "shutdown",
		"fernverwaltung", "remote admin", "admin-port", "konsolen-port", "console port"} {
		if strings.Contains(n, w) {
			return true
		}
	}
	return false
}

// CheckReach runs all checks for one server.
func (m *Manager) CheckReach(ctx context.Context, id string) (ReachReport, error) {
	sv, ok := m.st.Server(id)
	if !ok {
		return ReachReport{}, errors.New("Server nicht gefunden")
	}
	tpl, _ := m.tpls.Get(sv.Template)
	svc := ReachServices
	rep := ReachReport{Checked: time.Now(), Steps: []ReachStep{}, Ports: []ReachPort{}}
	step := func(title, state, detail string) { rep.Steps = append(rep.Steps, ReachStep{title, state, detail}) }

	var host string
	var upnpOn bool
	m.st.Read(func(d *store.Data) { host, upnpOn = strings.TrimSpace(d.Settings.PublicHost), d.Settings.UPnP })

	info := m.Info(sv)
	if info.Status != "running" {
		rep.Verdict, rep.Summary = "fail", "Der Server läuft gerade nicht ("+statusWord(info.Status)+"). Starte ihn und prüf dann noch einmal."
		return rep, nil
	}
	step("Server läuft", "ok", "Der Server ist gestartet.")

	qport, qtype := -1, ""
	if tpl.Query != nil && tpl.Query.Port >= 0 && tpl.Query.Port < len(sv.Ports) {
		qport, qtype = sv.Ports[tpl.Query.Port].Host, tpl.Query.Type
	}

	// ask the internet, the router and the DNS at the same time
	var pubIP, pubErr string
	var wanIP, rName, localIP string
	var rErr error
	var rc *upnp.Client
	maps := map[portKey]struct {
		m   upnp.Mapping
		err error
	}{}
	var tracked []store.RouterMap
	m.st.Read(func(d *store.Data) { tracked = slices.Clone(d.Router) })
	var v4, v6 []string
	var dnsErr error
	isIP := net.ParseIP(host) != nil
	reach.Parallel(
		func() {
			ip, err := svc.PublicIP(ctx)
			if err != nil {
				pubErr = err.Error()
			}
			pubIP = ip
		},
		func() {
			m.rt.mu.Lock()
			defer m.rt.mu.Unlock()
			rc, rErr = m.routerClient(ctx)
			if rErr != nil {
				return
			}
			rName, localIP = rc.Name, rc.LocalIP
			wanIP, _ = rc.ExternalIP(ctx)
			for _, p := range sv.Ports {
				k := portKey{p.Host, strings.ToUpper(p.Proto)}
				if _, done := maps[k]; done || privatePort(p.Name) {
					continue
				}
				mp, err := rc.Get(ctx, p.Host, k.Proto)
				if err != nil && upnp.Code(err) == 0 {
					m.rt.client = nil // network trouble: find the router again next time
				}
				maps[k] = struct {
					m   upnp.Mapping
					err error
				}{mp, err}
			}
		},
		func() {
			if host != "" && !isIP {
				v4, v6, dnsErr = svc.Lookup(ctx, host)
			}
		},
	)
	rep.PublicIP = pubIP

	// public IPv4
	routerTxt := ""
	if rName != "" {
		routerTxt = " (Router: " + rName + ")"
	}
	switch {
	case wanIP != "" && reach.Private(wanIP):
		step("Eigene öffentliche IPv4", "fail", fmt.Sprintf("Der Router hat nach außen nur die Adresse %s. Das heißt: Der Anschluss hat keine eigene öffentliche IPv4 (DS-Lite/CGNAT), oder vor dem Router hängt noch ein zweiter. Über IPv4 kommt dann niemand von außen rein. Abhilfe: beim Anbieter eine öffentliche IPv4 anfragen oder den vorderen Router als Modem betreiben.", wanIP))
	case pubErr != "":
		step("Eigene öffentliche IPv4", "warn", pubErr+". Die Prüfung von außen wird übersprungen.")
	case wanIP != "" && wanIP != pubIP:
		step("Eigene öffentliche IPv4", "warn", fmt.Sprintf("Der Router meldet %s, das Internet sieht aber %s. Läuft der Rechner über ein VPN, oder hängt noch ein Router davor?", wanIP, pubIP))
	default:
		step("Eigene öffentliche IPv4", "ok", pubIP+routerTxt)
	}

	// address for players
	firstPort := 0
	for _, p := range sv.Ports {
		if !privatePort(p.Name) {
			firstPort = p.Host
			break
		}
	}
	switch {
	case host == "":
		d := "In den Einstellungen ist keine Adresse für Spieler eingetragen."
		if pubIP != "" {
			d += " Spieler bräuchten deine IP " + pubIP + ", die sich bei einer Neueinwahl ändern kann. Besser einen DynDNS-Namen eintragen."
		}
		step("Adresse für Spieler", "warn", d)
		if pubIP != "" {
			rep.Address = net.JoinHostPort(pubIP, strconv.Itoa(firstPort))
		}
	case isIP:
		rep.Address = net.JoinHostPort(host, strconv.Itoa(firstPort))
		if pubIP != "" && host != pubIP {
			step("Adresse für Spieler", "fail", fmt.Sprintf("Eingetragen ist %s, deine öffentliche IP ist aber %s.", host, pubIP))
		} else {
			step("Adresse für Spieler", "ok", host)
		}
	default:
		rep.Address = net.JoinHostPort(host, strconv.Itoa(firstPort))
		six := ""
		if len(v6) > 0 {
			six = " Es gibt auch einen IPv6-Eintrag; die meisten Spiele verbinden aber per IPv4."
		}
		switch {
		case dnsErr != nil:
			step("Adresse für Spieler", "fail", dnsErr.Error()+".")
		case len(v4) == 0:
			step("Adresse für Spieler", "fail", host+" hat keinen IPv4-Eintrag (A-Record)."+six)
		case pubIP == "":
			step("Adresse für Spieler", "warn", fmt.Sprintf("%s zeigt auf %s, deine öffentliche IP ist aber unbekannt.", host, strings.Join(v4, ", ")))
		case slices.Contains(v4, pubIP):
			step("Adresse für Spieler", "ok", fmt.Sprintf("%s zeigt im Internet auf %s, deine öffentliche IP.%s", host, pubIP, six))
		default:
			step("Adresse für Spieler", "fail", fmt.Sprintf("%s zeigt im Internet auf %s, deine öffentliche IP ist aber %s. DynDNS prüfen; nach einem IP-Wechsel dauert es ein paar Minuten.", host, strings.Join(v4, ", "), pubIP))
		}
	}

	// ports: router forwarding and the view from outside
	canOutside := pubIP != "" && !(wanIP != "" && reach.Private(wanIP))
	seen := map[portKey]bool{}
	var mu sync.Mutex
	var jobs []func()
	usedCheckHost, usedMcSrv := false, false
	for _, p := range sv.Ports {
		k := portKey{p.Host, strings.ToUpper(p.Proto)}
		if seen[k] {
			continue
		}
		seen[k] = true
		rp := ReachPort{Port: p.Host, Proto: k.Proto, Name: p.Name, Router: "skip", Outside: "skip"}
		if privatePort(p.Name) {
			rp.Internal = true
			rp.RouterText = "Bleibt absichtlich zu: Verwaltungszugang"
			rp.OutsideText = "Nicht freigeben, sonst kann jeder versuchen, sich einzuloggen."
			rep.Ports = append(rep.Ports, rp)
			continue
		}
		// router
		if rErr != nil {
			rp.RouterText = "Router per UPnP nicht lesbar, bitte selbst in der Fritzbox nachsehen"
		} else {
			got := maps[k]
			manual := !slices.ContainsFunc(tracked, func(r store.RouterMap) bool { return r.Port == k.Port && strings.EqualFold(r.Proto, k.Proto) }) &&
				!strings.HasPrefix(got.m.Description, "gamepanel")
			switch {
			case upnp.Code(got.err) == 714:
				rp.Router, rp.RouterText = "fail", "Keine Freigabe im Router"
				if sv.NoRouter {
					rp.RouterText += ". Für diesen Server ist die automatische Freigabe aus."
				} else if !upnpOn {
					rp.RouterText += ". Automatische Portfreigabe einschalten (Einstellungen → Router & Ports) oder in der Fritzbox freigeben."
				}
			case got.err != nil:
				rp.Router, rp.RouterText = "warn", "Router-Antwort: "+got.err.Error()
			case got.m.Client != localIP:
				rp.Router, rp.RouterText = "fail", fmt.Sprintf("Freigabe zeigt auf %s statt auf diesen Rechner (%s)", got.m.Client, localIP)
			case !got.m.Enabled:
				rp.Router, rp.RouterText = "fail", "Freigabe ist im Router ausgeschaltet"
			case got.m.InternalPort != 0 && got.m.InternalPort != p.Host:
				rp.Router, rp.RouterText = "warn", fmt.Sprintf("Freigabe leitet auf Port %d weiter, der Server lauscht auf %d", got.m.InternalPort, p.Host)
			case manual:
				rp.Router, rp.RouterText = "ok", "Freigegeben (von Hand)"
			default:
				rp.Router, rp.RouterText = "ok", "Freigegeben (automatisch)"
			}
		}
		// outside
		idx := len(rep.Ports)
		rep.Ports = append(rep.Ports, rp)
		switch {
		case !canOutside:
			rep.Ports[idx].OutsideText = "Nicht geprüft"
		case k.Proto == "TCP":
			usedCheckHost = true
			port := p.Host
			jobs = append(jobs, func() {
				r := svc.CheckTCP(ctx, pubIP, port)
				st, txt := "fail", ""
				switch {
				case r.Open:
					st, txt = "ok", "Verbindung klappt aus "+strings.Join(r.From, ", ")
				case r.Refused:
					st, txt = "warn", "Kommt bis zum Rechner, aber dort nimmt niemand an. Manche Spiele nutzen diesen TCP-Port gar nicht."
				case strings.Contains(r.Error, "check-host.net"):
					st, txt = "skip", r.Error
				default:
					txt = "Keine Verbindung von außen"
					if r.Error != "" {
						txt += " (" + r.Error + ")"
					}
				}
				mu.Lock()
				rep.Ports[idx].Outside, rep.Ports[idx].OutsideText = st, txt
				mu.Unlock()
			})
		case qtype == "bedrock" && p.Host == qport:
			usedMcSrv = true
			port := p.Host
			jobs = append(jobs, func() {
				on, err := svc.BedrockOnline(ctx, pubIP, port)
				st, txt := "ok", "Minecraft antwortet von außen (mcsrvstat.us)"
				if err != nil {
					st, txt = "skip", err.Error()
				} else if !on {
					st, txt = "fail", "Von außen keine Antwort (mcsrvstat.us)"
				}
				mu.Lock()
				rep.Ports[idx].Outside, rep.Ports[idx].OutsideText = st, txt
				mu.Unlock()
			})
		default:
			rep.Ports[idx].OutsideText = "UDP lässt sich ohne das Spiel nicht von außen testen"
		}
	}
	// the game's own query through the public address (router loopback)
	var loopOK bool
	var loopErr error
	if qport > 0 && canOutside {
		jobs = append(jobs, func() {
			_, loopErr = reachAsk(qtype, pubIP, qport)
			loopOK = loopErr == nil
		})
	}
	reach.Parallel(jobs...)
	if usedCheckHost {
		rep.Services = append(rep.Services, "check-host.net")
	}
	if usedMcSrv {
		rep.Services = append(rep.Services, "mcsrvstat.us")
	}
	if qport > 0 && canOutside {
		if loopOK {
			step("Antwort über die öffentliche IP", "ok", fmt.Sprintf("Das Spiel antwortet über %s:%d. Der Weg durch den Router stimmt.", pubIP, qport))
		} else {
			step("Antwort über die öffentliche IP", "warn", "Keine Antwort über die öffentliche IP. Nicht jeder Router leitet Anfragen aus dem Heimnetz zurück; das allein heißt noch nichts.")
		}
	}
	rep = finishReach(rep)
	if rep.Verdict != "fail" && loopOK {
		rep.Verdict = "ok"
		rep.Summary = "Spieler kommen von außen über diese Adresse rein:"
	}
	return rep, nil
}

// finishReach sets the verdict from the steps and ports.
func finishReach(rep ReachReport) ReachReport {
	for _, s := range rep.Steps {
		if s.State == "fail" {
			rep.Verdict, rep.Summary = "fail", s.Detail
			return rep
		}
	}
	outsideOK := false
	for _, p := range rep.Ports {
		if p.Internal {
			continue
		}
		if p.Router == "fail" {
			rep.Verdict, rep.Summary = "fail", fmt.Sprintf("Port %d %s: %s", p.Port, p.Proto, p.RouterText)
			return rep
		}
		if p.Outside == "fail" {
			rep.Verdict, rep.Summary = "fail", fmt.Sprintf("Port %d %s: %s", p.Port, p.Proto, p.OutsideText)
			return rep
		}
		outsideOK = outsideOK || p.Outside == "ok"
	}
	if outsideOK {
		rep.Verdict, rep.Summary = "ok", "Spieler kommen von außen über diese Adresse rein:"
		return rep
	}
	rep.Verdict = "likely"
	rep.Summary = "Sieht gut aus: Die Freigaben stimmen. Für die Ports dieses Spiels gibt es keinen neutralen Test von außen, ganz sicher ist erst ein Test mit einem Freund."
	return rep
}

func statusWord(s string) string {
	switch s {
	case "stopped":
		return "gestoppt"
	case "installing":
		return "wird installiert"
	case "starting":
		return "startet noch"
	case "preparing":
		return "wird vorbereitet"
	case "error":
		return "Fehler"
	}
	return s
}
