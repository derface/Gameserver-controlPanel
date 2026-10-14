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

// CheckChanges validates proposed values and returns the real differences.
// Unknown keys and invalid values are dropped with a note.
func (m *Manager) CheckChanges(id string, vars map[string]string, filesIn map[string]map[string]string) ([]SettingChange, []string, error) {
	_, tpl, err := m.get(id)
	if err != nil {
		return nil, nil, err
	}
	v, err := m.Settings(id)
	if err != nil {
		return nil, nil, err
	}
	var out []SettingChange
	var notes []string
	for k, val := range vars {
		var found *VarValue
		for i := range v.Vars {
			if v.Vars[i].Key == k {
				found = &v.Vars[i]
			}
		}
		if found == nil || found.Type == "secret" || found.Type == "command" {
			notes = append(notes, "Startwert „"+k+"“ gibt es nicht, ausgelassen")
			continue
		}
		if found.Type == "select" && len(found.Options) > 0 && !strContains(found.Options, val) {
			notes = append(notes, fmt.Sprintf("„%s“: %q ist keine erlaubte Auswahl, ausgelassen", found.Label, val))
			continue
		}
		if found.Type == "bool" && val != "true" && val != "false" {
			notes = append(notes, fmt.Sprintf("„%s“: nur true/false, ausgelassen", found.Label))
			continue
		}
		if val != found.Value {
			out = append(out, SettingChange{Where: "vars", Key: k, Label: found.Label, Old: found.Value, New: val})
		}
	}
	for fi, kv := range filesIn {
		i, err := strconv.Atoi(fi)
		if err != nil || i < 0 || i >= len(v.Files) || !v.Files[i].Editable {
			notes = append(notes, "Datei "+fi+" gibt es nicht, ausgelassen")
			continue
		}
		f := v.Files[i]
		for k, val := range kv {
			var found *SettingValue
			for j := range f.Settings {
				if f.Settings[j].Key == k {
					found = &f.Settings[j]
				}
			}
			if found == nil || found.Type == "secret" {
				notes = append(notes, "„"+k+"“ gibt es in "+f.Title+" nicht, ausgelassen")
				continue
			}
			if found.Type == "number" {
				// the game's limits beat the AI's wish (Enshrouded: XP at most x2)
				if n, err := strconv.ParseFloat(strings.TrimSpace(val), 64); err == nil {
					c := n
					if found.Min != nil && c < *found.Min {
						c = *found.Min
					}
					if found.Max != nil && c > *found.Max {
						c = *found.Max
					}
					if c != n {
						val = strconv.FormatFloat(c, 'f', -1, 64)
						notes = append(notes, fmt.Sprintf("„%s“: %s geht im Spiel nicht, auf %s begrenzt", found.Label, strconv.FormatFloat(n, 'f', -1, 64), val))
					}
				}
			}
			if _, err := fileValue(found.Setting, val); err != nil {
				notes = append(notes, err.Error()+", ausgelassen")
				continue
			}
			if found.Type == "select" && len(found.Options) > 0 && !strContains(found.Options, val) {
				notes = append(notes, fmt.Sprintf("„%s“: %q ist keine erlaubte Auswahl, ausgelassen", found.Label, val))
				continue
			}
			same := val == found.Value
			if found.Type == "number" {
				a, e1 := strconv.ParseFloat(val, 64)
				b, e2 := strconv.ParseFloat(found.Value, 64)
				same = same || (e1 == nil && e2 == nil && a == b)
			}
			if !same || !found.Present {
				out = append(out, SettingChange{Where: fi, Key: k, Label: found.Label, Old: found.Value, New: val})
			}
		}
	}
	_ = tpl
	if len(out) == 0 && len(notes) > 0 {
		return nil, notes, errors.New("Keiner der Vorschläge der KI passte zu den Einstellungen dieses Servers")
	}
	return out, notes, nil
}

func strContains(xs []string, x string) bool {
	for _, y := range xs {
		if y == x {
			return true
		}
	}
	return false
}
