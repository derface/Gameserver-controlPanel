package manager

// Automatic settings: every value of a game's config file becomes a card.
// Names, descriptions, ranges and choices come from (in this order) the
// template, AMP's data, or are guessed from the key and the value.

import (
	"path"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/derface/Gameserver-controlPanel/internal/gameconfig"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// cfgFile is one config file as the settings page sees it.
type cfgFile struct {
	c      templates.ConfigFile // Settings = template settings + automatic ones
	data   []byte
	exists bool
	err    error
}

// configFiles returns the template's config files plus the ones AMP knows
// (if they exist), each with its complete list of settings.
func (m *Manager) configFiles(sv store.Server, tpl templates.Template) []cfgFile {
	amp := templates.AMP(tpl.ID)
	var out []cfgFile
	have := map[string]bool{}
	names := map[string]bool{} // values the template's own files already cover
	for _, c := range tpl.Config {
		for _, st := range c.Settings {
			names[strings.ToLower(lastPart(st.Key))] = true
		}
	}
	for _, c := range tpl.Config {
		data, exists, err := m.readConfig(sv, tpl, c)
		have[strings.ToLower(path.Base(c.File))] = true
		if exists && err == nil && (c.Auto == nil || *c.Auto) {
			c.Settings = withAuto(c, data, amp)
		}
		out = append(out, cfgFile{c, data, exists, err})
	}
	if amp == nil {
		return out
	}
	for _, f := range amp.Files {
		rel := path.Clean("server/" + strings.TrimPrefix(f.File, "./"))
		base := strings.ToLower(path.Base(rel))
		if have[base] || strings.HasPrefix(rel, "../") {
			continue
		}
		c := templates.ConfigFile{Title: path.Base(rel), File: rel, Format: f.Format}
		data, exists, err := m.readConfig(sv, tpl, c)
		if err != nil || !exists {
			continue // only files the game has written
		}
		if c.Format == "ini" && strings.Contains(string(data), "OptionSettings=(") {
			c.Format = "palworld"
		}
		have[base] = true
		for n := range names {
			c.Hide = append(c.Hide, n, "*/"+n)
		}
		c.Settings = withAuto(c, data, amp)
		if len(c.Settings) > 0 {
			out = append(out, cfgFile{c, data, true, nil})
		}
	}
	return out
}

// withAuto appends a setting for every value in the file the template does
// not describe itself.
func withAuto(c templates.ConfigFile, data []byte, amp *templates.AMPMeta) []templates.Setting {
	entries, err := gameconfig.List(c.Format, data)
	if err != nil {
		return c.Settings
	}
	out := append([]templates.Setting{}, c.Settings...)
	known := map[string]bool{}
	for i, s := range out {
		known[strings.ToLower(s.Key)] = true
		// the template's own settings borrow description and tab from AMP
		if a := amp.Lookup(s.Key, "", lastPart(s.Key)); a != nil && !a.Hide {
			if s.Help == "" && a.Help != "" {
				out[i].Help = templates.DE(a.Help)
			}
			if s.Group == "" && a.Group != "" && c.AutoGroup == "" {
				out[i].Group = templates.DE(a.Group)
			}
			if s.Type == "number" && s.Min == nil && s.Max == nil {
				out[i].Min, out[i].Max = a.Min, a.Max
			}
		}
		if out[i].Group == "" {
			if c.AutoGroup != "" {
				out[i].Group = c.AutoGroup
			} else if e, ok := sectionOf(s.Key, c.Format); ok {
				out[i].Group = prettySection(e)
			}
		}
	}
	// template settings still without a tab join their neighbour's
	for i := range out {
		if out[i].Group != "" {
			continue
		}
		for j := 1; j < len(out); j++ {
			if i-j >= 0 && out[i-j].Group != "" {
				out[i].Group = out[i-j].Group
				break
			}
			if i+j < len(out) && out[i+j].Group != "" {
				out[i].Group = out[i+j].Group
				break
			}
		}
	}
	const maxAuto = 400 // a few plugin configs are huge
	n := 0
	for _, e := range entries {
		if known[strings.ToLower(e.Key)] || hiddenKey(c.Hide, e.Key) {
			continue
		}
		a := amp.Lookup(e.Key, e.Section, e.Name)
		if a != nil && a.Hide || managedName(e.Name) {
			continue
		}
		if n++; n > maxAuto {
			break
		}
		out = append(out, autoSetting(c, e, a))
	}
	return out
}

func autoSetting(c templates.ConfigFile, e gameconfig.Entry, a *templates.AMPSetting) templates.Setting {
	s := templates.Setting{Key: e.Key, Label: humanize(e.Name)}
	group := c.AutoGroup
	if group == "" {
		group = prettySection(e.Section)
	}
	if group == "" {
		group = "Allgemein"
	}
	if a != nil {
		if a.Label != "" {
			s.Label = templates.DE(a.Label)
		}
		if a.Help != "" {
			s.Help = templates.DE(a.Help)
		}
		if a.Group != "" && c.AutoGroup == "" {
			group = templates.DE(a.Group)
		}
		s.Unit, s.Min, s.Max = templates.DE(a.Unit), a.Min, a.Max
		switch a.Type {
		case "bool":
			t, f := a.True, a.False
			if t == "" {
				t = "true"
			}
			if f == "" {
				f = "false"
			}
			if strings.EqualFold(e.Value, t) || strings.EqualFold(e.Value, f) {
				s.Type, s.True, s.False = "bool", spelled(e.Value, t), spelled(e.Value, f)
			}
		case "select":
			s.Type, s.Options = "select", a.Options
			for _, l := range a.Labels {
				s.Labels = append(s.Labels, templates.DE(l))
			}
			if len(s.Labels) != len(s.Options) {
				s.Labels = nil
			}
		case "number", "secret":
			s.Type = a.Type
		case "list":
			if e.Kind == "list" {
				s.Type = "list"
			}
		}
		if s.Type == "number" && e.Kind != "number" && e.Value != "" {
			s.Type, s.Min, s.Max = "", nil, nil // the file says otherwise
		}
	}
	if s.Type == "" {
		switch {
		case e.Kind == "bool" && c.Format != "cvar":
			s.Type = "bool"
			if e.Value != strings.ToLower(e.Value) { // True/False as in Unreal ini files
				s.True, s.False = "True", "False"
			}
		case e.Kind == "number":
			s.Type = "number"
		case e.Kind == "list":
			s.Type = "list"
		case secretName(e.Name):
			s.Type = "secret"
		}
	}
	s.Group = group
	return s
}

// spelled returns want in the case the file uses.
func spelled(cur, want string) string {
	if cur == strings.ToUpper(cur) {
		return strings.ToUpper(want)
	}
	if cur == strings.ToLower(cur) {
		return strings.ToLower(want)
	}
	return want
}

func lastPart(key string) string {
	if i := strings.LastIndexAny(key, "./"); i >= 0 {
		return key[i+1:]
	}
	return key
}

// sectionOf returns the ini section or json parent of a key.
func sectionOf(key, format string) (string, bool) {
	sep := "."
	if format == "ini" {
		sep = "/"
	} else if format != "json" {
		return "", false
	}
	i := strings.LastIndex(key, sep)
	if i <= 0 {
		return "", false
	}
	return key[:i], true
}

func hiddenKey(patterns []string, key string) bool {
	for _, p := range patterns {
		if strings.EqualFold(p, key) {
			return true
		}
		re := "^" + strings.ReplaceAll(regexp.QuoteMeta(p), `\*`, `[^./]*`) + "$"
		if ok, _ := regexp.MatchString(re, key); ok {
			return true
		}
	}
	return false
}

// words splits "bEnablePvP", "max_players" or "sv-lan" into words.
func words(name string) []string {
	var out []string
	var cur []rune
	rs := []rune(name)
	flush := func() {
		if len(cur) > 0 {
			out = append(out, string(cur))
			cur = nil
		}
	}
	for i, r := range rs {
		switch {
		case r == '_' || r == '-' || r == ' ' || r == '.':
			flush()
			continue
		case unicode.IsUpper(r) && i > 0 && (unicode.IsLower(rs[i-1]) || unicode.IsDigit(rs[i-1]) ||
			(i+1 < len(rs) && unicode.IsLower(rs[i+1]) && unicode.IsUpper(rs[i-1]))):
			flush()
		case unicode.IsDigit(r) && i > 0 && !unicode.IsDigit(rs[i-1]):
			flush()
		}
		cur = append(cur, r)
	}
	flush()
	// "PvP", "PvE" stay one word
	for i := 0; i+1 < len(out); i++ {
		if out[i] == "Pv" && (out[i+1] == "P" || out[i+1] == "E") {
			out[i] += out[i+1]
			out = append(out[:i+1], out[i+2:]...)
		}
	}
	return out
}

var managedWords = map[string]bool{"port": true, "ip": true, "ipaddress": true, "address": true, "bind": true, "multihome": true,
	"dir": true, "directory": true, "path": true, "folder": true, "logfile": true}

// managedName: ports, IP addresses and paths belong to the panel.
func managedName(name string) bool {
	w := words(name)
	if len(w) == 0 {
		return false
	}
	last := strings.ToLower(w[len(w)-1])
	if managedWords[last] {
		return true
	}
	for _, x := range w {
		if strings.EqualFold(x, "port") {
			return true
		}
	}
	return false
}

func secretName(name string) bool {
	n := strings.ToLower(name)
	return strings.Contains(n, "password") || strings.Contains(n, "passwort") || strings.Contains(n, "token") || strings.Contains(n, "secret")
}

// humanize turns "bEnablePlayerToPlayerDamage" into "Enable Player To Player Damage".
func humanize(name string) string {
	w := words(name)
	if len(w) > 1 && w[0] == "b" { // Unreal booleans
		w = w[1:]
	}
	for i, x := range w {
		if x == strings.ToLower(x) && len(x) > 0 {
			r := []rune(x)
			r[0] = unicode.ToUpper(r[0])
			w[i] = string(r)
		}
	}
	return strings.Join(w, " ")
}

// prettySection: "/Script/Pal.PalGameWorldSettings" → "Pal Game World Settings",
// "userGroups.1" → "User Groups 2".
func prettySection(sec string) string {
	if sec == "" {
		return ""
	}
	if i := strings.LastIndexAny(sec, "./"); i >= 0 && i < len(sec)-1 {
		last := sec[i+1:]
		if n := strings.TrimLeft(last, "0123456789"); n == "" { // array index
			prev := sec[:i]
			if j := strings.LastIndexAny(prev, "./"); j >= 0 {
				prev = prev[j+1:]
			}
			k, _ := strconv.Atoi(last)
			return humanize(prev) + " " + strconv.Itoa(k+1)
		}
		sec = last
	}
	return humanize(sec)
}
