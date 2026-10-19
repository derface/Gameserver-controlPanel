// Package store keeps the panel's state in a single JSON file.
// It is deliberately simple: the panel manages a handful of users and
// servers, so a mutex-guarded file with atomic writes is plenty.
package store

import (
	"encoding/json"
	"errors"
	"github.com/derface/Gameserver-controlPanel/internal/discord"
	"github.com/derface/Gameserver-controlPanel/internal/notify"
	"os"
	"path/filepath"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/remote"
)

type User struct {
	Name      string    `json:"name"`
	Salt      string    `json:"salt"`
	Hash      string    `json:"hash"`
	Created   time.Time `json:"created"`
	Role      string    `json:"role"`              // admin | operator | viewer
	Servers   []string  `json:"servers,omitempty"` // only these servers (empty = all); not for admins
	Disabled  bool      `json:"disabled,omitempty"`
	TOTP      string    `json:"totp,omitempty"`      // base32 secret of the authenticator app, empty = no 2FA
	TOTPLast  int64     `json:"totp_last,omitempty"` // last accepted time step, so a code works only once
	Recovery  []string  `json:"recovery,omitempty"`  // sha256 of the unused recovery codes
	LastLogin time.Time `json:"last_login,omitempty"`
	ViaNode   bool      `json:"-"` // request from another panel with a node key
	ViaKey    bool      `json:"-"` // request from a script with an API key
}

// Session is stored under the sha256 of its token, so a leaked panel.json
// does not hand out logged-in sessions.
type Session struct {
	User    string    `json:"user"`
	Expires time.Time `json:"expires"`
	Created time.Time `json:"created,omitempty"`
	IP      string    `json:"ip,omitempty"`
	Agent   string    `json:"agent,omitempty"`
}

// TLSConfig is the built-in HTTPS of the panel.
type TLSConfig struct {
	Mode   string `json:"mode"`             // "" (nur HTTP) | self | acme | custom
	Domain string `json:"domain,omitempty"` // for Let's Encrypt
	Email  string `json:"email,omitempty"`
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
	Tasks       []Task            `json:"tasks,omitempty"`
	GameEvents  []GameEvent       `json:"game_events,omitempty"`
	Watch       Watch             `json:"watch"`
	NoRouter    bool              `json:"no_router,omitempty"` // do not open its ports in the router
	Maintenance *Maintenance      `json:"maintenance,omitempty"`
	Invite      *Invite           `json:"invite,omitempty"`
	Seasons     *SeasonState      `json:"seasons,omitempty"`
	Macros      []Macro           `json:"macros,omitempty"`
}

// Macro is a saved sequence of console commands. A line "warte 10" (or
// "wait 10") pauses ten seconds; lines starting with # are comments.
type Macro struct {
	ID    string   `json:"id"`
	Name  string   `json:"name"`
	Lines []string `json:"lines"`
}

// Wish is something a player asked for on the status page.
type Wish struct {
	ID      string    `json:"id"`
	Title   string    `json:"title"`
	Kind    string    `json:"kind"` // spiel | mod | sonstiges
	By      string    `json:"by,omitempty"`
	Created time.Time `json:"created"`
	Votes   int       `json:"votes"`
	Voters  []string  `json:"voters,omitempty"` // hashes, so a visitor votes once
	Status  string    `json:"status"`           // offen | geplant | umgesetzt | abgelehnt
	Answer  string    `json:"answer,omitempty"` // reply of the admin
	IP      string    `json:"ip,omitempty"`     // hash of the sender, for limits
}

// Task is a scheduled action of one server.
type Task struct {
	ID         string    `json:"id"`
	Action     string    `json:"action"`            // restart | update | command | backup | start | stop
	Command    string    `json:"command,omitempty"` // for "command"
	At         string    `json:"at,omitempty"`      // "HH:MM" for daily/weekly tasks
	Days       []int     `json:"days,omitempty"`    // 0 = Sonntag … 6; leer = jeden Tag
	EveryHours int       `json:"every_hours,omitempty"`
	WarnMin    int       `json:"warn_min,omitempty"` // warn players this many minutes before
	Enabled    bool      `json:"enabled"`
	LastRun    time.Time `json:"last_run,omitempty"`
	LastResult string    `json:"last_result,omitempty"`
	Created    time.Time `json:"created"`
	// conditions (restart, update, stop)
	WhenEmpty    bool `json:"when_empty,omitempty"`     // wait until no player is online …
	MaxWaitMin   int  `json:"max_wait_min,omitempty"`   // … at most this long, then run anyway
	OnlyIfUpdate bool `json:"only_if_update,omitempty"` // skip when Steam has no newer build
	BackupFirst  bool `json:"backup_first,omitempty"`   // make a backup before the action
	// Quiet runs the task in the hour with the fewest players (from the
	// player statistics); At is used until enough data exists.
	Quiet bool `json:"quiet,omitempty"`
}

// Watch configures the crash and hang watchdog.
type Watch struct {
	NoAutoRestart bool `json:"no_auto_restart"` // default: Docker restarts a crashed server
	HangRestart   bool `json:"hang_restart"`    // restart when the player query stops answering
	HangMinutes   int  `json:"hang_minutes"`
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
	AIProvider   string        `json:"ai_provider"` // anthropic | openai | ollama | ""
	AnthropicKey string        `json:"anthropic_key,omitempty"`
	OpenAIKey    string        `json:"openai_key,omitempty"`
	OllamaURL    string        `json:"ollama_url,omitempty"`
	AIModel      string        `json:"ai_model,omitempty"`
	PublicHost   string        `json:"public_host,omitempty"` // shown to players, e.g. play.example.com
	WizardDone   bool          `json:"wizard_done,omitempty"` // setup wizard finished or skipped
	SteamUser    string        `json:"steam_user,omitempty"`  // logged-in Steam account (the login itself is cached by SteamCMD)
	Remote       remote.Config `json:"remote_backup"`
	TLS          TLSConfig     `json:"tls"`
	UPnP         bool          `json:"upnp"` // open game ports in the router automatically
	// AIAutoDiagnose lets the AI look at a server by itself after a crash,
	// a hang or sustained full RAM/CPU.
	AIAutoDiagnose bool `json:"ai_auto_diagnose"`
	// SteamAPIKey (optional) makes the Workshop search use the Steam Web API.
	SteamAPIKey string `json:"steam_api_key,omitempty"`
	// TrustedProxies are reverse proxies (IPs or networks) whose
	// X-Forwarded-For/-Proto the panel believes.
	TrustedProxies []string `json:"trusted_proxies,omitempty"`
	// Notify sends events to Discord and/or ntfy.
	Notify notify.Config `json:"notify"`
	// Status is the public status page for friends.
	Status StatusConfig `json:"status"`
	// Discord is the bot: slash commands and a self-updating status message.
	Discord discord.Config `json:"discord"`
	// DiscordLinks connect Discord users to player names (whitelist).
	DiscordLinks []DiscordLink `json:"discord_links,omitempty"`
	// Geo records where players connect from (place only, no addresses).
	Geo bool `json:"geo,omitempty"`
}

// Maintenance marks a server as being worked on: tasks and the crash
// watchdog pause, the status page and Discord say so.
type Maintenance struct {
	Since      time.Time `json:"since"`
	Until      time.Time `json:"until,omitempty"` // zero: until ended by hand
	Note       string    `json:"note,omitempty"`
	ClosePorts bool      `json:"close_ports,omitempty"` // close router ports: only the home network can join
	By         string    `json:"by,omitempty"`
}

// DiscordLink is a Discord user who put their player name on the
// whitelist with /whitelist.
type DiscordLink struct {
	User    string            `json:"user"`   // Discord user ID
	Name    string            `json:"name"`   // Discord user name (display)
	Player  string            `json:"player"` // "" = removed, entries are being cleaned up
	Active  bool              `json:"active"` // has the role
	Since   time.Time         `json:"since"`
	Checked time.Time         `json:"checked"` // last role check
	Applied map[string]string `json:"applied"` // server -> name on its whitelist
	Note    string            `json:"note,omitempty"`
}

// SeasonState keeps the running season and the archive of old ones.
type SeasonState struct {
	Current Season      `json:"current"`
	Past    []Season    `json:"past,omitempty"` // newest first
	Wipe    []string    `json:"wipe,omitempty"` // folders reset last time
	Plan    *SeasonPlan `json:"plan,omitempty"`
}

type Season struct {
	N      int              `json:"n"`
	Name   string           `json:"name"`
	Start  time.Time        `json:"start"`
	End    time.Time        `json:"end,omitempty"`
	Backup string           `json:"backup,omitempty"` // archive of the world
	Top    []SeasonPlayer   `json:"top,omitempty"`
	Hours  float64          `json:"hours,omitempty"`
	Base   map[string]int64 `json:"base,omitempty"` // play seconds per player at the start
}

type SeasonPlayer struct {
	Name    string `json:"name"`
	Seconds int64  `json:"seconds"`
}

type SeasonPlan struct {
	At     time.Time `json:"at"`
	Name   string    `json:"name"`
	Wipe   []string  `json:"wipe"`
	Warned int       `json:"warned,omitempty"`
}

// Invite is a public link for friends: address, how to join, a note.
type Invite struct {
	Token   string    `json:"token"`
	Note    string    `json:"note,omitempty"`
	Created time.Time `json:"created"`
}

// InMaintenance reports whether the server is in maintenance at t.
func (s Server) InMaintenance(t time.Time) bool {
	return s.Maintenance != nil && (s.Maintenance.Until.IsZero() || t.Before(s.Maintenance.Until))
}

// StatusConfig configures the public status page (no login).
type StatusConfig struct {
	Enabled   bool   `json:"enabled"`
	Host      string `json:"host,omitempty"` // own host name, e.g. status.example.com
	Title     string `json:"title,omitempty"`
	Note      string `json:"note,omitempty"`
	ShowNames bool   `json:"show_names"` // show who is online
	// CalToken is the key in the calendar feed URL (empty: no feed).
	CalToken string `json:"cal_token,omitempty"`
	// ShowPlayers shows a player list with play times on the page.
	ShowPlayers bool `json:"show_players,omitempty"`
	// ModDownload lets friends download the client mods as a zip.
	ModDownload bool `json:"mod_download,omitempty"`
	// HideUptime hides the 30-day online history on the page.
	HideUptime bool `json:"hide_uptime,omitempty"`
	// Wishes shows the wish list where players suggest games and mods.
	Wishes bool `json:"wishes,omitempty"`
	// HideGameNames hides the server name the game itself reports.
	HideGameNames bool                    `json:"hide_game_names,omitempty"`
	Servers       map[string]StatusServer `json:"servers,omitempty"`
}

type StatusServer struct {
	Show bool   `json:"show"`
	Note string `json:"note,omitempty"` // e.g. "Passwort steht im Discord"
}

// RouterMap is a port forwarding the panel created in the router.
type RouterMap struct {
	Server string `json:"server"`
	Port   int    `json:"port"`
	Proto  string `json:"proto"`
}

type Data struct {
	Users    []User             `json:"users"`
	Sessions map[string]Session `json:"sessions"`
	Servers  []Server           `json:"servers"`
	Settings Settings           `json:"settings"`
	Router   []RouterMap        `json:"router_maps,omitempty"`
	Nodes    []Node             `json:"nodes,omitempty"`     // other machines this panel controls
	NodeKeys []NodeKey          `json:"node_keys,omitempty"` // panels allowed to control this machine
	Wishes   []Wish             `json:"wishes,omitempty"`    // wish list of the status page
	WishSalt string             `json:"wish_salt,omitempty"`
	APIKeys  []APIKey           `json:"api_keys,omitempty"` // keys for scripts, Home Assistant, Stream Deck …
	Webhooks []Webhook          `json:"webhooks,omitempty"` // URLs called on events
}

// APIKey lets a script use the panel's API with the rights of a role.
// Only the sha256 of the key is stored.
type APIKey struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	Hash     string    `json:"hash"`
	Hint     string    `json:"hint"`              // first characters, to tell keys apart
	Role     string    `json:"role"`              // operator | viewer
	Servers  []string  `json:"servers,omitempty"` // only these servers (empty = all)
	Created  time.Time `json:"created"`
	By       string    `json:"by,omitempty"`
	LastUsed time.Time `json:"last_used,omitempty"`
	LastFrom string    `json:"last_from,omitempty"`
}

// Webhook is a URL the panel calls (HTTP POST with JSON) on events.
type Webhook struct {
	ID       string    `json:"id"`
	Name     string    `json:"name"`
	URL      string    `json:"url"`
	Secret   string    `json:"secret"`            // HMAC-SHA256 key for the signature header
	Events   []string  `json:"events"`            // kinds, see manager.WebhookEvents
	Servers  []string  `json:"servers,omitempty"` // only these servers (empty = all)
	Enabled  bool      `json:"enabled"`
	Created  time.Time `json:"created"`
	LastAt   time.Time `json:"last_at,omitempty"`
	LastCode int       `json:"last_code,omitempty"`
	LastErr  string    `json:"last_err,omitempty"`
	Fails    int       `json:"fails,omitempty"` // failed calls in a row
}

// Node is another machine running Bytewise Panel that this panel controls.
// Its servers appear here with the id "<node id>~<server id>".
type Node struct {
	ID    string    `json:"id"`
	Name  string    `json:"name"`
	URL   string    `json:"url"`   // e.g. https://192.168.178.60:8420
	Token string    `json:"token"` // the node's key, sent as Bearer token
	FP    string    `json:"fp"`    // sha256 of the node's TLS certificate, pinned on first contact ("" for http)
	Added time.Time `json:"added"`
}

// NodeKey lets another panel control this machine. Only the sha256 of the
// key is stored.
type NodeKey struct {
	ID       string    `json:"id"`
	Label    string    `json:"label"`
	Hash     string    `json:"hash"`
	Created  time.Time `json:"created"`
	LastUsed time.Time `json:"last_used,omitempty"`
	LastFrom string    `json:"last_from,omitempty"`
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
	for i := range s.d.Users {
		if s.d.Users[i].Role == "" { // panels before 0.12 only had admins
			s.d.Users[i].Role = "admin"
		}
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

// ModifyServer changes one server atomically, so concurrent writers
// (scheduler, API) do not overwrite each other's fields.
func (s *Store) ModifyServer(id string, fn func(sv *Server) error) error {
	return s.Update(func(d *Data) error {
		for i := range d.Servers {
			if d.Servers[i].ID == id {
				return fn(&d.Servers[i])
			}
		}
		return errNotFound
	})
}

var errNotFound = errors.New("Server nicht gefunden")

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
	c.Tasks = make([]Task, len(sv.Tasks))
	for i, t := range sv.Tasks {
		t.Days = append([]int(nil), t.Days...)
		c.Tasks[i] = t
	}
	return c
}

// GameEvent changes settings for a time span and sets them back afterwards
// (e.g. "Saturday 20–23: double XP").
type GameEvent struct {
	ID        string            `json:"id"`
	Name      string            `json:"name"`
	Start     time.Time         `json:"start"`
	End       time.Time         `json:"end"`
	Weekly    bool              `json:"weekly,omitempty"`
	Changes   []EventChange     `json:"changes"`
	Orig      map[string]string `json:"orig,omitempty"`   // "where|key" -> value before the event
	State     string            `json:"state"`            // geplant | aktiv | beendet
	Warned    int               `json:"warned,omitempty"` // minutes of the last in-game warning
	Created   time.Time         `json:"created"`
	LastError string            `json:"last_error,omitempty"`
}

type EventChange struct {
	Where string `json:"where"` // "vars" or file index
	Key   string `json:"key"`
	Label string `json:"label"`
	Value string `json:"value"`
	Old   string `json:"old,omitempty"` // value when the event was planned (display only)
}
