// Package templates loads the game templates that describe how to install
// and run each game. Adding a game means adding one JSON file.
package templates

import (
	"bytes"
	"crypto/rand"
	"embed"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
)

//go:embed games/*.json games/community/*.json games/amp/*.json
var builtin embed.FS

type Port struct {
	Name  string `json:"name"`
	Port  int    `json:"port"`
	Proto string `json:"proto"`
}

// AMPImport describes how data from a CubeCoders AMP instance lands in a
// server of this template.
type AMPImport struct {
	// Map moves paths below AMP's base directory, e.g. {"Zomboid/":
	// "home/Zomboid/"}. Everything else goes to "server/".
	Map map[string]string `json:"map,omitempty"`
	// Vars are fixed values for an imported server (e.g. resume the save).
	Vars map[string]string `json:"vars,omitempty"`
	// VarsFromFiles takes a value from the imported file names: the first
	// group of the expression, matched against paths below the base directory.
	VarsFromFiles map[string]string `json:"vars_from_files,omitempty"`
}

type Var struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Default string   `json:"default"`
	Type    string   `json:"type,omitempty"` // text | number | secret | select | bool | slug
	Options []string `json:"options,omitempty"`
	Labels  []string `json:"labels,omitempty"` // display names for Options
	Flag    string   `json:"flag,omitempty"`   // for bool: text inserted when true
	Help    string   `json:"help,omitempty"`
	Needed  bool     `json:"required,omitempty"`
	Group   string   `json:"group,omitempty"` // tab on the settings page, default "Start"
}

// DefaultContent returns the starting content of the file, or nil.
func (c ConfigFile) DefaultContent() []byte {
	if len(c.Default) == 0 {
		return nil
	}
	var out []byte
	var str string
	if json.Unmarshal(c.Default, &str) == nil {
		out = []byte(str)
	} else {
		var buf bytes.Buffer
		if json.Indent(&buf, c.Default, "", "    ") != nil {
			return nil
		}
		out = append(buf.Bytes(), '\n')
	}
	// every {{random}} becomes its own random word, e.g. for passwords that
	// a game requires to be set and different from each other
	for bytes.Contains(out, []byte("{{random}}")) {
		out = bytes.Replace(out, []byte("{{random}}"), []byte(randomWord()), 1)
	}
	return out
}

func randomWord() string {
	const abc = "abcdefghjkmnpqrstuvwxyzABCDEFGHJKLMNPQRSTUVWXYZ23456789"
	b := make([]byte, 12)
	_, _ = rand.Read(b)
	for i := range b {
		b[i] = abc[int(b[i])%len(abc)]
	}
	return string(b)
}

// ConfigFile is one game configuration file with the settings the panel
// shows as a form (see package gameconfig for the formats).
type ConfigFile struct {
	Title    string `json:"title"`
	File     string `json:"file"`                // below the server folder, may contain {{var}}
	Format   string `json:"format"`              // properties | ini | json | cvar | xmlprop | palworld
	InitFrom string `json:"init_from,omitempty"` // copy this file first if File is missing or empty
	// Default is the content to start from while the game has not written the
	// file yet: a JSON object for json files, otherwise a string.
	Default json.RawMessage `json:"default,omitempty"`
	// Check names an extra rule the panel enforces before saving
	// (enshrouded_groups: group passwords set and different).
	Check string `json:"check,omitempty"`
	Note  string `json:"note,omitempty"`
	// WriteDefault writes Default (with {{placeholders}} filled in) before
	// the server starts, if the file does not exist yet. For games that need
	// a config file to start at all.
	WriteDefault bool `json:"write_default,omitempty"`
	// Auto adds every other value found in the file as a card (default on;
	// "auto": false turns it off). Hide lists keys never shown ("*" matches
	// any part, e.g. "userGroups.*.name"); AutoGroup is the tab for values
	// without a better one.
	Auto      *bool    `json:"auto,omitempty"`
	Hide      []string `json:"hide,omitempty"`
	AutoGroup string   `json:"auto_group,omitempty"`
	// NoteGroup shows the note only on that tab of the settings page.
	NoteGroup string    `json:"note_group,omitempty"`
	Settings  []Setting `json:"settings"`
}

type Setting struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Type    string   `json:"type,omitempty"` // text | number | bool | select | secret | list
	Options []string `json:"options,omitempty"`
	Labels  []string `json:"labels,omitempty"` // display names for options
	True    string   `json:"true,omitempty"`   // how the file writes yes/no (default true/false)
	False   string   `json:"false,omitempty"`
	Min     *float64 `json:"min,omitempty"`
	Max     *float64 `json:"max,omitempty"`
	Help    string   `json:"help,omitempty"`
	// Group is the tab the setting is shown in (e.g. "Server", "Gameplay").
	Group string `json:"group,omitempty"`
	// Unit is shown inside the field, e.g. "Spieler" or "Minuten".
	Unit string `json:"unit,omitempty"`
	// Scale converts the shown value to the file: file = shown × Scale
	// (Enshrouded stores minutes as nanoseconds).
	Scale float64 `json:"scale,omitempty"`
	// When is "key=value" of another setting in the same file; the setting
	// only takes effect then (e.g. "gameSettingsPreset=Custom").
	When string `json:"when,omitempty"`
}

// BoolValues returns how the file spells true and false.
func (s Setting) BoolValues() (string, string) {
	t, f := s.True, s.False
	if t == "" {
		t = "true"
	}
	if f == "" {
		f = "false"
	}
	return t, f
}

var configFormats = map[string]bool{"properties": true, "ini": true, "json": true, "cvar": true, "xmlprop": true, "palworld": true, "armacfg": true}

func (t Template) checkConfig() error {
	for i, c := range t.Config {
		if !configFormats[c.Format] {
			return fmt.Errorf("config %d: unbekanntes Format %q", i, c.Format)
		}
		if c.File == "" || strings.Contains(c.File, "..") || strings.HasPrefix(c.File, "/") {
			return fmt.Errorf("config %d: ungültiger Dateipfad", i)
		}
		seen := map[string]bool{}
		for _, s := range c.Settings {
			if s.Key == "" || s.Label == "" || seen[s.Key] {
				return fmt.Errorf("config %d: Einstellung ohne Schlüssel/Name oder doppelt (%s)", i, s.Key)
			}
			seen[s.Key] = true
			if s.Scale < 0 || (s.Scale > 0 && s.Type != "number") {
				return fmt.Errorf("config %d: %s: scale nur für Zahlen", i, s.Key)
			}
			if s.When != "" && !strings.Contains(s.When, "=") {
				return fmt.Errorf("config %d: %s: when muss „schlüssel=wert“ sein", i, s.Key)
			}
			switch s.Type {
			case "", "text", "number", "bool", "secret":
			case "list":
				if c.Format != "json" {
					return fmt.Errorf("config %d: %s: Listen gibt es nur in JSON", i, s.Key)
				}
			case "select":
				if len(s.Options) == 0 || (len(s.Labels) > 0 && len(s.Labels) != len(s.Options)) {
					return fmt.Errorf("config %d: %s braucht passende options/labels", i, s.Key)
				}
			default:
				return fmt.Errorf("config %d: %s hat unbekannten Typ %q", i, s.Key, s.Type)
			}
		}
	}
	return nil
}

// ModSource connects a template to a mod catalogue. For "modrinth" the list
// of project slugs is kept in the variable Var; the server's loader and game
// version come from TypeVar and VersionVar.
type ModSource struct {
	Source     string `json:"source"` // modrinth
	Var        string `json:"var"`
	TypeVar    string `json:"type_var"`
	VersionVar string `json:"version_var"`
}

type Template struct {
	ID          string            `json:"id"`
	Name        string            `json:"name"`
	Category    string            `json:"category"`
	Kind        string            `json:"kind"` // steam | image
	SteamAppID  int               `json:"steam_app_id,omitempty"`
	SteamBeta   string            `json:"steam_beta,omitempty"`
	SteamMod    string            `json:"steam_mod,omitempty"`         // GoldSrc: +app_set_config 90 mod <name>
	SteamLogin  bool              `json:"steam_login,omitempty"`       // SteamCMD needs a Steam account (not anonymous)
	SteamPrePl  string            `json:"steam_preplatform,omitempty"` // first install once with this platform (L4D2)
	SteamBase   int               `json:"steam_base_app,omitempty"`    // extra app installed first (TF2 for TF2 Classified)
	SteamBaseIn string            `json:"steam_base_dir,omitempty"`    // its folder below the server folder
	Image       string            `json:"image,omitempty"`
	Env         map[string]string `json:"env,omitempty"`
	Start       string            `json:"start,omitempty"`
	Ports       []Port            `json:"ports"`
	FixedPorts  bool              `json:"fixed_ports,omitempty"`
	Vars        []Var             `json:"vars"`
	AMPImport   *AMPImport        `json:"amp_import,omitempty"`
	MemoryMB    int               `json:"memory_mb"`
	MinMemoryMB int               `json:"min_memory_mb"`
	JavaMemEnv  string            `json:"java_memory_env,omitempty"`
	Console     string            `json:"console"` // stdin | exec
	ExecPrefix  []string          `json:"exec_prefix,omitempty"`
	Broadcast   string            `json:"broadcast,omitempty"` // console command that messages all players, {{msg}}
	StopSignal  string            `json:"stop_signal,omitempty"`
	StopTimeout int               `json:"stop_timeout,omitempty"`
	Ready       string            `json:"ready,omitempty"` // regex in logs meaning "server is up"
	Status      string            `json:"status"`          // stabil | beta | experimentell | defekt (hidden)
	DiskGB      int               `json:"disk_gb,omitempty"`
	Notes       string            `json:"notes,omitempty"`
	Custom      bool              `json:"custom,omitempty"`    // user supplies app id, start and ports
	Query       *Query            `json:"query,omitempty"`     // how to read the player count
	Packages    []string          `json:"packages,omitempty"`  // extra Debian packages (steam)
	Runtime     string            `json:"runtime,omitempty"`   // "" or "wine"
	Platform    string            `json:"platform,omitempty"`  // "windows" downloads Windows binaries
	DataPath    string            `json:"data_path,omitempty"` // mount point for image servers (default /data)
	DataUID     int               `json:"data_uid,omitempty"`  // owner of the server folder (default 1000)
	Cmd         []string          `json:"cmd,omitempty"`       // arguments for image servers
	LogFile     string            `json:"log_file,omitempty"`  // steam: game writes here instead of stdout

	// Settings shown as a form; values live in the game's config files.
	Config []ConfigFile `json:"config,omitempty"`
	// Mod manager: where the list of mods lives.
	Mods *ModSource `json:"mods,omitempty"`

	// Backups: by default a Steam server backs up everything in its data
	// folder except the game install ("server"); image servers back up all.
	BackupExclude []string `json:"backup_exclude,omitempty"` // top-level entries to skip
	BackupInclude []string `json:"backup_include,omitempty"` // extra paths, also inside excluded ones
	BackupPre     []string `json:"backup_pre,omitempty"`     // console commands before a live backup
	BackupPost    []string `json:"backup_post,omitempty"`    // console commands afterwards
}

// DataPathOr returns where the server folder is mounted in the container.
func (t Template) DataPathOr() string {
	if t.DataPath != "" && t.Kind == "image" {
		return t.DataPath
	}
	return "/data"
}

// Excludes returns the effective top-level excludes for backups.
func (t Template) Excludes() []string {
	if t.BackupExclude != nil {
		return t.BackupExclude
	}
	if t.Kind == "steam" {
		return []string{"server"}
	}
	return nil
}

// Query describes how to ask a running server for its player count.
// Type is "a2s" (Steam), "minecraft" (Java) or "bedrock"; Port is the index into Ports.
type Query struct {
	Type string `json:"type"`
	Port int    `json:"port"`
}

type Set struct {
	list []Template
	byID map[string]Template
}

// Load reads the built-in templates plus any *.json in extraDir (may be empty).
func Load(extraDir string) (*Set, error) {
	s := &Set{byID: map[string]Template{}}
	for _, dir := range []string{"games", "games/community", "games/amp"} {
		entries, err := builtin.ReadDir(dir)
		if err != nil {
			return nil, err
		}
		for _, e := range entries {
			if e.IsDir() {
				continue
			}
			b, err := builtin.ReadFile(dir + "/" + e.Name())
			if err != nil {
				return nil, err
			}
			if err := s.add(b, e.Name()); err != nil {
				return nil, err
			}
		}
	}
	if extraDir != "" {
		files, _ := filepath.Glob(filepath.Join(extraDir, "*.json"))
		for _, f := range files {
			b, err := os.ReadFile(f)
			if err != nil {
				continue
			}
			if err := s.add(b, f); err != nil {
				fmt.Fprintf(os.Stderr, "Vorlage %s übersprungen: %v\n", f, err)
			}
		}
	}
	sort.Slice(s.list, func(i, j int) bool {
		if s.list[i].Category != s.list[j].Category {
			return s.list[i].Category < s.list[j].Category
		}
		return s.list[i].Name < s.list[j].Name
	})
	return s, nil
}

func (s *Set) add(b []byte, src string) error {
	var t Template
	if err := json.Unmarshal(b, &t); err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	if t.ID == "" || t.Name == "" || (t.Kind != "steam" && t.Kind != "image") {
		return fmt.Errorf("%s: id, name und kind (steam|image) sind Pflicht", src)
	}
	if t.Kind == "image" && t.Image == "" {
		return fmt.Errorf("%s: image fehlt", src)
	}
	if err := t.checkConfig(); err != nil {
		return fmt.Errorf("%s: %w", src, err)
	}
	if t.Console == "" {
		t.Console = "stdin"
	}
	if t.MemoryMB == 0 {
		t.MemoryMB = 4096
	}
	if t.MinMemoryMB == 0 {
		t.MinMemoryMB = 1024
	}
	if t.StopTimeout == 0 {
		t.StopTimeout = 30
	}
	if _, dup := s.byID[t.ID]; dup {
		// later files (user dir) override built-ins
		for i := range s.list {
			if s.list[i].ID == t.ID {
				s.list[i] = t
			}
		}
	} else {
		s.list = append(s.list, t)
	}
	s.byID[t.ID] = t
	return nil
}

func (s *Set) All() []Template { return s.list }
func (s *Set) Get(id string) (Template, bool) {
	t, ok := s.byID[id]
	return t, ok
}

var unsafeChars = regexp.MustCompile("[\"'`$\\\\;&|<>\n\r]")
var slugChars = regexp.MustCompile(`[^A-Za-z0-9_-]`)

// Clean removes characters that could break out of a shell argument.
func Clean(v string) string { return strings.TrimSpace(unsafeChars.ReplaceAllString(v, "")) }

// ResolveVars fills defaults, validates types and returns the final values.
func (t Template) ResolveVars(in map[string]string) (map[string]string, error) {
	out := map[string]string{}
	for _, v := range t.Vars {
		val, ok := in[v.Key]
		if !ok || strings.TrimSpace(val) == "" {
			val = v.Default
		}
		if v.Type == "command" {
			// the admin writes this shell command on purpose; only keep it on one line
			val = strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ").Replace(val))
		} else {
			val = Clean(val)
		}
		switch v.Type {
		case "number":
			if val != "" {
				if _, err := strconv.Atoi(val); err != nil {
					return nil, fmt.Errorf("„%s“ muss eine Zahl sein", v.Label)
				}
			}
		case "select":
			if val != "" && len(v.Options) > 0 && !contains(v.Options, val) {
				return nil, fmt.Errorf("„%s“: erlaubt sind %s", v.Label, strings.Join(v.Options, ", "))
			}
		case "bool":
			if val != "true" {
				val = "false"
			}
		case "slug":
			val = slugChars.ReplaceAllString(val, "")
		}
		if v.Needed && val == "" {
			return nil, fmt.Errorf("„%s“ fehlt", v.Label)
		}
		out[v.Key] = val
	}
	return out, nil
}

func contains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}

var placeholder = regexp.MustCompile(`\{\{\s*([a-z0-9_]+)\s*\}\}`)

// Render replaces {{key}} placeholders. ports are the host ports in template order:
// {{port}} is the first, {{port_2}} the second and so on.
func (t Template) Render(s string, vars map[string]string, ports []int) string {
	return placeholder.ReplaceAllStringFunc(s, func(m string) string {
		key := placeholder.FindStringSubmatch(m)[1]
		if key == "port" && len(ports) > 0 {
			return strconv.Itoa(ports[0])
		}
		if strings.HasPrefix(key, "port_") {
			if n, err := strconv.Atoi(strings.TrimPrefix(key, "port_")); err == nil && n >= 1 && n <= len(ports) {
				return strconv.Itoa(ports[n-1])
			}
		}
		for _, v := range t.Vars {
			if v.Key == key && v.Type == "bool" {
				if vars[key] == "true" {
					return v.Flag
				}
				return ""
			}
		}
		return vars[key]
	})
}

// ParsePorts parses "27015/udp, 27015/tcp" for custom templates.
func ParsePorts(s string) ([]Port, error) {
	var out []Port
	for _, part := range strings.Split(s, ",") {
		part = strings.TrimSpace(part)
		if part == "" {
			continue
		}
		num, proto, _ := strings.Cut(part, "/")
		if proto == "" {
			proto = "udp"
		}
		proto = strings.ToLower(proto)
		n, err := strconv.Atoi(num)
		if err != nil || n < 1 || n > 65535 || (proto != "tcp" && proto != "udp") {
			return nil, fmt.Errorf("Port „%s“ ist ungültig. Beispiel: 27015/udp, 27015/tcp", part)
		}
		out = append(out, Port{Port: n, Proto: proto})
	}
	if len(out) == 0 {
		return nil, fmt.Errorf("mindestens ein Port ist nötig")
	}
	return out, nil
}

// ValueKind tells how a missing value is created in a JSON file:
// "number", "bool", "list" or "string".
func (s Setting) ValueKind() string {
	switch s.Type {
	case "number":
		return "number"
	case "bool":
		if s.True == "" && s.False == "" {
			return "bool"
		}
	case "list":
		return "list"
	case "select":
		for _, o := range s.Options {
			if _, err := strconv.ParseFloat(o, 64); err != nil {
				return "string"
			}
		}
		return "number"
	}
	return "string"
}
