package manager

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Timed events: settings change for a time span (double XP on Saturday
// evening), the players are warned in the game, and afterwards the old
// values come back. Applying settings restarts a running server.

var eventWarn = []int{10, 1} // minutes before start and end

// PlanEvent checks and stores a new event.
func (m *Manager) PlanEvent(id string, ev store.GameEvent, vars map[string]string, files map[string]map[string]string) (store.GameEvent, []string, error) {
	ev.Name = strings.TrimSpace(ev.Name)
	if ev.Name == "" {
		ev.Name = "Event"
	}
	if len([]rune(ev.Name)) > 60 {
		return ev, nil, errors.New("Der Name ist zu lang")
	}
	if !ev.End.After(ev.Start) {
		return ev, nil, errors.New("Das Ende muss nach dem Beginn liegen")
	}
	if ev.End.Sub(ev.Start) > 14*24*time.Hour {
		return ev, nil, errors.New("Ein Event dauert höchstens 14 Tage")
	}
	if ev.Weekly && ev.End.Sub(ev.Start) > 6*24*time.Hour {
		return ev, nil, errors.New("Ein wöchentliches Event dauert höchstens 6 Tage")
	}
	if !ev.End.After(time.Now()) {
		return ev, nil, errors.New("Das Event wäre schon vorbei")
	}
	changes, notes, err := m.CheckChanges(id, vars, files)
	if err != nil {
		return ev, notes, err
	}
	if len(changes) == 0 {
		return ev, notes, errors.New("Das Event ändert nichts: die Werte stehen schon so")
	}
	ev.Changes = nil
	for _, c := range changes {
		if c.Where == "memory" {
			continue
		}
		ev.Changes = append(ev.Changes, store.EventChange{Where: c.Where, Key: c.Key, Label: c.Label, Value: c.New, Old: c.Old})
	}
	ev.ID, ev.State, ev.Created, ev.Orig, ev.Warned, ev.LastError = newID(), "geplant", time.Now(), nil, 0, ""
	err = m.st.ModifyServer(id, func(sv *store.Server) error {
		if len(sv.GameEvents) >= 20 {
			return errors.New("Höchstens 20 Events pro Server")
		}
		sv.GameEvents = append(sv.GameEvents, ev)
		return nil
	})
	if err == nil {
		m.EventKind(id, "info", "event", fmt.Sprintf("Event „%s“ angekündigt: %s bis %s%s. %s", ev.Name, deTime(ev.Start), deTime(ev.End), weeklyText(ev.Weekly), changeText(ev.Changes)))
	}
	return ev, notes, err
}

// DeleteEvent removes an event; a running one is ended first.
func (m *Manager) DeleteEvent(ctx context.Context, id, evID string) error {
	sv, ok := m.st.Server(id)
	if !ok {
		return errors.New("Server nicht gefunden")
	}
	for _, ev := range sv.GameEvents {
		if ev.ID == evID && ev.State == "aktiv" {
			if err := m.endEvent(ctx, id, ev, false, time.Now()); err != nil {
				return err
			}
		}
	}
	return m.st.ModifyServer(id, func(sv *store.Server) error {
		out := sv.GameEvents[:0]
		for _, ev := range sv.GameEvents {
			if ev.ID != evID {
				out = append(out, ev)
			}
		}
		sv.GameEvents = out
		return nil
	})
}

func (m *Manager) saveEvent(id string, ev store.GameEvent) {
	_ = m.st.ModifyServer(id, func(sv *store.Server) error {
		for i := range sv.GameEvents {
			if sv.GameEvents[i].ID == ev.ID {
				sv.GameEvents[i] = ev
			}
		}
		return nil
	})
}

// changeMaps turns event changes into the ApplySettings format.
func changeMaps(cs []store.EventChange, vals map[string]string) (map[string]string, map[int]map[string]string) {
	vars, files := map[string]string{}, map[int]map[string]string{}
	for _, c := range cs {
		v := c.Value
		if vals != nil {
			var ok bool
			if v, ok = vals[c.Where+"|"+c.Key]; !ok {
				continue
			}
		}
		if c.Where == "vars" {
			vars[c.Key] = v
			continue
		}
		i, err := strconv.Atoi(c.Where)
		if err != nil {
			continue
		}
		if files[i] == nil {
			files[i] = map[string]string{}
		}
		files[i][c.Key] = v
	}
	if len(vars) == 0 {
		vars = nil
	}
	return vars, files
}

// currentValues reads the present value of each key an event touches.
func (m *Manager) currentValues(id string, cs []store.EventChange) (map[string]string, error) {
	v, err := m.Settings(id)
	if err != nil {
		return nil, err
	}
	out := map[string]string{}
	for _, c := range cs {
		if c.Where == "vars" {
			for _, x := range v.Vars {
				if x.Key == c.Key {
					out[c.Where+"|"+c.Key] = x.Value
				}
			}
			continue
		}
		i, err := strconv.Atoi(c.Where)
		if err != nil || i < 0 || i >= len(v.Files) {
			continue
		}
		for _, x := range v.Files[i].Settings {
			if x.Key == c.Key && x.Present { // keys the file did not have stay as the event set them
				out[c.Where+"|"+c.Key] = x.Value
			}
		}
	}
	return out, nil
}

func (m *Manager) startEvent(ctx context.Context, id string, ev store.GameEvent) error {
	orig, err := m.currentValues(id, ev.Changes)
	if err != nil {
		return err
	}
	ev.Orig = orig
	m.saveEvent(id, ev) // remember the old values before anything changes
	vars, files := changeMaps(ev.Changes, nil)
	if _, err := m.ApplySettings(ctx, id, vars, files); err != nil {
		ev.LastError = err.Error()
		m.saveEvent(id, ev)
		m.EventKind(id, "error", "error", fmt.Sprintf("Event „%s“ konnte nicht starten: %v", ev.Name, err))
		return err
	}
	ev.State, ev.Warned, ev.LastError = "aktiv", 0, ""
	m.saveEvent(id, ev)
	m.EventKind(id, "info", "event", fmt.Sprintf("Event „%s“ läuft bis %s.", ev.Name, deTime(ev.End)))
	return nil
}

func (m *Manager) endEvent(ctx context.Context, id string, ev store.GameEvent, reschedule bool, now time.Time) error {
	if ev.State == "aktiv" && len(ev.Orig) > 0 {
		vars, files := changeMaps(ev.Changes, ev.Orig)
		if _, err := m.ApplySettings(ctx, id, vars, files); err != nil {
			ev.LastError = "Zurücksetzen fehlgeschlagen: " + err.Error()
			m.saveEvent(id, ev)
			m.EventKind(id, "error", "error", fmt.Sprintf("Event „%s“: alte Werte konnten nicht zurückgesetzt werden: %v", ev.Name, err))
			return err
		}
		m.EventKind(id, "info", "event", fmt.Sprintf("Event „%s“ ist vorbei, die alten Werte gelten wieder.", ev.Name))
	}
	ev.Orig, ev.Warned, ev.LastError = nil, 0, ""
	if reschedule && ev.Weekly {
		for !ev.End.After(now) {
			ev.Start, ev.End = ev.Start.AddDate(0, 0, 7), ev.End.AddDate(0, 0, 7)
		}
		ev.State = "geplant"
	} else {
		ev.State = "beendet"
	}
	m.saveEvent(id, ev)
	return nil
}

// checkEvents runs every 20 seconds with the task scheduler.
func (m *Manager) checkEvents(ctx context.Context, now time.Time) {
	for _, sv := range m.st.Servers() {
		for _, ev := range sv.GameEvents {
			switch ev.State {
			case "geplant":
				if !now.Before(ev.End) { // missed completely (panel was off)
					_ = m.endEvent(ctx, sv.ID, ev, true, now)
					continue
				}
				if !now.Before(ev.Start) {
					if m.eventBusy(sv.ID + ev.ID) {
						continue
					}
					go func(id string, ev store.GameEvent) {
						defer m.eventDone(id + ev.ID)
						actx, cancel := context.WithTimeout(ctx, 15*time.Minute)
						defer cancel()
						_ = m.startEvent(actx, id, ev)
					}(sv.ID, ev)
					continue
				}
				m.warnEvent(sv.ID, ev, ev.Start, now, "startet", fmt.Sprintf("Event „%s“ startet in %%d %%s. Der Server startet dafür kurz neu.", ev.Name))
			case "aktiv":
				if !now.Before(ev.End) {
					if m.eventBusy(sv.ID + ev.ID) {
						continue
					}
					go func(id string, ev store.GameEvent) {
						defer m.eventDone(id + ev.ID)
						actx, cancel := context.WithTimeout(ctx, 15*time.Minute)
						defer cancel()
						_ = m.endEvent(actx, id, ev, true, now)
					}(sv.ID, ev)
					continue
				}
				m.warnEvent(sv.ID, ev, ev.End, now, "endet", fmt.Sprintf("Event „%s“ endet in %%d %%s. Der Server startet dafür kurz neu.", ev.Name))
			}
		}
	}
}

func (m *Manager) warnEvent(id string, ev store.GameEvent, at, now time.Time, _ string, format string) {
	left := at.Sub(now)
	for _, w := range eventWarn {
		if left <= time.Duration(w)*time.Minute && left > time.Duration(w-1)*time.Minute && ev.Warned != w {
			unit := "Minuten"
			if w == 1 {
				unit = "Minute"
			}
			m.Broadcast(id, fmt.Sprintf(format, w, unit))
			ev.Warned = w
			m.saveEvent(id, ev)
			return
		}
	}
}

func (m *Manager) eventBusy(key string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	if m.pendingTasks == nil {
		m.pendingTasks = map[string]bool{}
	}
	if m.pendingTasks["ev:"+key] {
		return true
	}
	m.pendingTasks["ev:"+key] = true
	return false
}

func (m *Manager) eventDone(key string) {
	m.mu.Lock()
	delete(m.pendingTasks, "ev:"+key)
	m.mu.Unlock()
}

var wdShort = [...]string{"So", "Mo", "Di", "Mi", "Do", "Fr", "Sa"}

func deTime(t time.Time) string {
	t = t.Local()
	return wdShort[t.Weekday()] + " " + t.Format("02.01. 15:04")
}

func weeklyText(w bool) string {
	if w {
		return ", jede Woche"
	}
	return ""
}

// changeText lists the event values for the announcement, at most five.
func changeText(cs []store.EventChange) string {
	var parts []string
	for i, c := range cs {
		if i == 5 {
			parts = append(parts, fmt.Sprintf("und %d weitere", len(cs)-5))
			break
		}
		l := c.Label
		if l == "" {
			l = c.Key
		}
		parts = append(parts, l+": "+c.Value)
	}
	return strings.Join(parts, ", ")
}
