package manager

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// SetMaintenance starts (or changes) maintenance of a server.
func (m *Manager) SetMaintenance(id string, until time.Time, note string, closePorts bool, by string) error {
	note = strings.TrimSpace(note)
	if len(note) > 200 {
		return errors.New("Hinweis zu lang (höchstens 200 Zeichen)")
	}
	if !until.IsZero() && until.Before(time.Now().Add(time.Minute)) {
		return errors.New("Das Ende muss in der Zukunft liegen")
	}
	if !until.IsZero() && until.After(time.Now().Add(14*24*time.Hour)) {
		return errors.New("Höchstens 14 Tage")
	}
	var was bool
	err := m.st.ModifyServer(id, func(sv *store.Server) error {
		was = sv.Maintenance != nil
		since := time.Now()
		if was {
			since = sv.Maintenance.Since
		}
		sv.Maintenance = &store.Maintenance{Since: since, Until: until, Note: note, ClosePorts: closePorts, By: by}
		return nil
	})
	if err != nil {
		return err
	}
	m.KickRouter()
	if !was {
		txt := "Wartung beginnt"
		if !until.IsZero() {
			txt += " (bis " + deTime(until) + ")"
		}
		if note != "" {
			txt += ": " + note
		}
		m.Broadcast(id, txt)
		m.EventKind(id, "info", "event", txt+".")
	}
	return nil
}

// EndMaintenance ends it now.
func (m *Manager) EndMaintenance(id, why string) error {
	var was bool
	err := m.st.ModifyServer(id, func(sv *store.Server) error {
		was = sv.Maintenance != nil
		sv.Maintenance = nil
		return nil
	})
	if err != nil || !was {
		return err
	}
	m.KickRouter()
	m.EventKind(id, "info", "event", "Wartung beendet"+why+".")
	return nil
}

// checkMaintenance ends maintenance whose time is up.
func (m *Manager) checkMaintenance(now time.Time) {
	for _, sv := range m.st.Servers() {
		if sv.Maintenance != nil && !sv.InMaintenance(now) {
			_ = m.EndMaintenance(sv.ID, fmt.Sprintf(" (geplant bis %s)", sv.Maintenance.Until.Local().Format("15:04")))
		}
	}
}
