package manager

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"slices"
	"sort"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Smart home: Philips Hue lights (local bridge API) and Home Assistant
// (lights, Alexa or speaker announcements, any service) react to panel
// events, e.g. a lamp flashes green when a friend joins, red on a crash,
// and Alexa says "Bjorn ist jetzt auf Valheim".

// SmartTriggers are the events a rule can react to.
func SmartTriggers() []struct{ Key, Label string } {
	return []struct{ Key, Label string }{
		{"join", "Spieler betritt einen Server"},
		{"leave", "Spieler verlässt einen Server"},
		{"online", "Server ist online"},
		{"offline", "Server wurde gestoppt"},
		{"crash", "Absturz oder Hänger"},
		{"error", "Fehler (Aufgabe, Backup, Installation)"},
		{"update", "Spiel-Update eingespielt"},
		{"backup", "Backup erstellt"},
		{"event", "Event beginnt oder endet"},
		{"wish", "Neuer Wunsch auf der Statusseite"},
	}
}

func validTrigger(t string) bool {
	for _, x := range SmartTriggers() {
		if x.Key == t {
			return true
		}
	}
	return false
}

// trigger maps a panel event to a smart home trigger ("" = none).
func smartTrigger(kind, event string, data map[string]any) string {
	switch event {
	case "player.join":
		return "join"
	case "player.leave":
		return "leave"
	case "server.status":
		to, _ := data["to"].(string)
		from, _ := data["from"].(string)
		if to == "running" {
			return "online"
		}
		if to == "stopped" && (from == "running" || from == "starting") {
			return "offline"
		}
		return ""
	}
	switch kind {
	case "crash", "error", "update", "backup", "event", "wish":
		return kind
	}
	return ""
}

// hue xy colours (gamut C) and Home Assistant rgb for the same names
var smartColors = map[string]struct {
	XY  [2]float64
	RGB [3]int
}{
	"red":    {[2]float64{0.675, 0.322}, [3]int{255, 0, 0}},
	"green":  {[2]float64{0.17, 0.7}, [3]int{0, 255, 0}},
	"blue":   {[2]float64{0.167, 0.04}, [3]int{0, 0, 255}},
	"yellow": {[2]float64{0.4317, 0.4996}, [3]int{255, 220, 0}},
	"orange": {[2]float64{0.5614, 0.4156}, [3]int{255, 120, 0}},
	"purple": {[2]float64{0.2725, 0.1096}, [3]int{160, 0, 255}},
	"white":  {[2]float64{0.3227, 0.329}, [3]int{255, 255, 255}},
}

var smartClient = &http.Client{Timeout: 8 * time.Second}

// ValidateSmartRule checks and cleans a rule.
func ValidateSmartRule(r *store.SmartRule) error {
	r.Name = strings.TrimSpace(r.Name)
	if len([]rune(r.Name)) > 50 {
		return errors.New("Der Name ist zu lang")
	}
	if !validTrigger(r.Trigger) {
		return errors.New("Bitte ein Ereignis wählen")
	}
	switch r.Action {
	case "hue_light", "ha_light":
		if len(r.Targets) == 0 {
			return errors.New("Bitte mindestens eine Lampe wählen")
		}
		if _, ok := smartColors[r.Color]; !ok {
			r.Color = "green"
		}
		if r.Effect != "pulse" && r.Effect != "stay" {
			r.Effect = "blink"
		}
	case "ha_announce":
		if !strings.Contains(r.Service, ".") {
			return errors.New("Bitte den Dienst für die Ansage wählen, z. B. notify.alexa_media_kueche oder tts.speak")
		}
		if strings.TrimSpace(r.Message) == "" {
			r.Message = "{{text}}"
		}
	case "ha_service":
		if !strings.Contains(r.Service, ".") {
			return errors.New("Dienst bitte als domain.dienst angeben, z. B. script.party")
		}
		if strings.TrimSpace(r.Data) != "" && !json.Valid([]byte(renderSmart(r.Data, smartVars{}, true))) {
			return errors.New("Die Zusatzdaten sind kein gültiges JSON")
		}
	default:
		return errors.New("Unbekannte Aktion")
	}
	if len(r.Message) > 300 || len(r.Data) > 2000 || len(r.Targets) > 20 {
		return errors.New("Zu viele oder zu lange Angaben")
	}
	return nil
}

type smartVars struct{ Text, Server, Player, Game string }

// renderSmart fills {{text}} {{server}} {{player}} {{game}}; inJSON escapes
// the values for use inside a JSON string.
func renderSmart(tpl string, v smartVars, inJSON bool) string {
	esc := func(s string) string {
		if !inJSON {
			return s
		}
		b, _ := json.Marshal(s)
		return string(b[1 : len(b)-1])
	}
	return strings.NewReplacer("{{text}}", esc(v.Text), "{{server}}", esc(v.Server), "{{player}}", esc(v.Player), "{{game}}", esc(v.Game)).Replace(tpl)
}

// smartEvent runs every rule that matches the event.
func (m *Manager) smartEvent(kind, event, serverID, text string, data map[string]any) {
	trig := smartTrigger(kind, event, data)
	if trig == "" {
		return
	}
	var cfg store.SmartHome
	m.st.Read(func(d *store.Data) {
		cfg = d.Smart
		cfg.Rules = append([]store.SmartRule(nil), d.Smart.Rules...)
	})
	if len(cfg.Rules) == 0 {
		return
	}
	v := smartVars{Text: text}
	if p, ok := data["player"].(string); ok {
		v.Player = p
	}
	if serverID != "" {
		if sv, tpl, err := m.get(serverID); err == nil {
			v.Server, v.Game = sv.Name, tpl.Name
		}
	}
	for _, r := range cfg.Rules {
		if !r.Enabled || r.Trigger != trig || (len(r.Servers) > 0 && !slices.Contains(r.Servers, serverID)) {
			continue
		}
		if !notifyLimit.Allow("smart|"+r.ID+"|"+serverID+"|"+v.Player, 5*time.Second) {
			continue
		}
		go m.runSmart(cfg, r, v)
	}
}

// RunSmartRule runs a rule now (test button) and reports the result.
func (m *Manager) RunSmartRule(r store.SmartRule) error {
	var cfg store.SmartHome
	m.st.Read(func(d *store.Data) { cfg = d.Smart })
	return m.runSmart(cfg, r, smartVars{Text: "Test vom Bytewise Panel", Server: "Valheim", Player: "Bjorn", Game: "Valheim"})
}

func (m *Manager) runSmart(cfg store.SmartHome, r store.SmartRule, v smartVars) error {
	var err error
	switch r.Action {
	case "hue_light":
		err = hueFlash(cfg, r)
	case "ha_light":
		err = haFlash(cfg, r)
	case "ha_announce":
		err = haAnnounce(cfg, r, v)
	case "ha_service":
		err = haService(cfg, r, v)
	}
	_ = m.st.Update(func(d *store.Data) error {
		for i := range d.Smart.Rules {
			if d.Smart.Rules[i].ID == r.ID {
				d.Smart.Rules[i].LastAt = time.Now()
				d.Smart.Rules[i].LastErr = ""
				if err != nil {
					d.Smart.Rules[i].LastErr = err.Error()
				}
			}
		}
		return nil
	})
	return err
}

func effectTime(effect string) time.Duration {
	switch effect {
	case "pulse":
		return 8 * time.Second
	case "stay":
		return 0
	}
	return 3 * time.Second
}

// ---------- Philips Hue (local API v1) ----------

func hueURL(cfg store.SmartHome, path string) string {
	host := strings.TrimSuffix(strings.TrimPrefix(strings.TrimPrefix(cfg.HueBridge, "http://"), "https://"), "/")
	return "http://" + host + "/api/" + cfg.HueUser + path
}

func hueCall(method, u string, body any, out any) error {
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequest(method, u, rd)
	if err != nil {
		return err
	}
	resp, err := smartClient.Do(req)
	if err != nil {
		return fmt.Errorf("Hue-Bridge nicht erreichbar (%v)", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	var errs []struct {
		Error *struct {
			Type        int    `json:"type"`
			Description string `json:"description"`
		} `json:"error"`
	}
	if json.Unmarshal(b, &errs) == nil {
		for _, e := range errs {
			if e.Error != nil {
				if e.Error.Type == 1 {
					return errors.New("Hue: nicht gekoppelt (bitte neu koppeln)")
				}
				if e.Error.Type == 101 {
					return errors.New("Hue: bitte zuerst den runden Knopf auf der Bridge drücken, dann innerhalb von 30 Sekunden „Koppeln“")
				}
				return errors.New("Hue: " + e.Error.Description)
			}
		}
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

type hueState struct {
	On   bool      `json:"on"`
	Bri  int       `json:"bri"`
	XY   []float64 `json:"xy,omitempty"`
	CT   int       `json:"ct,omitempty"`
	Mode string    `json:"colormode,omitempty"`
}

func hueFlash(cfg store.SmartHome, r store.SmartRule) error {
	if cfg.HueBridge == "" || cfg.HueUser == "" {
		return errors.New("Hue ist nicht eingerichtet")
	}
	c := smartColors[r.Color]
	alert := map[string]string{"blink": "select", "pulse": "lselect", "stay": "none"}[r.Effect]
	type saved struct {
		id string
		st hueState
	}
	var before []saved
	var firstErr error
	for _, id := range r.Targets {
		var l struct {
			State hueState `json:"state"`
		}
		if err := hueCall("GET", hueURL(cfg, "/lights/"+url.PathEscape(id)), nil, &l); err != nil {
			firstErr = err
			continue
		}
		before = append(before, saved{id, l.State})
		if err := hueCall("PUT", hueURL(cfg, "/lights/"+url.PathEscape(id)+"/state"), map[string]any{"on": true, "bri": 254, "xy": c.XY, "alert": alert, "transitiontime": 2}, nil); err != nil {
			firstErr = err
		}
	}
	if d := effectTime(r.Effect); d > 0 && len(before) > 0 {
		go func() {
			time.Sleep(d)
			for _, b := range before {
				st := map[string]any{"on": b.st.On, "alert": "none", "transitiontime": 4}
				if b.st.On {
					st["bri"] = b.st.Bri
					if b.st.Mode == "ct" && b.st.CT > 0 {
						st["ct"] = b.st.CT
					} else if len(b.st.XY) == 2 {
						st["xy"] = b.st.XY
					}
				}
				_ = hueCall("PUT", hueURL(cfg, "/lights/"+url.PathEscape(b.id)+"/state"), st, nil)
			}
		}()
	}
	return firstErr
}

// HueDiscover finds bridges in the home network via the Philips service.
func HueDiscover() ([]string, error) {
	var out []struct {
		IP string `json:"internalipaddress"`
	}
	req, _ := http.NewRequest("GET", "https://discovery.meethue.com/", nil)
	resp, err := smartClient.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	if err := json.NewDecoder(io.LimitReader(resp.Body, 1<<16)).Decode(&out); err != nil {
		return nil, err
	}
	var ips []string
	for _, b := range out {
		if net.ParseIP(b.IP) != nil {
			ips = append(ips, b.IP)
		}
	}
	return ips, nil
}

// HuePair asks the bridge for an app key (after the link button was pressed).
func HuePair(bridge string) (string, error) {
	var out []struct {
		Success *struct {
			Username string `json:"username"`
		} `json:"success"`
	}
	err := hueCall("POST", hueURL(store.SmartHome{HueBridge: bridge}, ""), map[string]string{"devicetype": "bytewise_panel#gamepanel"}, &out)
	if err != nil {
		return "", err
	}
	for _, o := range out {
		if o.Success != nil && o.Success.Username != "" {
			return o.Success.Username, nil
		}
	}
	return "", errors.New("Hue: keine Antwort der Bridge")
}

// SmartLight is a lamp or speaker the UI can offer.
type SmartLight struct {
	ID   string `json:"id"`
	Name string `json:"name"`
	Kind string `json:"kind,omitempty"` // HA: light | media_player | tts | notify
}

// HueLights lists the lamps of the bridge.
func HueLights(cfg store.SmartHome) ([]SmartLight, error) {
	var m map[string]struct {
		Name string `json:"name"`
	}
	if err := hueCall("GET", hueURL(cfg, "/lights"), nil, &m); err != nil {
		return nil, err
	}
	out := []SmartLight{}
	for id, l := range m {
		out = append(out, SmartLight{ID: id, Name: l.Name, Kind: "light"})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out, nil
}

// ---------- Home Assistant (REST API) ----------

func haCall(cfg store.SmartHome, method, path string, body any, out any) error {
	if cfg.HAURL == "" || cfg.HAToken == "" {
		return errors.New("Home Assistant ist nicht eingerichtet")
	}
	var rd io.Reader
	if body != nil {
		b, _ := json.Marshal(body)
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(context.Background(), method, strings.TrimSuffix(cfg.HAURL, "/")+path, rd)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+cfg.HAToken)
	req.Header.Set("Content-Type", "application/json")
	resp, err := smartClient.Do(req)
	if err != nil {
		return fmt.Errorf("Home Assistant nicht erreichbar (%v)", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	switch {
	case resp.StatusCode == 401:
		return errors.New("Home Assistant: Token ungültig")
	case resp.StatusCode == 400 || resp.StatusCode == 404:
		return fmt.Errorf("Home Assistant: %s (HTTP %d)", strings.TrimSpace(string(b[:min(len(b), 200)])), resp.StatusCode)
	case resp.StatusCode >= 300:
		return fmt.Errorf("Home Assistant: HTTP %d", resp.StatusCode)
	}
	if out != nil {
		return json.Unmarshal(b, out)
	}
	return nil
}

func haCallService(cfg store.SmartHome, service string, data map[string]any) error {
	domain, svc, ok := strings.Cut(service, ".")
	if !ok || domain == "" || svc == "" || strings.ContainsAny(service, "/?#") {
		return errors.New("Dienst bitte als domain.dienst angeben")
	}
	return haCall(cfg, "POST", "/api/services/"+domain+"/"+svc, data, nil)
}

func haFlash(cfg store.SmartHome, r store.SmartRule) error {
	c := smartColors[r.Color]
	restore := effectTime(r.Effect)
	if restore > 0 {
		// remember the lamps as they are, to switch back afterwards
		if err := haCallService(cfg, "scene.create", map[string]any{"scene_id": "bytewise_vorher_" + r.ID, "snapshot_entities": r.Targets}); err != nil {
			return err
		}
	}
	data := map[string]any{"entity_id": r.Targets, "rgb_color": c.RGB, "brightness": 255}
	if r.Effect == "pulse" {
		data["flash"] = "long"
	}
	if err := haCallService(cfg, "light.turn_on", data); err != nil {
		return err
	}
	if restore > 0 {
		go func() {
			time.Sleep(restore)
			_ = haCallService(cfg, "scene.turn_on", map[string]any{"entity_id": "scene.bytewise_vorher_" + r.ID})
		}()
	}
	return nil
}

func haAnnounce(cfg store.SmartHome, r store.SmartRule, v smartVars) error {
	msg := renderSmart(r.Message, v, false)
	switch {
	case r.Service == "tts.speak":
		var tts string
		var players []string
		for _, t := range r.Targets {
			if strings.HasPrefix(t, "tts.") {
				tts = t
			} else if strings.HasPrefix(t, "media_player.") {
				players = append(players, t)
			}
		}
		if tts == "" || len(players) == 0 {
			return errors.New("Für tts.speak bitte eine Sprachausgabe (tts.…) und einen Lautsprecher (media_player.…) wählen")
		}
		return haCallService(cfg, "tts.speak", map[string]any{"entity_id": tts, "media_player_entity_id": players, "message": msg})
	case strings.HasPrefix(r.Service, "notify.alexa_media"):
		return haCallService(cfg, r.Service, map[string]any{"message": msg, "data": map[string]any{"type": "announce"}})
	case strings.HasPrefix(r.Service, "notify."):
		return haCallService(cfg, r.Service, map[string]any{"title": "Bytewise Panel", "message": msg})
	}
	data := map[string]any{"message": msg}
	if len(r.Targets) > 0 {
		data["entity_id"] = r.Targets
	}
	return haCallService(cfg, r.Service, data)
}

func haService(cfg store.SmartHome, r store.SmartRule, v smartVars) error {
	data := map[string]any{}
	if s := strings.TrimSpace(r.Data); s != "" {
		if err := json.Unmarshal([]byte(renderSmart(s, v, true)), &data); err != nil {
			return errors.New("Die Zusatzdaten sind kein gültiges JSON")
		}
	}
	if len(r.Targets) > 0 {
		data["entity_id"] = r.Targets
	}
	return haCallService(cfg, r.Service, data)
}

// HAEntities lists lights, speakers, voices and notify services for the UI.
func HAEntities(cfg store.SmartHome) ([]SmartLight, error) {
	var states []struct {
		ID    string `json:"entity_id"`
		Attrs struct {
			Name string `json:"friendly_name"`
		} `json:"attributes"`
	}
	if err := haCall(cfg, "GET", "/api/states", nil, &states); err != nil {
		return nil, err
	}
	out := []SmartLight{}
	for _, s := range states {
		kind, _, _ := strings.Cut(s.ID, ".")
		if kind != "light" && kind != "media_player" && kind != "tts" {
			continue
		}
		name := s.Attrs.Name
		if name == "" {
			name = s.ID
		}
		out = append(out, SmartLight{ID: s.ID, Name: name, Kind: kind})
	}
	var services []struct {
		Domain   string                     `json:"domain"`
		Services map[string]json.RawMessage `json:"services"`
	}
	if haCall(cfg, "GET", "/api/services", nil, &services) == nil {
		for _, d := range services {
			if d.Domain != "notify" {
				continue
			}
			for name := range d.Services {
				out = append(out, SmartLight{ID: "notify." + name, Name: "notify." + name, Kind: "notify"})
			}
		}
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Kind != out[j].Kind {
			return out[i].Kind < out[j].Kind
		}
		return strings.ToLower(out[i].Name) < strings.ToLower(out[j].Name)
	})
	return out, nil
}

// HACheck tests URL and token.
func HACheck(cfg store.SmartHome) error {
	var out struct {
		Message string `json:"message"`
	}
	return haCall(cfg, "GET", "/api/", nil, &out)
}
