package manager

import (
	"encoding/json"
	"os"
	"path/filepath"
	"sync"
	"time"
)

// Event is one line in a server's history: started, crashed, task ran …
type Event struct {
	Time  time.Time `json:"time"`
	Level string    `json:"level"` // info | warn | error
	Text  string    `json:"text"`
	Kind  string    `json:"kind,omitempty"` // crash, update, … (see notify kinds)
}

const maxEvents = 200

type eventLog struct {
	mu    sync.Mutex
	cache map[string][]Event
}

func (m *Manager) eventFile(id string) string {
	return filepath.Join(m.dataDir, "events", id+".json")
}

func (m *Manager) loadEvents(id string) []Event {
	if m.ev.cache == nil {
		m.ev.cache = map[string][]Event{}
	}
	if evs, ok := m.ev.cache[id]; ok {
		return evs
	}
	var evs []Event
	if b, err := os.ReadFile(m.eventFile(id)); err == nil {
		_ = json.Unmarshal(b, &evs)
	}
	m.ev.cache[id] = evs
	return evs
}

// Event appends to the server's history and keeps the newest entries.
func (m *Manager) Event(id, level, text string) {
	kind := ""
	if level == "error" {
		kind = "error"
	}
	m.EventKind(id, level, kind, text)
}

// EventKind records an event and sends it as a notification when the user
// chose that kind (crash, error, update, task, backup, startstop, ai).
func (m *Manager) EventKind(id, level, kind, text string) {
	if kind != "" {
		m.notifyEvent(id, level, kind, text)
		m.webhook(kind, "server."+kind, id, level, text, nil)
	}
	m.ev.mu.Lock()
	defer m.ev.mu.Unlock()
	evs := append(m.loadEvents(id), Event{Time: time.Now(), Level: level, Text: text, Kind: kind})
	if len(evs) > maxEvents {
		evs = evs[len(evs)-maxEvents:]
	}
	m.ev.cache[id] = evs
	_ = os.MkdirAll(filepath.Dir(m.eventFile(id)), 0o700)
	if b, err := json.Marshal(evs); err == nil {
		tmp := m.eventFile(id) + ".tmp"
		if os.WriteFile(tmp, b, 0o600) == nil {
			_ = os.Rename(tmp, m.eventFile(id))
		}
	}
}

// Events returns the history, newest first.
func (m *Manager) Events(id string) []Event {
	m.ev.mu.Lock()
	defer m.ev.mu.Unlock()
	evs := m.loadEvents(id)
	out := make([]Event, len(evs))
	for i, e := range evs {
		out[len(evs)-1-i] = e
	}
	return out
}

func (m *Manager) dropEvents(id string) {
	m.ev.mu.Lock()
	defer m.ev.mu.Unlock()
	if m.ev.cache != nil {
		delete(m.ev.cache, id)
	}
	_ = os.Remove(m.eventFile(id))
}

// EventsSince returns the events of a server since t, oldest first.
func (m *Manager) EventsSince(id string, t time.Time) []Event {
	m.ev.mu.Lock()
	defer m.ev.mu.Unlock()
	var out []Event
	for _, e := range m.loadEvents(id) {
		if !e.Time.Before(t) {
			out = append(out, e)
		}
	}
	return out
}
