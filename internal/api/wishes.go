package api

import (
	"crypto/rand"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"net/http"
	"sort"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Wish list on the status page: players suggest games, mods or anything
// else and vote; the admin answers in the panel. No login, so every visitor
// counts once per wish (by a salted hash of the address) and the number of
// new wishes and votes per address is limited.

func (a *API) routesWishes(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/public/wishes", a.wishesPublic)
	mux.HandleFunc("POST /api/public/wishes", a.wishAdd)
	mux.HandleFunc("POST /api/public/wishes/{wid}/vote", a.wishVote)
	mux.HandleFunc("GET /api/wishes", a.need(auth.Admin, a.wishesAdmin))
	mux.HandleFunc("PATCH /api/wishes/{wid}", a.need(auth.Admin, a.wishPatch))
	mux.HandleFunc("DELETE /api/wishes/{wid}", a.need(auth.Admin, a.wishDelete))
}

var wishKinds = map[string]bool{"spiel": true, "mod": true, "sonstiges": true}
var wishStates = map[string]bool{"offen": true, "geplant": true, "umgesetzt": true, "abgelehnt": true}

// limiter allows n actions per address and window.
type limiter struct {
	mu   sync.Mutex
	seen map[string][]time.Time
}

func (l *limiter) allow(key string, n int, window time.Duration) bool {
	l.mu.Lock()
	defer l.mu.Unlock()
	if l.seen == nil {
		l.seen = map[string][]time.Time{}
	}
	now := time.Now()
	keep := l.seen[key][:0]
	for _, t := range l.seen[key] {
		if now.Sub(t) < window {
			keep = append(keep, t)
		}
	}
	if len(keep) >= n {
		l.seen[key] = keep
		return false
	}
	l.seen[key] = append(keep, now)
	if len(l.seen) > 5000 { // forget old addresses
		for k, v := range l.seen {
			if len(v) == 0 || now.Sub(v[len(v)-1]) > window {
				delete(l.seen, k)
			}
		}
	}
	return true
}

var wishLimit, voteLimit limiter

// visitor is a salted hash of the visitor's address: equal for the same
// address, but the address cannot be read back from panel.json.
func (a *API) visitor(r *http.Request, extra string) string {
	salt := ""
	a.Store.Read(func(d *store.Data) { salt = d.WishSalt })
	if salt == "" {
		_ = a.Store.Update(func(d *store.Data) error {
			if d.WishSalt == "" {
				b := make([]byte, 16)
				_, _ = rand.Read(b)
				d.WishSalt = hex.EncodeToString(b)
			}
			salt = d.WishSalt
			return nil
		})
	}
	s := sha256.Sum256([]byte(salt + "|" + auth.ClientIP(r) + "|" + extra))
	return hex.EncodeToString(s[:12])
}

type publicWish struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Kind    string    `json:"kind"`
	By      string    `json:"by,omitempty"`
	Created time.Time `json:"created"`
	Votes   int       `json:"votes"`
	Status  string    `json:"status"`
	Answer  string    `json:"answer,omitempty"`
	Voted   bool      `json:"voted,omitempty"`
}

func sortWishes(list []store.Wish) {
	rank := map[string]int{"geplant": 0, "offen": 1, "umgesetzt": 2, "abgelehnt": 3}
	sort.SliceStable(list, func(i, j int) bool {
		if rank[list[i].Status] != rank[list[j].Status] {
			return rank[list[i].Status] < rank[list[j].Status]
		}
		if list[i].Votes != list[j].Votes {
			return list[i].Votes > list[j].Votes
		}
		return list[i].Created.After(list[j].Created)
	})
}

func (a *API) wishesPublic(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	if !a.statusCfg().Enabled || !a.statusCfg().Wishes {
		fail(w, 404, errNotFound)
		return
	}
	var list []store.Wish
	a.Store.Read(func(d *store.Data) { list = append(list, d.Wishes...) })
	sortWishes(list)
	out := []publicWish{}
	for _, x := range list {
		// done and refused wishes stay visible for two months
		if (x.Status == "umgesetzt" || x.Status == "abgelehnt") && time.Since(x.Created) > 60*24*time.Hour {
			continue
		}
		v := a.visitor(r, x.ID)
		out = append(out, publicWish{ID: x.ID, Title: x.Title, Kind: x.Kind, By: x.By, Created: x.Created, Votes: x.Votes,
			Status: x.Status, Answer: x.Answer, Voted: containsStr(x.Voters, v)})
	}
	writeJSON(w, 200, map[string]any{"wishes": out})
}

func containsStr(list []string, s string) bool {
	for _, x := range list {
		if x == s {
			return true
		}
	}
	return false
}

// cleanText keeps one line of plain text.
func cleanText(s string, max int) string {
	s = strings.Join(strings.Fields(strings.Map(func(c rune) rune {
		if c < 32 || c == 127 {
			return ' '
		}
		return c
	}, s)), " ")
	if utf8.RuneCountInString(s) > max {
		s = string([]rune(s)[:max])
	}
	return s
}

func (a *API) wishAdd(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	c := a.statusCfg()
	if !c.Enabled || !c.Wishes {
		fail(w, 404, errNotFound)
		return
	}
	var in struct {
		Title   string `json:"title"`
		Kind    string `json:"kind"`
		By      string `json:"by"`
		Website string `json:"website"` // honeypot: people leave it empty, bots fill it
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if in.Website != "" {
		writeJSON(w, 201, map[string]bool{"ok": true}) // pretend it worked
		return
	}
	title, by := cleanText(in.Title, 80), cleanText(in.By, 30)
	if utf8.RuneCountInString(title) < 3 {
		fail(w, 400, errors.New("Bitte kurz beschreiben, was du dir wünschst (mindestens 3 Zeichen)"))
		return
	}
	if !wishKinds[in.Kind] {
		in.Kind = "sonstiges"
	}
	ip := a.visitor(r, "")
	if !wishLimit.allow(ip, 3, time.Hour) {
		fail(w, 429, errors.New("Du hast gerade schon ein paar Wünsche eingetragen. Bitte später wieder."))
		return
	}
	wish := store.Wish{ID: newToken()[:10], Title: title, Kind: in.Kind, By: by, Created: time.Now(), Status: "offen", IP: ip, Votes: 1}
	wish.Voters = []string{a.visitor(r, wish.ID)} // the sender's vote counts right away
	err := a.Store.Update(func(d *store.Data) error {
		open := 0
		for _, x := range d.Wishes {
			if x.Status == "offen" {
				open++
			}
			if strings.EqualFold(x.Title, title) && x.Status != "abgelehnt" {
				return errors.New("Das hat sich schon jemand gewünscht. Stimm einfach dafür.")
			}
		}
		if open >= 200 {
			return errors.New("Die Wunschliste ist gerade voll.")
		}
		d.Wishes = append(d.Wishes, wish)
		return nil
	})
	if err != nil {
		fail(w, 400, err)
		return
	}
	who := ""
	if by != "" {
		who = " (von " + by + ")"
	}
	if a.Mgr != nil {
		a.Mgr.NotifyPanel("wish", "info", "Neuer Wunsch auf der Statusseite", title+who)
	}
	writeJSON(w, 201, map[string]bool{"ok": true})
}

func (a *API) wishVote(w http.ResponseWriter, r *http.Request) {
	noAudit(r)
	c := a.statusCfg()
	if !c.Enabled || !c.Wishes {
		fail(w, 404, errNotFound)
		return
	}
	if !voteLimit.allow(a.visitor(r, ""), 60, time.Hour) {
		fail(w, 429, errors.New("Bitte etwas langsamer abstimmen."))
		return
	}
	wid := r.PathValue("wid")
	v := a.visitor(r, wid)
	voted, votes := false, 0
	err := a.Store.Update(func(d *store.Data) error {
		for i := range d.Wishes {
			x := &d.Wishes[i]
			if x.ID != wid {
				continue
			}
			if x.Status == "umgesetzt" || x.Status == "abgelehnt" {
				return errors.New("Darüber wird nicht mehr abgestimmt.")
			}
			if j := indexStr(x.Voters, v); j >= 0 { // second click takes the vote back
				x.Voters = append(x.Voters[:j], x.Voters[j+1:]...)
				x.Votes = max(0, x.Votes-1)
			} else {
				if len(x.Voters) < 2000 {
					x.Voters = append(x.Voters, v)
				}
				x.Votes++
				voted = true
			}
			votes = x.Votes
			return nil
		}
		return errNotFound
	})
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]any{"voted": voted, "votes": votes})
}

func indexStr(list []string, s string) int {
	for i, x := range list {
		if x == s {
			return i
		}
	}
	return -1
}

func (a *API) wishesAdmin(w http.ResponseWriter, r *http.Request) {
	var list []store.Wish
	a.Store.Read(func(d *store.Data) { list = append(list, d.Wishes...) })
	sortWishes(list)
	for i := range list {
		list[i].Voters, list[i].IP = nil, ""
	}
	if list == nil {
		list = []store.Wish{}
	}
	writeJSON(w, 200, map[string]any{"wishes": list})
}

func (a *API) wishPatch(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Status *string `json:"status"`
		Answer *string `json:"answer"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if in.Status != nil && !wishStates[*in.Status] {
		fail(w, 400, errors.New("Unbekannter Stand"))
		return
	}
	err := a.Store.Update(func(d *store.Data) error {
		for i := range d.Wishes {
			if d.Wishes[i].ID == r.PathValue("wid") {
				if in.Status != nil {
					d.Wishes[i].Status = *in.Status
				}
				if in.Answer != nil {
					d.Wishes[i].Answer = cleanText(*in.Answer, 200)
				}
				Note(r, d.Wishes[i].Title)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		fail(w, 404, err)
		return
	}
	a.wishesAdmin(w, r)
}

func (a *API) wishDelete(w http.ResponseWriter, r *http.Request) {
	err := a.Store.Update(func(d *store.Data) error {
		for i := range d.Wishes {
			if d.Wishes[i].ID == r.PathValue("wid") {
				Note(r, d.Wishes[i].Title)
				d.Wishes = append(d.Wishes[:i], d.Wishes[i+1:]...)
				return nil
			}
		}
		return errNotFound
	})
	if err != nil {
		fail(w, 404, err)
		return
	}
	a.wishesAdmin(w, r)
}
