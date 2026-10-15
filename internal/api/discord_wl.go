package api

import (
	"context"
	"fmt"
	"maps"
	"net/http"
	"regexp"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/discord"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Whitelist through Discord: members with the configured role put their
// player name on the whitelist with /whitelist. The panel checks the role
// regularly and takes the name off again when it is gone.

var rePlayer = regexp.MustCompile(`^[A-Za-z0-9_.\-]{2,32}$`)

func (a *API) routesDiscordWL(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/discord/links", a.need(auth.Admin, a.wlLinks))
	mux.HandleFunc("DELETE /api/discord/links/{user}", a.need(auth.Admin, a.wlUnlink))
}

func (a *API) wlLinks(w http.ResponseWriter, r *http.Request) {
	var links []store.DiscordLink
	a.Store.Read(func(d *store.Data) { links = slices.Clone(d.Settings.DiscordLinks) })
	if links == nil {
		links = []store.DiscordLink{}
	}
	type srv struct {
		ID   string `json:"id"`
		Name string `json:"name"`
	}
	can := []srv{}
	for _, s := range a.Store.Servers() {
		if a.Mgr.CanWhitelist(s.ID) {
			can = append(can, srv{s.ID, s.Name})
		}
	}
	writeJSON(w, 200, map[string]any{"links": links, "servers": can})
}

func (a *API) wlUnlink(w http.ResponseWriter, r *http.Request) {
	user := r.PathValue("user")
	Note(r, user)
	_ = a.Store.Update(func(d *store.Data) error {
		for i := range d.Settings.DiscordLinks {
			if d.Settings.DiscordLinks[i].User == user {
				d.Settings.DiscordLinks[i].Player, d.Settings.DiscordLinks[i].Active = "", false
			}
		}
		return nil
	})
	go a.wlSync(context.Background(), false)
	writeJSON(w, 200, map[string]bool{"ok": true})
}

// wlCommand handles /whitelist.
func (a *API) wlCommand(c discord.Config, in discord.Interaction) map[string]any {
	if c.WLRole == "" || len(c.WLServers) == 0 {
		return ephemeral("Die Whitelist über Discord ist im Panel nicht eingerichtet.")
	}
	uid, uname, roles := in.Who()
	if in.Member == nil {
		return ephemeral("Bitte den Befehl auf dem Discord-Server benutzen, nicht im Privatchat.")
	}
	if !slices.Contains(roles, c.WLRole) {
		return ephemeral("Dafür brauchst du die passende Rolle auf diesem Discord-Server. Frag einen Admin.")
	}
	name, _ := in.Opt("spielername")
	name = strings.TrimSpace(name)
	if !rePlayer.MatchString(name) {
		return ephemeral("Der Spielername darf nur Buchstaben, Ziffern und _ . - enthalten (2 bis 32 Zeichen).")
	}
	var taken bool
	_ = a.Store.Update(func(d *store.Data) error {
		for _, l := range d.Settings.DiscordLinks {
			if l.User != uid && strings.EqualFold(l.Player, name) {
				taken = true
				return nil
			}
		}
		for i := range d.Settings.DiscordLinks {
			l := &d.Settings.DiscordLinks[i]
			if l.User == uid {
				l.Player, l.Name, l.Active, l.Checked, l.Note = name, uname, true, time.Now(), ""
				return nil
			}
		}
		d.Settings.DiscordLinks = append(d.Settings.DiscordLinks, store.DiscordLink{User: uid, Name: uname, Player: name, Active: true,
			Since: time.Now(), Checked: time.Now(), Applied: map[string]string{}})
		return nil
	})
	if taken {
		return ephemeral("Diesen Spielernamen hat schon jemand anderes eingetragen. Wenn das ein Fehler ist, frag einen Admin.")
	}
	var names []string
	for _, id := range c.WLServers {
		if sv, ok := a.Store.Server(id); ok {
			names = append(names, sv.Name)
		}
	}
	go a.wlSync(context.Background(), false)
	return ephemeral(fmt.Sprintf("✅ %s kommt auf die Whitelist von: %s. Server, die gerade aus sind, bekommen den Eintrag beim nächsten Start.", name, strings.Join(names, ", ")))
}

var wlMu sync.Mutex

// wlSync checks roles (every 30 minutes, or all with force) and brings the
// whitelists in line with the links. Servers that are off are done later.
func (a *API) wlSync(ctx context.Context, force bool) {
	wlMu.Lock()
	defer wlMu.Unlock()
	c := a.discordCfg()
	var links []store.DiscordLink
	a.Store.Read(func(d *store.Data) {
		for _, l := range d.Settings.DiscordLinks {
			l.Applied = maps.Clone(l.Applied)
			links = append(links, l)
		}
	})
	if len(links) == 0 {
		return
	}
	for i := range links {
		l := &links[i]
		if l.Applied == nil {
			l.Applied = map[string]string{}
		}
		// role check
		if l.Player != "" && c.Token != "" && c.GuildID != "" && c.WLRole != "" && (force || time.Since(l.Checked) > 30*time.Minute) {
			cctx, cancel := context.WithTimeout(ctx, 20*time.Second)
			roles, member, err := discord.MemberRoles(cctx, c, l.User)
			cancel()
			if err == nil {
				was := l.Active
				l.Active, l.Checked, l.Note = member && slices.Contains(roles, c.WLRole), time.Now(), ""
				if was && !l.Active {
					l.Note = "Rolle weg oder Discord-Server verlassen"
					a.Mgr.EventKind(firstOr(c.WLServers), "info", "", fmt.Sprintf("Whitelist: %s (%s) hat die Rolle nicht mehr und wird entfernt", l.Player, l.Name))
				}
			}
		}
		if c.WLRole == "" {
			l.Active = false
		}
		// what should be where
		want := map[string]string{}
		if l.Active && l.Player != "" {
			for _, id := range c.WLServers {
				want[id] = l.Player
			}
		}
		for id, name := range l.Applied {
			if want[id] == name || !a.Mgr.IsUp(id) {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if _, err := a.Mgr.PlayerAction(cctx, id, "whitelist_remove", name, ""); err == nil || strings.Contains(err.Error(), "nicht gefunden") {
				delete(l.Applied, id)
			} else if _, ok := a.Store.Server(id); !ok {
				delete(l.Applied, id) // server deleted
			}
			cancel()
		}
		for id, name := range want {
			if l.Applied[id] == name || !a.Mgr.IsUp(id) {
				continue
			}
			cctx, cancel := context.WithTimeout(ctx, 30*time.Second)
			if _, err := a.Mgr.PlayerAction(cctx, id, "whitelist_add", name, ""); err == nil {
				l.Applied[id] = name
			} else {
				l.Note = err.Error()
			}
			cancel()
		}
	}
	// write back, keeping links made meanwhile; drop removed ones that are clean
	_ = a.Store.Update(func(d *store.Data) error {
		byUser := map[string]store.DiscordLink{}
		for _, l := range links {
			byUser[l.User] = l
		}
		var out []store.DiscordLink
		for _, cur := range d.Settings.DiscordLinks {
			l, ok := byUser[cur.User]
			if !ok {
				out = append(out, cur)
				continue
			}
			if cur.Player != l.Player && cur.Player != "" {
				// relinked while we worked: keep the new name, our progress on Applied stays
				l.Player, l.Active = cur.Player, cur.Active
			}
			if l.Player == "" && len(l.Applied) == 0 {
				continue
			}
			out = append(out, l)
		}
		d.Settings.DiscordLinks = out
		return nil
	})
}

func firstOr(s []string) string {
	if len(s) > 0 {
		return s[0]
	}
	return ""
}
