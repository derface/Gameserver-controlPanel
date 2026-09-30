// Package store keeps the panel's state in a single JSON file.
// It is deliberately simple: the panel manages a handful of users and
// servers, so a mutex-guarded file with atomic writes is plenty.
package store

import (
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type User struct {
	Name    string    `json:"name"`
	Salt    string    `json:"salt"`
	Hash    string    `json:"hash"`
	Created time.Time `json:"created"`
}

type Session struct {
	User    string    `json:"user"`
	Expires time.Time `json:"expires"`
}

type PortMap struct {
	Name      string `json:"name,omitempty"`
	Host      int    `json:"host"`
	Container int    `json:"container"`
	Proto     string `json:"proto"` // tcp | udp
}

type Server struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Template    string            `json:"template"`
	Vars        map[string]string `json:"vars"`
	Ports       []PortMap         `json:"ports"`
	MemoryMB    int               `json:"memory_mb"`
	CPUs        float64           `json:"cpus"`
	UpdateOnRun bool              `json:"update_on_start"`
	ContainerID string            `json:"container_id,omitempty"`
	Created     time.Time         `json:"created"`
	CreatedBy   string            `json:"created_by,omitempty"` // "manual" | "ai"
	Backup      BackupPlan        `json:"backup"`
}

// BackupPlan is the automatic backup schedule of one server.
type BackupPlan struct {
	EveryHours int       `json:"every_hours"` // 0 = aus, 6, 12 oder 24
	AtHour     int       `json:"at_hour"`     // Uhrzeit bei täglich
	Keep       int       `json:"keep"`        // so viele automatische Backups behalten
	LastAuto   time.Time `json:"last_auto,omitempty"`
	LastError  string    `json:"last_error,omitempty"`
}

type Settings struct {
	AIProvider   string `json:"ai_provider"` // anthropic | openai | ollama | ""
	AnthropicKey string `json:"anthropic_key,omitempty"`
	OpenAIKey    string `json:"openai_key,omitempty"`
	OllamaURL    string `json:"ollama_url,omitempty"`
	AIModel      string `json:"ai_model,omitempty"`
	PublicHost   string `json:"public_host,omitempty"` // shown to players, e.g. play.example.com
}

type Data struct {
	Users    []User             `json:"users"`
	Sessions map[string]Session `json:"sessions"`
	Servers  []Server           `json:"servers"`
	Settings Settings           `json:"settings"`
	License  string             `json:"license,omitempty"`
}

type Store struct {
	mu   sync.RWMutex
	path string
	d    Data
}

func Open(dir string) (*Store, error) {
	if err := os.MkdirAll(dir, 0o750); err != nil {
		return nil, err
	}
	s := &Store{path: filepath.Join(dir, "panel.json")}
	b, err := os.ReadFile(s.path)
	if errors.Is(err, os.ErrNotExist) {
		s.d = Data{Sessions: map[string]Session{}}
		return s, s.save()
	}
	if err != nil {
		return nil, err
	}
	if err := json.Unmarshal(b, &s.d); err != nil {
		return nil, err
	}
	if s.d.Sessions == nil {
		s.d.Sessions = map[string]Session{}
	}
	return s, nil
}

// save must be called with the write lock held (or during Open).
func (s *Store) save() error {
	b, err := json.MarshalIndent(s.d, "", "  ")
	if err != nil {
		return err
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return err
	}
	return os.Rename(tmp, s.path)
}

// Read runs fn with a read lock on a copy-safe view of the data.
func (s *Store) Read(fn func(d *Data)) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	fn(&s.d)
}

// Update runs fn with the write lock and persists the result.
func (s *Store) Update(fn func(d *Data) error) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if err := fn(&s.d); err != nil {
		return err
	}
	return s.save()
}

func (s *Store) Server(id string) (Server, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	for _, sv := range s.d.Servers {
		if sv.ID == id {
			return copyServer(sv), true
		}
	}
	return Server{}, false
}

func (s *Store) Servers() []Server {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := make([]Server, 0, len(s.d.Servers))
	for _, sv := range s.d.Servers {
		out = append(out, copyServer(sv))
	}
	return out
}

func (s *Store) SaveServer(sv Server) error {
	return s.Update(func(d *Data) error {
		for i := range d.Servers {
			if d.Servers[i].ID == sv.ID {
				d.Servers[i] = sv
				return nil
			}
		}
		d.Servers = append(d.Servers, sv)
		return nil
	})
}

func (s *Store) DeleteServer(id string) error {
	return s.Update(func(d *Data) error {
		out := d.Servers[:0]
		for _, sv := range d.Servers {
			if sv.ID != id {
				out = append(out, sv)
			}
		}
		d.Servers = out
		return nil
	})
}

func copyServer(sv Server) Server {
	c := sv
	c.Vars = map[string]string{}
	for k, v := range sv.Vars {
		c.Vars[k] = v
	}
	c.Ports = append([]PortMap(nil), sv.Ports...)
	return c
}
