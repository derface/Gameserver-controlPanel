package manager

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/geoip"
)

const ctProc = `ipv4     2 udp      17 118 src=81.0.4.5 dst=192.168.178.54 sport=50123 dport=15636 src=172.17.0.2 dst=81.0.4.5 sport=15636 dport=50123 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 25 src=84.0.1.1 dst=192.168.178.54 sport=40000 dport=15637 src=172.17.0.2 dst=84.0.1.1 sport=15637 dport=40000 mark=0 zone=0 use=2
ipv4     2 tcp      6 431999 ESTABLISHED src=83.0.9.9 dst=192.168.178.54 sport=51000 dport=25565 src=172.17.0.3 dst=83.0.9.9 sport=25565 dport=51000 [ASSURED] mark=0 use=1
ipv4     2 udp      17 119 src=155.133.230.10 dst=192.168.178.54 sport=27015 dport=15636 src=172.17.0.2 dst=155.133.230.10 sport=15636 dport=27015 [ASSURED] mark=0 use=1
ipv4     2 udp      17 117 src=192.168.178.20 dst=192.168.178.54 sport=6000 dport=15636 src=172.17.0.2 dst=192.168.178.20 sport=15636 dport=6000 [ASSURED] mark=0 use=1
ipv4     2 tcp      6 100 TIME_WAIT src=86.0.0.1 dst=192.168.178.54 sport=1 dport=25565 src=172.17.0.3 dst=86.0.0.1 sport=25565 dport=1 [ASSURED] mark=0 use=1
`

// conntrack -L prints the same without the family columns
const ctTool = `udp      17 119 src=82.0.1.2 dst=192.168.178.54 sport=1 dport=15636 src=172.17.0.2 dst=82.0.1.2 sport=15636 dport=1 [ASSURED] mark=0 use=1
`

func TestParseConntrack(t *testing.T) {
	es := parseConntrack([]byte(ctProc + ctTool))
	if len(es) != 7 {
		t.Fatalf("%d entries", len(es))
	}
	if es[0].proto != "udp" || es[0].dport != 15636 || es[0].src.String() != "81.0.4.5" || !es[0].assured || es[0].timeout != 118 {
		t.Fatalf("%+v", es[0])
	}
	if es[2].state != "ESTABLISHED" || es[6].src.String() != "82.0.1.2" {
		t.Fatalf("%+v %+v", es[2], es[6])
	}
	busy := 0
	for _, e := range es {
		if e.busy(120) {
			busy++
		}
	}
	if busy != 5 { // not the unassured ping, not TIME_WAIT
		t.Fatalf("busy %d", busy)
	}
}

func TestGeoSample(t *testing.T) {
	m, _ := settingsMgr(t)
	b, _ := os.ReadFile("../geoip/testdata/test.mmdb")
	dir := filepath.Join(m.dataDir, "geo")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "custom.mmdb"), b, 0o644)
	m.geo = geoip.New(dir)
	ports := map[string][2]any{"udp/15636": {"ens", 2}, "udp/15637": {"ens", 2}, "tcp/25565": {"mc", -1}}
	es := parseConntrack([]byte(ctProc))
	now := time.Now()
	for i := 0; i < 6; i++ {
		m.recordGeoSample(es, ports, now.Add(time.Duration(i)*geoEvery), 120)
	}
	v := m.Geo([]string{"ens", "mc"})
	if len(v.Points) != 2 || v.Relay.Sessions != 1 || v.LAN.Sessions != 1 {
		t.Fatalf("%+v", v)
	}
	if len(v.Live) != 4 {
		t.Fatalf("live %d", len(v.Live))
	}
	var berlin GeoPoint
	for _, p := range v.Points {
		if p.City == "Berlin" {
			berlin = p
		}
	}
	if berlin.Sessions != 1 || berlin.Seconds != 100 || berlin.CountryName != "Deutschland" {
		t.Fatalf("%+v", berlin)
	}
	// reconnect within 30 minutes is the same visit
	m.gt.conns = map[string]*geoConn{}
	for i := 0; i < 4; i++ {
		m.recordGeoSample(es, ports, now.Add(10*time.Minute+time.Duration(i)*geoEvery), 120)
	}
	if v := m.Geo([]string{"ens"}); v.Points[0].Sessions != 1 {
		t.Fatalf("reconnect counted again: %+v", v.Points)
	}
	// nobody plays: busy connections are pings
	m.gt.conns, m.gt.recent = map[string]*geoConn{}, map[string]time.Time{}
	ports["udp/15636"] = [2]any{"ens", 0}
	for i := 0; i < 4; i++ {
		m.recordGeoSample(es, ports, now.Add(2*time.Hour+time.Duration(i)*geoEvery), 120)
	}
	if v := m.Geo([]string{"ens"}); v.Points[0].Sessions != 1 {
		t.Fatalf("counted while empty: %+v", v.Points)
	}
}

// Enshrouded seen on the real VM: only queries arrive on the game ports,
// the play traffic leaves the container towards the player or a relay.
const ctSteamNet = `ipv4     2 udp      17 18 src=127.0.0.1 dst=127.0.0.1 sport=59465 dport=15637 src=127.0.0.1 dst=127.0.0.1 sport=15637 dport=59465 mark=0 zone=0 use=2
ipv4     2 udp      17 12 src=58.11.25.195 dst=192.168.178.54 sport=5680 dport=15637 src=172.17.0.2 dst=58.11.25.195 sport=15637 dport=5680 mark=0 zone=0 use=2
ipv4     2 udp      17 18 src=172.17.0.1 dst=172.17.0.2 sport=53517 dport=15637 src=172.17.0.2 dst=172.17.0.1 sport=15637 dport=53517 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 119 src=172.17.0.2 dst=81.0.7.7 sport=40001 dport=27036 src=81.0.7.7 dst=192.168.178.54 sport=27036 dport=40001 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 118 src=172.17.0.2 dst=155.133.226.71 sport=40002 dport=27028 src=155.133.226.71 dst=192.168.178.54 sport=27028 dport=40002 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 117 src=172.17.0.2 dst=192.168.178.30 sport=40003 dport=50000 src=192.168.178.30 dst=172.17.0.2 sport=50000 dport=40003 [ASSURED] mark=0 zone=0 use=2
ipv4     2 udp      17 119 src=172.17.0.2 dst=1.1.1.1 sport=40004 dport=53 src=1.1.1.1 dst=192.168.178.54 sport=53 dport=40004 [ASSURED] mark=0 zone=0 use=2
`

func TestGeoOutbound(t *testing.T) {
	m, _ := settingsMgr(t)
	b, _ := os.ReadFile("../geoip/testdata/test.mmdb")
	dir := filepath.Join(m.dataDir, "geo")
	_ = os.MkdirAll(dir, 0o755)
	_ = os.WriteFile(filepath.Join(dir, "custom.mmdb"), b, 0o644)
	m.geo = geoip.New(dir)
	ports := map[string][2]any{"udp/15636": {"ens", 1}, "udp/15637": {"ens", 1}, "ip/172.17.0.2": {"ens", 1}}
	es := parseConntrack([]byte(ctSteamNet))
	now := time.Now()
	for i := 0; i < 5; i++ {
		m.recordGeoSample(es, ports, now.Add(time.Duration(i)*geoEvery), 120)
	}
	v := m.Geo([]string{"ens"})
	if len(v.Points) != 1 || v.Points[0].City != "Berlin" || v.Relay.Sessions != 1 || v.LAN.Sessions != 1 {
		t.Fatalf("%+v", v)
	}
	if len(v.Debug) != 6 { // DNS is left out
		t.Fatalf("debug %d: %v", len(v.Debug), v.Debug)
	}
}
