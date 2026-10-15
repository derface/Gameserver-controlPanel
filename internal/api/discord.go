package api

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/discord"
	"github.com/derface/Gameserver-controlPanel/internal/nodes"
	"github.com/derface/Gameserver-controlPanel/internal/proxy"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Discord bot: slash commands come in as signed HTTP requests from
// Discord, the status message is kept up to date by RunDiscord.

func (a *API) routesDiscord(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/discord/interactions", a.discordInteraction)
	mux.HandleFunc("GET /api/discord", a.need(auth.Admin, a.discordGet))
	mux.HandleFunc("PUT /api/discord", a.need(auth.Admin, a.discordPut))
	mux.HandleFunc("POST /api/discord/test", a.need(auth.Admin, a.discordTest))
	a.routesDiscordWL(mux)
}

func (a *API) discordCfg() discord.Config {
	var c discord.Config
	a.Store.Read(func(d *store.Data) { c = d.Settings.Discord })
	return c
}

func (a *API) discordGet(w http.ResponseWriter, r *http.Request) {
	c := a.discordCfg()
	c.Token = mask(c.Token)
	type row struct {
		ID      string `json:"id"`
		Name    string `json:"name"`
		Game    string `json:"game"`
		Allowed bool   `json:"allowed"`
	}
	rows := []row{}
	for _, s := range a.fleetInfos() {
		rows = append(rows, row{s.ID, a.fleetName(s), s.TemplateName, slices.Contains(c.Servers, s.ID)})
	}
	dc.Lock()
	st := map[string]any{"last_error": dc.lastErr, "last_update": dc.lastOK}
	dc.Unlock()
	writeJSON(w, 200, map[string]any{"config": c, "servers": rows, "state": st, "endpoint": a.discordEndpoint(r)})
}

// discordEndpoint is the URL to paste into the Discord developer portal.
func (a *API) discordEndpoint(r *http.Request) string {
	host := r.Host
	scheme := "https"
	if r.TLS == nil && !proxy.HTTPS(r) {
		scheme = "http"
	}
	return scheme + "://" + host + "/api/discord/interactions"
}

func (a *API) discordPut(w http.ResponseWriter, r *http.Request) {
	var in discord.Config
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	cur := a.discordCfg()
	if in.Token == "" || strings.Contains(in.Token, "••••") {
		in.Token = cur.Token
	}
	if err := in.Validate(); err != nil {
		fail(w, 400, err)
		return
	}
	var ok []string
	for _, id := range in.Servers {
		if a.fleetHas(id) && !slices.Contains(ok, id) {
			ok = append(ok, id)
		}
	}
	in.Servers = ok
	var wl []string
	for _, id := range in.WLServers {
		if a.fleetCanWhitelist(id) && !slices.Contains(wl, id) {
			wl = append(wl, id)
		}
	}
	in.WLServers = wl
	// a new channel needs a new message
	in.MessageID, in.Registered = cur.MessageID, cur.Registered
	if in.Channel != cur.Channel || in.Token != cur.Token {
		in.MessageID = ""
	}
	note := ""
	if in.Ready() && discord.Fingerprint(in) != in.Registered {
		ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
		err := discord.Register(ctx, in)
		cancel()
		if err != nil {
			note = "Gespeichert, aber die Befehle konnten nicht angemeldet werden: " + err.Error()
		} else {
			in.Registered = discord.Fingerprint(in)
			if in.GuildID == "" {
				note = "Befehle angemeldet. Ohne Server-ID kann es bis zu einer Stunde dauern, bis sie in Discord erscheinen."
			} else {
				note = "Befehle angemeldet, sie sind sofort in Discord da."
			}
		}
	}
	_ = a.Store.Update(func(d *store.Data) error { d.Settings.Discord = in; return nil })
	dc.Lock()
	dc.hash, dc.lastErr = "", "" // send the status message at the next tick
	dc.Unlock()
	go a.discordTick(context.Background(), true)
	go a.wlSync(context.Background(), false)
	c := in
	c.Token = mask(c.Token)
	writeJSON(w, 200, map[string]any{"config": c, "note": note})
}

// discordTest posts or refreshes the status message right now.
func (a *API) discordTest(w http.ResponseWriter, r *http.Request) {
	c := a.discordCfg()
	if c.Token == "" || c.Channel == "" {
		fail(w, 400, errors.New("Erst Bot-Token und Kanal-ID eintragen und speichern"))
		return
	}
	dc.Lock()
	dc.hash = ""
	dc.Unlock()
	if err := a.discordTick(r.Context(), true); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// cheapServers lists the allowed servers without asking for player names
// (fast enough for the 3-second answer).
func (a *API) cheapServers(c discord.Config) []publicServer {
	list := a.publicServers(func(id string) (bool, string) { return slices.Contains(c.Servers, id), "" }, false, "")
	for _, s := range a.fleetInfos() { // servers of other machines, from the last check
		if strings.Contains(s.ID, nodes.Sep) && slices.Contains(c.Servers, s.ID) {
			list = append(list, publicServer{ID: s.ID, Name: s.Name, Game: s.TemplateName, State: publicState(s), Players: max(s.Players, 0), Max: s.MaxPlayers})
		}
	}
	return list
}

// discordServers are the servers the bot may show, with the status page
// notes as hints (e.g. where the password is).
func (a *API) discordServers(c discord.Config) []publicServer {
	host := ""
	var sc store.StatusConfig
	a.Store.Read(func(d *store.Data) { host, sc = d.Settings.PublicHost, d.Settings.Status })
	o := pubOpts{Notes: map[string]string{}, Names: true, Host: host}
	for _, id := range c.Servers {
		o.Notes[id] = sc.Servers[id].Note
	}
	v := a.localPublic(o)
	a.addRemotePublic(&v, o)
	return v.Servers
}

var stateIcon = map[string]string{"online": "🟢", "startet": "🟡", "fehler": "🔴", "aus": "⚫", "wartung": "🛠️"}
var stateText = map[string]string{"online": "Online", "startet": "Startet", "fehler": "Fehler", "aus": "Aus", "wartung": "Wartung"}

func serverField(p publicServer, detail bool) discord.Field {
	var b strings.Builder
	b.WriteString(stateText[p.State])
	if p.State == "wartung" && p.Until != "" {
		if t, err := time.Parse(time.RFC3339, p.Until); err == nil {
			b.WriteString(" bis <t:" + strconv.FormatInt(t.Unix(), 10) + ":t>")
		}
	}
	if p.State == "online" {
		if p.Max > 0 {
			fmt.Fprintf(&b, " · %d/%d Spieler", p.Players, p.Max)
		} else {
			fmt.Fprintf(&b, " · %d Spieler", p.Players)
		}
		if len(p.Names) > 0 {
			n := p.Names
			if len(n) > 15 {
				n = append(n[:15:15], fmt.Sprintf("+%d", len(p.Names)-15))
			}
			b.WriteString("\n" + strings.Join(n, ", "))
		}
		if p.Address != "" {
			b.WriteString("\n`" + p.Address + "`")
		}
		if detail && p.Hint != "" {
			b.WriteString("\n" + p.Hint)
		}
	}
	if p.Note != "" {
		b.WriteString("\n" + p.Note)
	}
	v := b.String()
	if len(v) > 1000 {
		v = v[:1000]
	}
	return discord.Field{Name: stateIcon[p.State] + " " + p.Name + " · " + p.Game, Value: v}
}

func statusEmbed(list []publicServer) discord.Embed {
	e := discord.Embed{Title: "Gameserver", Color: 0xF28C28}
	on, players := 0, 0
	for i, p := range list {
		if i == 25 {
			break
		}
		if p.State == "online" {
			on++
			players += p.Players
		}
		e.Fields = append(e.Fields, serverField(p, false))
	}
	switch {
	case len(list) == 0:
		e.Description = "Im Panel sind für Discord noch keine Server freigegeben."
	case on == 0:
		e.Description = "Gerade läuft kein Server."
	default:
		e.Description = fmt.Sprintf("%d von %d Servern online, %d Spieler.", on, len(list), players)
	}
	return e
}

// dc keeps the state of the status message between ticks.
var dc struct {
	sync.Mutex
	hash    string
	at      time.Time
	lastErr string
	lastOK  time.Time
}

// RunDiscord keeps the status message up to date.
func (a *API) RunDiscord(ctx context.Context) {
	t := time.NewTicker(time.Minute)
	defer t.Stop()
	// new panel versions may bring new commands
	if c := a.discordCfg(); c.Ready() && discord.Fingerprint(c) != c.Registered {
		cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
		if discord.Register(cctx, c) == nil {
			_ = a.Store.Update(func(d *store.Data) error { d.Settings.Discord.Registered = discord.Fingerprint(c); return nil })
		}
		cancel()
	}
	n := 0
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			_ = a.discordTick(ctx, false)
			if n++; n%5 == 0 {
				a.wlSync(ctx, false)
			}
		}
	}
}

// discordTick edits the status message when something changed, at least
// every 15 minutes so the time stamp stays fresh.
func (a *API) discordTick(ctx context.Context, force bool) error {
	c := a.discordCfg()
	if c.Token == "" || c.Channel == "" {
		return nil
	}
	e := statusEmbed(a.discordServers(c))
	b, _ := json.Marshal(e)
	h := fmt.Sprintf("%x", sha256.Sum256(b))
	dc.Lock()
	same := dc.hash == h && time.Since(dc.at) < 15*time.Minute
	dc.Unlock()
	if same && !force && c.MessageID != "" {
		return nil
	}
	e.Footer = &discord.Footer{Text: "Bytewise Panel · aktualisiert sich selbst"}
	e.Timestamp = time.Now().UTC().Format(time.RFC3339)
	cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	var err error
	if c.MessageID != "" {
		err = discord.EditStatus(cctx, c, e)
	}
	if c.MessageID == "" || errors.Is(err, discord.ErrUnknownMessage) {
		var id string
		if id, err = discord.PostStatus(cctx, c, e); err == nil {
			_ = a.Store.Update(func(d *store.Data) error {
				if d.Settings.Discord.Channel == c.Channel {
					d.Settings.Discord.MessageID = id
				}
				return nil
			})
		}
	}
	dc.Lock()
	defer dc.Unlock()
	if err != nil {
		dc.lastErr = err.Error()
		return err
	}
	dc.hash, dc.at, dc.lastErr, dc.lastOK = h, time.Now(), "", time.Now()
	return nil
}

func ephemeral(text string) map[string]any {
	return map[string]any{"type": 4, "data": map[string]any{"content": text, "flags": 64, "allowed_mentions": map[string]any{"parse": []string{}}}}
}

func (a *API) discordInteraction(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	c := a.discordCfg()
	if c.PublicKey == "" {
		fail(w, 404, errNotFound)
		return
	}
	body, err := io.ReadAll(io.LimitReader(r.Body, 64<<10))
	if err != nil || !discord.Verify(c.PublicKey, r.Header.Get("X-Signature-Ed25519"), r.Header.Get("X-Signature-Timestamp"), body) {
		http.Error(w, "invalid request signature", http.StatusUnauthorized)
		return
	}
	var in discord.Interaction
	if err := json.Unmarshal(body, &in); err != nil {
		fail(w, 400, err)
		return
	}
	switch in.Type {
	case 1: // ping from the developer portal
		writeJSON(w, 200, map[string]int{"type": 1})
	case 4: // autocomplete for the server option
		q, _ := in.Opt("server")
		q = strings.ToLower(q)
		choices := []discord.Choice{}
		for _, p := range a.cheapServers(c) {
			if q == "" || strings.Contains(strings.ToLower(p.Name+" "+p.Game), q) {
				name := p.Name + " (" + stateText[p.State] + ")"
				if len(name) > 100 {
					name = name[:100]
				}
				choices = append(choices, discord.Choice{Name: name, Value: p.ID})
			}
			if len(choices) == 25 {
				break
			}
		}
		writeJSON(w, 200, map[string]any{"type": 8, "data": map[string]any{"choices": choices}})
	case 2:
		writeJSON(w, 200, a.discordCommand(c, in))
	default:
		fail(w, 400, errors.New("unbekannte Interaktion"))
	}
}

// findServer matches the option (an ID from autocomplete or a typed name)
// against the servers the bot may use.
func findServer(list []publicServer, q string) (publicServer, bool) {
	q = strings.TrimSpace(q)
	for _, p := range list {
		if p.ID == q {
			return p, true
		}
	}
	var hit []publicServer
	for _, p := range list {
		if strings.EqualFold(p.Name, q) {
			return p, true
		}
		if q != "" && strings.Contains(strings.ToLower(p.Name), strings.ToLower(q)) {
			hit = append(hit, p)
		}
	}
	if len(hit) == 1 {
		return hit[0], true
	}
	return publicServer{}, false
}

func (a *API) discordCommand(c discord.Config, in discord.Interaction) map[string]any {
	if in.Data.Name == "whitelist" {
		return a.wlCommand(c, in)
	}
	q, _ := in.Opt("server")
	if in.Data.Name == "status" {
		// asking the game servers for player names can take longer than the
		// 3 seconds Discord waits, so answer "thinking" and fill in later
		go func() {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			defer cancel()
			list := a.discordServers(c)
			e := statusEmbed(list)
			if q != "" {
				p, ok := findServer(list, q)
				if !ok {
					_ = discord.FollowUp(ctx, c.AppID, in.Token, "Diesen Server kenne ich nicht.")
					return
				}
				e = discord.Embed{Title: p.Name, Color: 0xF28C28, Fields: []discord.Field{serverField(p, true)}}
				if p.SteamLink != "" {
					e.Description = "Beitreten: " + p.SteamLink
				}
			}
			_ = discord.FollowUpEmbed(ctx, c.AppID, in.Token, e)
		}()
		return map[string]any{"type": 5}
	}
	list := a.cheapServers(c)
	verb := map[string][2]string{"start": {"Starte", "gestartet"}, "stop": {"Stoppe", "gestoppt"}, "restart": {"Starte", "neu gestartet"}}
	v, known := verb[in.Data.Name]
	if !known {
		return ephemeral("Diesen Befehl kenne ich nicht.")
	}
	if !c.Allowed(in) {
		return ephemeral("Dafür hast du keine Rechte. Ein Admin kann deine Rolle im Panel unter Einstellungen → Discord-Bot freigeben.")
	}
	p, ok := findServer(list, q)
	if !ok {
		return ephemeral("Diesen Server kenne ich nicht, oder er ist für Discord nicht freigegeben.")
	}
	switch {
	case p.State == "wartung":
		return ephemeral(p.Name + " ist gerade in Wartung. Bitte später nochmal.")
	case in.Data.Name == "start" && p.State != "aus" && p.State != "fehler":
		return ephemeral(p.Name + " läuft schon.")
	case in.Data.Name == "stop" && p.State == "aus":
		return ephemeral(p.Name + " ist schon aus.")
	}
	_, who, _ := in.Who()
	what := v[0] + " " + p.Name
	if in.Data.Name == "restart" {
		what = v[0] + " " + p.Name + " neu"
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Minute)
		defer cancel()
		err := a.fleetPower(ctx, p.ID, in.Data.Name, "Discord: "+who)
		var msg string
		if in.Data.Name == "start" || in.Data.Name == "restart" {
			msg = "✅ " + p.Name + " wurde " + v[1] + ". Bis man beitreten kann, dauert es oft noch ein, zwei Minuten."
		} else {
			msg = "✅ " + p.Name + " wurde " + v[1] + "."
		}
		if err != nil {
			msg = "❌ " + p.Name + ": " + err.Error()
		} else {
			a.Mgr.EventKind(p.ID, "info", "", fmt.Sprintf("Über Discord von %s: %s", who, what))
		}
		_ = discord.FollowUp(ctx, c.AppID, in.Token, msg)
		_ = a.discordTick(ctx, false)
	}()
	return map[string]any{"type": 4, "data": map[string]any{"content": "⏳ " + what + " … (angefragt von " + who + ")", "allowed_mentions": map[string]any{"parse": []string{}}}}
}
