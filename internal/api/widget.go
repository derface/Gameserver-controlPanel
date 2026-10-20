package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Phone widgets (KWGT on Android, or anything that can read JSON): one
// small answer with every server's state, players, CPU and RAM, plus ready
// lines of text so a widget needs only a formula per line.
//
// Widget apps cannot send headers, so the key may come as ?key=gpk_…; that
// works only here and only for keys with read-only rights.

func (a *API) routesWidget(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/widget", a.widget)
}

type widgetServer struct {
	ID      string `json:"id"`
	Name    string `json:"name"`
	Game    string `json:"game"`
	Machine string `json:"machine,omitempty"`
	State   string `json:"state"` // online | startet | aus | fehler | wartung
	Icon    string `json:"icon"`
	Players int    `json:"players"` // -1 = unknown
	Max     int    `json:"max"`
	CPU     int    `json:"cpu"` // % of what the server may use
	RAM     int    `json:"ram"` // % of its RAM limit
	RAMMB   int    `json:"ram_mb"`
	Line    string `json:"line"`
}

type widgetAnswer struct {
	Updated  string         `json:"updated"`
	Title    string         `json:"title"`
	Running  int            `json:"running"`
	Total    int            `json:"total"`
	Players  int            `json:"players"`
	Problem  int            `json:"problems"` // servers with an error
	HostCPU  int            `json:"host_cpu"`
	HostRAM  int            `json:"host_ram"`
	HostDisk int            `json:"host_disk"`
	Text     string         `json:"text"`
	Servers  []widgetServer `json:"servers"`
}

var widgetIcon = map[string]string{"online": "🟢", "startet": "🟡", "aus": "⚫", "fehler": "🔴", "wartung": "🛠"}

func (a *API) widget(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	w.Header().Set("Cache-Control", "no-store")
	viaQuery := false
	if k := r.URL.Query().Get("key"); k != "" {
		if !strings.HasPrefix(k, auth.APIKeyPrefix) {
			fail(w, 401, errors.New("Ungültiger Schlüssel"))
			return
		}
		r.Header.Set("Authorization", "Bearer "+k)
		viaQuery = true
	}
	u, ok := a.Auth.Current(r)
	if !ok {
		fail(w, 401, errors.New("Bitte anmelden oder einen API-Schlüssel angeben"))
		return
	}
	if viaQuery && u.Role != auth.Viewer {
		// a key in a URL can end up in logs: only read-only keys may do that
		fail(w, 403, errors.New("Im Link nur Schlüssel mit Leserechten (Zuschauer) verwenden"))
		return
	}
	en := r.URL.Query().Get("lang") == "en"
	writeJSON(w, 200, a.widgetData(u, en))
}

type widgetInfo struct {
	manager.Info
	Machine string
}

func (a *API) widgetInfos(u store.User) []widgetInfo {
	var out []widgetInfo
	if a.Mgr != nil {
		for _, s := range a.Mgr.List() {
			if auth.CanSee(u, s.ID) && s.CreatedBy != "selbsttest" {
				out = append(out, widgetInfo{Info: s})
			}
		}
	}
	if a.Nodes != nil && !u.ViaNode {
		for _, m := range a.Nodes.Servers() {
			id, _ := m["id"].(string)
			if !auth.CanSee(u, id) {
				continue
			}
			b, _ := json.Marshal(m)
			var s manager.Info
			if json.Unmarshal(b, &s) == nil {
				machine, _ := m["node_name"].(string)
				out = append(out, widgetInfo{Info: s, Machine: machine})
			}
		}
	}
	return out
}

func pct(a, b float64) int {
	if b <= 0 {
		return 0
	}
	return int(math.Round(a / b * 100))
}

func (a *API) widgetData(u store.User, en bool) widgetAnswer {
	ans := widgetAnswer{Updated: time.Now().Format("15:04"), Servers: []widgetServer{}}
	order := map[string]int{"fehler": 0, "online": 1, "startet": 2, "wartung": 3, "aus": 4}
	for _, s := range a.widgetInfos(u) {
		st := publicState(s.Info)
		ws := widgetServer{ID: s.ID, Name: s.Name, Game: s.TemplateName, Machine: s.Machine, State: st, Icon: widgetIcon[st], Players: -1}
		if st == "online" {
			ans.Running++
			ws.Players, ws.Max = s.Players, s.MaxPlayers
			if s.Players > 0 {
				ans.Players += s.Players
			}
			ws.CPU = int(math.Round(s.CPU))
			ws.RAMMB = int(s.MemMB)
			ws.RAM = pct(s.MemMB, float64(s.MemoryMB))
		}
		if st == "fehler" {
			ans.Problem++
		}
		ws.Line = widgetLine(ws, en)
		ans.Servers = append(ans.Servers, ws)
	}
	sort.SliceStable(ans.Servers, func(i, j int) bool {
		x, y := ans.Servers[i], ans.Servers[j]
		if order[x.State] != order[y.State] {
			return order[x.State] < order[y.State]
		}
		if x.Players != y.Players {
			return x.Players > y.Players
		}
		return strings.ToLower(x.Name) < strings.ToLower(y.Name)
	})
	ans.Total = len(ans.Servers)
	if a.Host != nil {
		h := a.Host.Get()
		ans.HostCPU = int(math.Round(h.CPUPercent))
		ans.HostRAM = pct(h.MemUsedMB, h.MemTotalMB)
		ans.HostDisk = pct(h.DiskUsedG, h.DiskTotalG)
	}
	if en {
		ans.Title = fmt.Sprintf("%d/%d running · %d players", ans.Running, ans.Total, ans.Players)
	} else {
		ans.Title = fmt.Sprintf("%d/%d laufen · %d Spieler", ans.Running, ans.Total, ans.Players)
	}
	if ans.Problem > 0 {
		if en {
			ans.Title += fmt.Sprintf(" · %d with errors", ans.Problem)
		} else {
			ans.Title += fmt.Sprintf(" · %d mit Fehler", ans.Problem)
		}
	}
	lines := make([]string, 0, len(ans.Servers))
	for _, s := range ans.Servers {
		lines = append(lines, s.Line)
	}
	ans.Text = strings.Join(lines, "\n")
	return ans
}

func widgetLine(s widgetServer, en bool) string {
	switch s.State {
	case "online":
		pl := ""
		if s.Players >= 0 {
			pl = fmt.Sprintf(" %d", s.Players)
			if s.Max > 0 {
				pl += fmt.Sprintf("/%d", s.Max)
			}
			pl += " 👤"
		}
		return fmt.Sprintf("%s %s%s · CPU %d%% · RAM %d%%", s.Icon, s.Name, pl, s.CPU, s.RAM)
	case "startet":
		if en {
			return s.Icon + " " + s.Name + " · starting"
		}
		return s.Icon + " " + s.Name + " · startet"
	case "fehler":
		if en {
			return s.Icon + " " + s.Name + " · error"
		}
		return s.Icon + " " + s.Name + " · Fehler"
	case "wartung":
		if en {
			return s.Icon + " " + s.Name + " · maintenance"
		}
		return s.Icon + " " + s.Name + " · Wartung"
	}
	if en {
		return s.Icon + " " + s.Name + " · off"
	}
	return s.Icon + " " + s.Name + " · aus"
}
