package api

import (
	"errors"
	"fmt"
	"net/http"
	"strings"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/stream"
)

// Streaming settings: Twitch channel, YouTube stream and viewer rules.

func (a *API) routesStreaming(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/streaming", a.need(auth.Admin, a.streamGet))
	mux.HandleFunc("PUT /api/streaming", a.need(auth.Admin, a.streamPut))
	mux.HandleFunc("PUT /api/streaming/rules", a.need(auth.Admin, a.streamRulesPut))
	mux.HandleFunc("POST /api/streaming/rules/{rid}/test", a.need(auth.Admin, a.streamRuleTest))
}

func (a *API) streamGet(w http.ResponseWriter, r *http.Request) {
	var c store.Streaming
	a.Store.Read(func(d *store.Data) {
		c = d.Stream
		c.Rules = append([]store.StreamRule{}, d.Stream.Rules...)
	})
	keySet := c.YTKey != ""
	c.YTKey = ""
	out := map[string]any{"config": c, "yt_key_set": keySet}
	if a.Mgr != nil {
		out["state"] = a.Mgr.StreamState()
	}
	writeJSON(w, 200, out)
}

func (a *API) streamPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		TwitchChannel string `json:"twitch_channel"`
		TwitchOn      bool   `json:"twitch_on"`
		YTKey         string `json:"yt_key"`
		YTVideo       string `json:"yt_video"`
		YTOn          bool   `json:"yt_on"`
		Streamer      string `json:"streamer"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	ch := ""
	if strings.TrimSpace(in.TwitchChannel) != "" {
		var err error
		if ch, err = stream.CleanChannel(in.TwitchChannel); err != nil {
			fail(w, 400, err)
			return
		}
	}
	if in.TwitchOn && ch == "" {
		fail(w, 400, errors.New("Bitte den Twitch-Kanal angeben"))
		return
	}
	video := strings.TrimSpace(in.YTVideo)
	if video != "" {
		if _, err := stream.VideoID(video); err != nil {
			fail(w, 400, err)
			return
		}
	}
	streamer := strings.TrimSpace(in.Streamer)
	if len(streamer) > 32 || strings.ContainsAny(streamer, " ;\"'\\/") {
		fail(w, 400, errors.New("Der Spielername darf keine Leer- oder Sonderzeichen enthalten"))
		return
	}
	err := a.Store.Update(func(d *store.Data) error {
		s := &d.Stream
		if k := strings.TrimSpace(in.YTKey); k != "" {
			s.YTKey = k
		}
		if in.YTOn && (s.YTKey == "" || video == "") {
			return errors.New("Für YouTube bitte API-Schlüssel und Link zum laufenden Stream angeben")
		}
		s.TwitchChannel, s.TwitchOn, s.YTVideo, s.YTOn, s.Streamer = ch, in.TwitchOn, video, in.YTOn, streamer
		return nil
	})
	if err != nil {
		fail(w, 400, err)
		return
	}
	if a.Mgr != nil {
		a.Mgr.ApplyStreaming()
	}
	a.streamGet(w, r)
}

func (a *API) streamRulesPut(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Rules []store.StreamRule `json:"rules"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if len(in.Rules) > 50 {
		fail(w, 400, errors.New("Höchstens 50 Regeln"))
		return
	}
	for i := range in.Rules {
		if !a.fleetHas(in.Rules[i].Server) || strings.Contains(in.Rules[i].Server, "~") {
			fail(w, 400, fmt.Errorf("Regel %d: bitte einen Server auf diesem Rechner wählen", i+1))
			return
		}
		if err := manager.ValidateStreamRule(&in.Rules[i]); err != nil {
			fail(w, 400, fmt.Errorf("Regel %d: %w", i+1, err))
			return
		}
	}
	_ = a.Store.Update(func(d *store.Data) error {
		old := map[string]store.StreamRule{}
		for _, x := range d.Stream.Rules {
			old[x.ID] = x
		}
		for i := range in.Rules {
			if o, ok := old[in.Rules[i].ID]; ok && in.Rules[i].ID != "" {
				in.Rules[i].LastAt = o.LastAt
			} else {
				in.Rules[i].ID = newToken()[:10]
			}
		}
		d.Stream.Rules = in.Rules
		return nil
	})
	a.streamGet(w, r)
}

func (a *API) streamRuleTest(w http.ResponseWriter, r *http.Request) {
	var rule store.StreamRule
	found := false
	a.Store.Read(func(d *store.Data) {
		for _, x := range d.Stream.Rules {
			if x.ID == r.PathValue("rid") {
				rule, found = x, true
			}
		}
	})
	if !found {
		fail(w, 404, errNotFound)
		return
	}
	writeJSON(w, 200, map[string]string{"result": a.Mgr.TestStreamRule(rule)})
}
