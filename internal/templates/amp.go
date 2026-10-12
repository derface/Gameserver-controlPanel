package templates

// Setting descriptions taken from CubeCoders' AMPTemplates (MIT, see
// games/AMPTEMPLATES-LICENSE.md), converted by tools/gen_amp_meta.py, plus
// German translations in amp/de.json (tools/amp_de.py).

import (
	_ "embed"
	"encoding/json"
	"strings"
	"sync"
)

//go:embed amp/meta.json
var ampMetaJSON []byte

//go:embed amp/de.json
var ampDeJSON []byte

// AMPSetting describes one value of a game's config file.
type AMPSetting struct {
	Key     string   `json:"key"`
	Label   string   `json:"label"`
	Help    string   `json:"help"`
	Type    string   `json:"type"` // number | bool | select | text | secret | list
	Options []string `json:"options"`
	Labels  []string `json:"labels"`
	True    string   `json:"true"`
	False   string   `json:"false"`
	Min     *float64 `json:"min"`
	Max     *float64 `json:"max"`
	Unit    string   `json:"unit"`
	Group   string   `json:"group"`
	Hide    bool     `json:"hide"` // ports, IPs and other things the panel manages
	Cmd     bool     `json:"cmd"`  // passed on the command line by AMP
}

// AMPFile is a config file AMP knows for a game, relative to the install dir.
type AMPFile struct {
	File   string `json:"file"`
	Format string `json:"format"`
}

// AMPMeta is what AMP knows about one game.
type AMPMeta struct {
	Source   string       `json:"source"`
	Files    []AMPFile    `json:"files"`
	Settings []AMPSetting `json:"settings"`
	byKey    map[string]*AMPSetting
	byName   map[string]*AMPSetting
}

var (
	ampOnce sync.Once
	ampAll  map[string]*AMPMeta
	ampDe   map[string]string
)

func loadAMP() {
	ampAll = map[string]*AMPMeta{}
	_ = json.Unmarshal(ampMetaJSON, &ampAll)
	for _, m := range ampAll {
		m.byKey, m.byName = map[string]*AMPSetting{}, map[string]*AMPSetting{}
		for i := range m.Settings {
			s := &m.Settings[i]
			for _, k := range []string{s.Key, strings.ReplaceAll(s.Key, ".", "/")} {
				if _, ok := m.byKey[strings.ToLower(k)]; !ok {
					m.byKey[strings.ToLower(k)] = s
				}
			}
			name := s.Key
			if i := strings.LastIndexAny(name, "./"); i >= 0 {
				name = name[i+1:]
			}
			if prev, ok := m.byName[strings.ToLower(name)]; !ok || (prev.Hide && !s.Hide) {
				m.byName[strings.ToLower(name)] = s
			}
		}
	}
	ampDe = map[string]string{}
	_ = json.Unmarshal(ampDeJSON, &ampDe)
}

// AMP returns AMP's data for a template id, or nil.
func AMP(id string) *AMPMeta {
	ampOnce.Do(loadAMP)
	return ampAll[id]
}

// Lookup finds the description of a config value: by its full key
// ("Section/Name", "a.b"), then by section + name, then by name alone
// (not for values nested in JSON objects: "userGroups.0.name" is no server name).
func (m *AMPMeta) Lookup(key, section, name string) *AMPSetting {
	if m == nil {
		return nil
	}
	for _, k := range []string{key, section + "." + name, section + "/" + name} {
		if s, ok := m.byKey[strings.ToLower(k)]; ok {
			return s
		}
	}
	if strings.Contains(key, ".") && !strings.Contains(key, "/") && section != "" {
		return nil
	}
	return m.byName[strings.ToLower(name)]
}

// DE translates an English text from AMP; unknown texts stay as they are.
func DE(s string) string {
	ampOnce.Do(loadAMP)
	if t, ok := ampDe[s]; ok && t != "" {
		return t
	}
	return s
}
