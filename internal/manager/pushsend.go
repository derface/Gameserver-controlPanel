package manager

import (
	"context"
	"encoding/json"
	"errors"
	"slices"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/notify"
	"github.com/derface/Gameserver-controlPanel/internal/push"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Web push to phones and browsers where a user switched it on (installed
// panel app on Android/iOS, or a desktop browser). Each device chooses
// which kinds of events it wants; servers the user may not see are left out.

const pushSubject = "https://github.com/derface/Gameserver-controlPanel"

var pushLimit notify.Limiter

// PushKinds are the kinds a device can choose: the notification kinds plus
// players joining and status changes.
func PushKinds() []struct{ Key, Label string } {
	out := []struct{ Key, Label string }{
		{"players", "Spieler betreten einen Server"},
		{"status", "Server startet, läuft oder stoppt"},
	}
	return append(out, notify.Kinds...)
}

// DefaultPushKinds are on for a new device.
var DefaultPushKinds = []string{"crash", "error", "update", "wish"}

// PushKeys returns the VAPID keys, creating them on first use.
func (m *Manager) PushKeys() (store.PushKeys, error) {
	var k store.PushKeys
	m.st.Read(func(d *store.Data) { k = d.PushKeys })
	if k.Public != "" {
		return k, nil
	}
	nk, err := push.NewKeys()
	if err != nil {
		return k, err
	}
	err = m.st.Update(func(d *store.Data) error {
		if d.PushKeys.Public == "" {
			d.PushKeys = store.PushKeys{Private: nk.Private, Public: nk.Public}
		}
		k = d.PushKeys
		return nil
	})
	return k, err
}

type pushMsg struct {
	Title string `json:"title"`
	Body  string `json:"body"`
	Tag   string `json:"tag,omitempty"`
	URL   string `json:"url,omitempty"`
	Badge *int   `json:"badge,omitempty"` // servers with a problem; 0 clears the badge
}

// problemCount is the number shown on the app icon: servers with an error.
func (m *Manager) problemCount() int {
	m.mu.RLock()
	defer m.mu.RUnlock()
	n := 0
	for _, l := range m.live {
		if l.Phase == "error" {
			n++
		}
	}
	return n
}

// pushEvent sends an event to all devices that want its kind.
func (m *Manager) pushEvent(kind, event, serverID, level, text string) {
	if kind == "players" && event != "player.join" {
		return // joins only; counts and leaves would be too much on a phone
	}
	var subs []store.PushSub
	users := map[string]store.User{}
	var keys store.PushKeys
	m.st.Read(func(d *store.Data) {
		keys = d.PushKeys
		for _, s := range d.PushSubs {
			if slices.Contains(s.Kinds, kind) {
				subs = append(subs, s)
			}
		}
		for _, u := range d.Users {
			users[u.Name] = u
		}
	})
	if len(subs) == 0 || keys.Public == "" {
		return
	}
	title := "Bytewise Panel"
	url := "/"
	if serverID != "" {
		if sv, ok := m.st.Server(serverID); ok {
			title = sv.Name
		}
		url = "/#/server/" + serverID
	}
	n := m.problemCount()
	msg := pushMsg{Title: title, Body: text, Tag: serverID + "|" + kind, URL: url, Badge: &n}
	if kind == "players" {
		msg.Tag = serverID + "|players|" + text // every join is its own message
	}
	urgency := "normal"
	if level == "error" || kind == "crash" {
		urgency = "high"
	}
	for _, s := range subs {
		u, ok := users[s.User]
		if !ok || u.Disabled || (serverID != "" && !canSee(u, serverID)) {
			continue
		}
		if !pushLimit.Allow(s.ID+"|"+text, 2*time.Minute) {
			continue
		}
		go m.sendPush(keys, s, msg, urgency)
	}
}

func canSee(u store.User, id string) bool {
	return u.Role == "admin" || len(u.Servers) == 0 || slices.Contains(u.Servers, id)
}

func (m *Manager) sendPush(keys store.PushKeys, s store.PushSub, msg pushMsg, urgency string) error {
	body, _ := json.Marshal(msg)
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	err := push.Send(ctx, push.Keys{Private: keys.Private, Public: keys.Public},
		push.Subscription{Endpoint: s.Endpoint, P256dh: s.P256dh, Auth: s.Auth}, body, urgency, pushSubject)
	_ = m.st.Update(func(d *store.Data) error {
		for i := range d.PushSubs {
			if d.PushSubs[i].ID != s.ID {
				continue
			}
			if errors.Is(err, push.ErrGone) {
				// the browser dropped the subscription (app removed, permission revoked)
				d.PushSubs = append(d.PushSubs[:i], d.PushSubs[i+1:]...)
				return nil
			}
			if err != nil {
				d.PushSubs[i].LastErr = err.Error()
			} else {
				d.PushSubs[i].LastOK, d.PushSubs[i].LastErr = time.Now(), ""
			}
			return nil
		}
		return nil
	})
	return err
}

// TestPush sends a test message to one device and waits for the result.
func (m *Manager) TestPush(s store.PushSub) error {
	keys, err := m.PushKeys()
	if err != nil {
		return err
	}
	n := m.problemCount()
	return m.sendPush(keys, s, pushMsg{Title: "Bytewise Panel", Body: "Test: Push-Nachrichten kommen auf diesem Gerät an.", Tag: "test", URL: "/", Badge: &n}, "normal")
}
