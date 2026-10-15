package api

import (
	"fmt"
	"net/http"
	"sort"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/nodes"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Calendar feed (iCalendar) with the planned game events and maintenance,
// so friends can subscribe in Google, Apple or Outlook calendars.

func (a *API) routesCalendar(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/public/calendar/{file}", a.calendarFeed)
	mux.HandleFunc("PUT /api/status-page/calendar", a.need(auth.Admin, a.calendarToken))
}

func (a *API) calendarToken(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Enabled bool `json:"enabled"`
		Renew   bool `json:"renew"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	var tok string
	_ = a.Store.Update(func(d *store.Data) error {
		switch {
		case !in.Enabled:
			d.Settings.Status.CalToken = ""
		case in.Renew || d.Settings.Status.CalToken == "":
			d.Settings.Status.CalToken = newToken()
		}
		tok = d.Settings.Status.CalToken
		return nil
	})
	writeJSON(w, 200, map[string]string{"token": tok})
}

// publicEvent is an event as friends see it.
type publicEvent struct {
	ID     string    `json:"id"`
	Server string    `json:"server"`
	Name   string    `json:"name"`
	Start  time.Time `json:"start"`
	End    time.Time `json:"end"`
	Weekly bool      `json:"weekly,omitempty"`
	Active bool      `json:"active,omitempty"`
	What   string    `json:"what,omitempty"` // changed values in short
	Kind   string    `json:"kind"`           // event | wartung
}

// calendarServers are the servers on the status page, or all if none is picked.
func (a *API) calendarServers(c store.StatusConfig) []store.Server {
	var all, shown []store.Server
	for _, sv := range a.Store.Servers() {
		all = append(all, sv)
		if e, ok := c.Servers[sv.ID]; ok && e.Show {
			shown = append(shown, sv)
		}
	}
	if len(shown) == 0 {
		return all
	}
	return shown
}

func (a *API) publicEvents(servers []store.Server, horizon time.Duration) []publicEvent {
	now := time.Now()
	out := []publicEvent{}
	for _, sv := range servers {
		for _, ev := range sv.GameEvents {
			if ev.State == "beendet" || ev.End.Before(now) || ev.Start.After(now.Add(horizon)) {
				continue
			}
			var what []string
			for i, ch := range ev.Changes {
				if i == 4 {
					what = append(what, "…")
					break
				}
				l := ch.Label
				if l == "" {
					l = ch.Key
				}
				what = append(what, l+": "+ch.Value)
			}
			out = append(out, publicEvent{ID: ev.ID, Server: sv.Name, Name: ev.Name, Start: ev.Start, End: ev.End, Weekly: ev.Weekly,
				Active: ev.State == "aktiv", What: strings.Join(what, ", "), Kind: "event"})
		}
		if mt := sv.Maintenance; mt != nil && !mt.Until.IsZero() && mt.Until.After(now) {
			out = append(out, publicEvent{ID: "mt-" + sv.ID, Server: sv.Name, Name: "Wartung", Start: mt.Since, End: mt.Until,
				Active: true, What: mt.Note, Kind: "wartung"})
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Start.Before(out[j].Start) })
	return out
}

func icsEscape(s string) string {
	r := strings.NewReplacer(`\`, `\\`, ";", `\;`, ",", `\,`, "\n", `\n`, "\r", "")
	return r.Replace(s)
}

// icsFold keeps lines under 75 bytes as RFC 5545 asks.
func icsFold(line string) string {
	var b strings.Builder
	n := 0
	for _, r := range line {
		l := len(string(r))
		if n+l > 73 {
			b.WriteString("\r\n ")
			n = 1
		}
		b.WriteRune(r)
		n += l
	}
	return b.String() + "\r\n"
}

func (a *API) calendarFeed(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	c := a.statusCfg()
	tok := strings.TrimSuffix(r.PathValue("file"), ".ics")
	if c.CalToken == "" || len(tok) < 16 || tok != c.CalToken {
		http.NotFound(w, r)
		return
	}
	title := c.Title
	if title == "" {
		title = "Unsere Gameserver"
	}
	stamp := time.Now().UTC().Format("20060102T150405Z")
	var b strings.Builder
	line := func(s string) { b.WriteString(icsFold(s)) }
	line("BEGIN:VCALENDAR")
	line("VERSION:2.0")
	line("PRODID:-//Bytewise Panel//Events//DE")
	line("CALSCALE:GREGORIAN")
	line("METHOD:PUBLISH")
	line("X-WR-CALNAME:" + icsEscape(title+" · Events"))
	line("REFRESH-INTERVAL;VALUE=DURATION:PT1H")
	line("X-PUBLISHED-TTL:PT1H")
	events := a.publicEvents(a.calendarServers(c), 60*24*time.Hour)
	remote := pubOpts{Notes: map[string]string{}, Horizon: 60 * 24 * time.Hour}
	for id, e := range c.Servers {
		if e.Show && strings.Contains(id, nodes.Sep) {
			remote.Notes[id] = ""
		}
	}
	if len(remote.Notes) > 0 { // servers of other machines on the status page
		var v pubView
		a.addRemotePublic(&v, remote)
		events = append(events, v.Events...)
	}
	for _, ev := range events {
		line("BEGIN:VEVENT")
		line("UID:" + ev.ID + "@bytewise-panel")
		line("DTSTAMP:" + stamp)
		line("DTSTART:" + ev.Start.UTC().Format("20060102T150405Z"))
		line("DTEND:" + ev.End.UTC().Format("20060102T150405Z"))
		sum := ev.Name + " · " + ev.Server
		if ev.Kind == "wartung" {
			sum = "Wartung · " + ev.Server
		}
		line("SUMMARY:" + icsEscape(sum))
		if ev.What != "" {
			line("DESCRIPTION:" + icsEscape(ev.What))
		}
		if ev.Weekly {
			line("RRULE:FREQ=WEEKLY")
		}
		line("END:VEVENT")
	}
	line("END:VCALENDAR")
	w.Header().Set("Content-Type", "text/calendar; charset=utf-8")
	w.Header().Set("Content-Disposition", fmt.Sprintf(`inline; filename="%s.ics"`, "events"))
	_, _ = w.Write([]byte(b.String()))
}
