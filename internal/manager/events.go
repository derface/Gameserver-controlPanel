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
	m.ev.mu.Lock()
	defer m.ev.mu.Unlock()
	evs := append(m.loadEvents(id), Event{Time: time.Now(), Level: level, Text: text})
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
