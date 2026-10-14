package manager

import (
	"errors"
	"fmt"
	"strconv"
	"strings"
)

// The AI assistant can also change an existing server. It gets the
// server's settings as a compact list and answers with values; the panel
// checks them against the same rules as the settings form and shows the
// differences before anything is written.

// SettingsCatalog lists a server's settings for the AI (no secrets).
func (m *Manager) SettingsCatalog(id string) (string, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return "", err
	}
	v, err := m.Settings(id)
	if err != nil {
		return "", err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Server %q (id=%s, Spiel %s, RAM %d MB, läuft: %v)\n", sv.Name, sv.ID, tpl.Name, sv.MemoryMB, v.Running)
	lines := 0
	kind := func(typ string, opts []string, min, max *float64, unit string) string {
		switch typ {
		case "bool":
			return "true/false"
		case "select":
			return "eins von " + strings.Join(opts, "/")
		case "number":
			s := "Zahl"
			if min != nil || max != nil {
				lo, hi := "", ""
				if min != nil {
					lo = strconv.FormatFloat(*min, 'f', -1, 64)
				}
				if max != nil {
					hi = strconv.FormatFloat(*max, 'f', -1, 64)
				}
				s += " " + lo + "–" + hi
			}
			if unit != "" {
				s += " " + unit
			}
			return s
		case "list":
			return "Liste, kommagetrennt"
		}
		return "Text"
	}
	short := func(s string) string {
		if r := []rune(s); len(r) > 90 {
			return string(r[:90]) + "…"
		}
		return s
	}
	b.WriteString("Startwerte (vars):\n")
	for _, x := range v.Vars {
		if x.Type == "secret" || x.Type == "command" {
			continue
		}
		fmt.Fprintf(&b, "- %s | %s | aktuell %q | %s", x.Key, x.Label, x.Value, kind(x.Type, x.Options, nil, nil, ""))
		if x.Help != "" {
			b.WriteString(" | " + short(x.Help))
		}
		b.WriteString("\n")
		lines++
	}
	for i, f := range v.Files {
		if !f.Editable || len(f.Settings) == 0 {
			continue
		}
		fmt.Fprintf(&b, "Datei %d (files.\"%d\"): %s\n", i, i, f.Title)
		for _, s := range f.Settings {
			if s.Type == "secret" || lines > 450 {
				continue
			}
			cur := s.Value
			if !s.Present {
				cur = "(nicht gesetzt)"
			}
			fmt.Fprintf(&b, "- %s | %s | aktuell %q | %s", s.Key, s.Label, cur, kind(s.Type, s.Options, s.Min, s.Max, s.Unit))
			if s.When != "" {
				b.WriteString(" | wirkt nur wenn " + s.When)
			}
			if s.Help != "" {
				b.WriteString(" | " + short(s.Help))
			}
			b.WriteString("\n")
			lines++
		}
	}
	return b.String(), nil
}

// SettingChange is one difference the AI proposes.
type SettingChange struct {
	Where string `json:"where"` // "vars" or the file index
	Key   string `json:"key"`
	Label string `json:"label"`
	Old   string `json:"old"`
	New   string `json:"new"`
}

type aiSlot struct {
	where string // "vars" or file index
	file  string // title
	v     *VarValue
	s     *SettingValue
}

func (x aiSlot) key() string {
	if x.v != nil {
		return x.v.Key
	}
	return x.s.Key
}

func (x aiSlot) label() string {
	if x.v != nil {
		return x.v.Label
	}
	return x.s.Label
}

// CheckChanges validates proposed values and returns the real differences.
// Models are not exact: a key may come without its prefix
// ("experienceCombatFactor"), under "vars" although it is in a file, or with
// the file name instead of its number. Unknown keys and invalid values are
// dropped with a note, numbers are clamped to the game's range.
func (m *Manager) CheckChanges(id string, vars map[string]string, filesIn map[string]map[string]string) ([]SettingChange, []string, error) {
	v, err := m.Settings(id)
	if err != nil {
		return nil, nil, err
	}
	var slots []aiSlot
	for i := range v.Vars {
		if t := v.Vars[i].Type; t != "secret" && t != "command" {
			slots = append(slots, aiSlot{where: "vars", v: &v.Vars[i]})
		}
	}
	for fi := range v.Files {
		f := &v.Files[fi]
		if !f.Editable {
			continue
		}
		for j := range f.Settings {
			if f.Settings[j].Type != "secret" {
				slots = append(slots, aiSlot{where: strconv.Itoa(fi), file: f.Title, s: &f.Settings[j]})
			}
		}
	}
	fileHint := func(h string) string {
		if h == "" || h == "vars" {
			return h
		}
		if _, err := strconv.Atoi(h); err == nil {
			return h
		}
		lh := strings.ToLower(h)
		for fi, f := range v.Files {
			if strings.ToLower(f.Title) == lh || strings.HasSuffix(strings.ToLower(f.File), lh) {
				return strconv.Itoa(fi)
			}
		}
		return ""
	}
	find := func(hint, key string) *aiSlot {
		pick := func(match func(aiSlot) bool) *aiSlot {
			var hits []int
			for i, x := range slots {
				if match(x) {
					hits = append(hits, i)
				}
			}
			for _, i := range hits {
				if slots[i].where == hint {
					return &slots[i]
				}
			}
			if len(hits) == 1 {
				return &slots[hits[0]]
			}
			return nil
		}
		lk := strings.ToLower(strings.TrimSpace(key))
		tail := func(s string) string { return s[strings.LastIndexAny(s, "./")+1:] }
		for _, match := range []func(aiSlot) bool{
			func(x aiSlot) bool { return x.key() == key },
			func(x aiSlot) bool { return strings.ToLower(x.key()) == lk },
			func(x aiSlot) bool { return strings.ToLower(tail(x.key())) == tail(lk) },
			func(x aiSlot) bool { return strings.ToLower(x.label()) == lk },
		} {
			if r := pick(match); r != nil {
				return r
			}
		}
		return nil
	}
	type prop struct{ hint, key, val string }
	var props []prop
	for k, val := range vars {
		props = append(props, prop{"vars", k, val})
	}
	for fi, kv := range filesIn {
		h := fileHint(fi)
		for k, val := range kv {
			props = append(props, prop{h, k, val})
		}
	}
	var out []SettingChange
	var notes []string
	seen := map[string]bool{}
	for _, p := range props {
		x := find(p.hint, p.key)
		if x == nil {
			notes = append(notes, "„"+p.key+"“ gibt es bei diesem Server nicht, ausgelassen")
			continue
		}
		id := x.where + "/" + x.key()
		if seen[id] {
			continue
		}
		seen[id] = true
		val := strings.TrimSpace(p.val)
		if x.v != nil {
			vv := x.v
			if vv.Type == "select" && len(vv.Options) > 0 && !strContains(vv.Options, val) {
				notes = append(notes, fmt.Sprintf("„%s“: %q ist keine erlaubte Auswahl, ausgelassen", vv.Label, val))
				continue
			}
			if vv.Type == "bool" {
				if val = normBool(val); val == "" {
					notes = append(notes, fmt.Sprintf("„%s“: nur an oder aus, ausgelassen", vv.Label))
					continue
				}
			}
			if vv.Type == "number" {
				if _, err := strconv.Atoi(val); err != nil {
					notes = append(notes, fmt.Sprintf("„%s“ muss eine ganze Zahl sein, ausgelassen", vv.Label))
					continue
				}
			}
			if val != vv.Value {
				out = append(out, SettingChange{Where: "vars", Key: vv.Key, Label: vv.Label, Old: vv.Value, New: val})
			}
			continue
		}
		st := x.s
		if st.Type == "bool" {
			if b := normBool(val); b != "" {
				val = b
			}
		}
		if st.Type == "number" {
			// the game's limits beat the AI's wish (Enshrouded: XP at most x2)
			if n, err := strconv.ParseFloat(val, 64); err == nil {
				c := n
				if st.Min != nil && c < *st.Min {
					c = *st.Min
				}
				if st.Max != nil && c > *st.Max {
					c = *st.Max
				}
				if c != n {
					val = strconv.FormatFloat(c, 'f', -1, 64)
					notes = append(notes, fmt.Sprintf("„%s“: %s geht im Spiel nicht, auf %s begrenzt", st.Label, strconv.FormatFloat(n, 'f', -1, 64), val))
				}
			}
		}
		if st.Type == "select" && len(st.Options) > 0 && !strContains(st.Options, val) {
			// "custom" for "Custom"
			for _, o := range st.Options {
				if strings.EqualFold(o, val) {
					val = o
				}
			}
			if !strContains(st.Options, val) {
				notes = append(notes, fmt.Sprintf("„%s“: %q ist keine erlaubte Auswahl, ausgelassen", st.Label, val))
				continue
			}
		}
		if _, err := fileValue(st.Setting, val); err != nil {
			notes = append(notes, err.Error()+", ausgelassen")
			continue
		}
		same := val == st.Value
		if st.Type == "number" {
			a, e1 := strconv.ParseFloat(val, 64)
			b, e2 := strconv.ParseFloat(st.Value, 64)
			same = same || (e1 == nil && e2 == nil && a == b)
		}
		if !same || !st.Present {
			out = append(out, SettingChange{Where: x.where, Key: st.Key, Label: st.Label, Old: st.Value, New: val})
		}
	}
	if len(out) == 0 && len(notes) > 0 {
		n := notes
		if len(n) > 4 {
			n = append(n[:4:4], fmt.Sprintf("… und %d weitere", len(notes)-4))
		}
		return nil, notes, errors.New("Keiner der Vorschläge der KI passte zu den Einstellungen dieses Servers: " + strings.Join(n, "; "))
	}
	return out, notes, nil
}

func normBool(v string) string {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "1", "yes", "ja", "an", "on":
		return "true"
	case "false", "0", "no", "nein", "aus", "off":
		return "false"
	}
	return ""
}

func strContains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
