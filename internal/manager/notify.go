package manager

import (
	"context"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/notify"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

var notifyLimit notify.Limiter

func (m *Manager) notifyCfg() notify.Config {
	var c notify.Config
	m.st.Read(func(d *store.Data) { c = d.Settings.Notify })
	return c
}

// notifyEvent sends a server event in the background (same text at most
// every 10 minutes per server).
func (m *Manager) notifyEvent(id, level, kind, text string) {
	c := m.notifyCfg()
	if !c.Enabled() || !c.Wants(kind) || !notifyLimit.Allow(id+"|"+text, 10*time.Minute) {
		return
	}
	title := id
	if sv, ok := m.st.Server(id); ok {
		title = sv.Name
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = notify.Send(ctx, c, notify.Message{Title: title, Text: text, Level: level})
	}()
}

// NotifyPanel sends a message that is not about one server.
func (m *Manager) NotifyPanel(kind, level, title, text string) {
	c := m.notifyCfg()
	if !c.Enabled() || !c.Wants(kind) || !notifyLimit.Allow("panel|"+text, 12*time.Hour) {
		return
	}
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_ = notify.Send(ctx, c, notify.Message{Title: title, Text: text, Level: level})
	}()
}
