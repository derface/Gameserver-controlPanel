package gameconfig

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Entry is one value found in a config file.
type Entry struct {
	Key     string // as Get/Set expect it ("Section/Name" for ini, "a.b" for json)
	Name    string // the last part of the key, e.g. "MaxPlayers"
	Section string // ini section or json parent path, "" at the top
	Value   string
	Kind    string // "bool", "number", "list" or "string" as written in the file
}

// List returns every simple value of a config file in file order. Lines that
// cannot be edited safely (Unreal "+Array=" lines, nested blocks, objects in
// JSON lists) are left out.
func List(format string, data []byte) ([]Entry, error) {
	var out []Entry
	seen := map[string]bool{}
	add := func(e Entry) {
		if e.Key == "" || seen[e.Key] {
			return // duplicate keys: only the first one is what Get/Set touch
		}
		seen[e.Key] = true
		if e.Name == "" {
			e.Name = e.Key
		}
		if e.Kind == "" {
			e.Kind = kindOf(e.Value)
		}
		out = append(out, e)
	}
	switch format {
	case "properties":
		for _, l := range lines(data) {
			t := strings.TrimSpace(strings.TrimSuffix(l, "\r"))
			if t == "" || t[0] == '#' || t[0] == '!' || t[0] == ';' {
				continue
			}
			if k, v, ok := strings.Cut(t, "="); ok {
				add(Entry{Key: strings.TrimSpace(k), Value: strings.TrimSpace(v)})
			}
		}
	case "ini":
		cur := ""
		for _, l := range lines(data) {
			t := strings.TrimSpace(strings.TrimSuffix(l, "\r"))
			if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
				cur = t[1 : len(t)-1]
				continue
			}
			if cur == "" || t == "" || strings.ContainsRune(";#+-!.", rune(t[0])) {
				continue
			}
			k, v, ok := strings.Cut(t, "=")
			k = strings.TrimSpace(k)
			if !ok || k == "" || strings.ContainsAny(k, " \t/") {
				continue
			}
			add(Entry{Key: cur + "/" + k, Name: k, Section: cur, Value: strings.TrimSpace(v)})
		}
	case "cvar":
		for _, l := range lines(data) {
			t := strings.TrimSpace(strings.TrimSuffix(l, "\r"))
			if t == "" || strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
				continue
			}
			f := strings.Fields(t)
			if len(f) < 2 || !cvarName.MatchString(f[0]) || cvarCommands[strings.ToLower(f[0])] {
				continue
			}
			if _, v, ok := findCvar(data, f[0]); ok {
				add(Entry{Key: f[0], Value: v})
			}
		}
	case "xmlprop":
		for _, m := range xmlAllProps.FindAllSubmatch(data, -1) {
			add(Entry{Key: string(m[1]), Value: xmlUnescape(string(m[2]))})
		}
	case "palworld":
		_, _, fields, err := palFields(data)
		if err != nil {
			return nil, err
		}
		for _, f := range fields {
			k := strings.TrimSpace(string(data[f[0]:f[1]]))
			v := string(data[f[2]:f[3]])
			if strings.HasPrefix(v, "(") {
				continue // nested lists like CrossplayPlatforms=(Steam,Xbox)
			}
			quoted := len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"'
			if quoted {
				v = v[1 : len(v)-1]
			}
			e := Entry{Key: k, Value: v}
			if quoted {
				e.Kind = "string"
			}
			add(e)
		}
	case "armacfg":
		depth := 0
		for _, l := range lines(data) {
			t := strings.TrimSpace(strings.TrimSuffix(l, "\r"))
			if strings.HasPrefix(t, "//") {
				continue
			}
			if depth == 0 {
				if m := armaLine.FindStringSubmatch(strings.TrimSuffix(l, "\r")); m != nil && !strings.HasSuffix(m[2], "[]") {
					_, v, quoted, _ := findArma(data, m[2])
					e := Entry{Key: m[2], Value: v}
					if quoted {
						e.Kind = "string"
					}
					add(e)
				}
			}
			depth += strings.Count(t, "{") - strings.Count(t, "}")
		}
	case "json":
		root, err := parseOrdered(data)
		if err != nil {
			return nil, fmt.Errorf("JSON ungültig: %w", err)
		}
		var walk func(n *node, path []string)
		walk = func(n *node, path []string) {
			key := strings.Join(path, ".")
			sec := ""
			if len(path) > 1 {
				sec = strings.Join(path[:len(path)-1], ".")
			}
			name := ""
			if len(path) > 0 {
				name = path[len(path)-1]
			}
			switch n.kind {
			case 'o':
				for i, k := range n.keys {
					walk(n.vals[i], append(append([]string{}, path...), k))
				}
			case 'a':
				allStr := true
				for _, v := range n.vals {
					allStr = allStr && v.kind == 's'
				}
				if allStr && len(path) > 0 {
					var parts []string
					for _, v := range n.vals {
						parts = append(parts, v.str)
					}
					add(Entry{Key: key, Name: name, Section: sec, Value: strings.Join(parts, ", "), Kind: "list"})
					return
				}
				for i, v := range n.vals {
					if v.kind == 'o' { // e.g. userGroups.0.password
						walk(v, append(append([]string{}, path...), strconv.Itoa(i)))
					}
				}
			case 's':
				add(Entry{Key: key, Name: name, Section: sec, Value: n.str, Kind: "string"})
			case 'n':
				add(Entry{Key: key, Name: name, Section: sec, Value: n.raw, Kind: "number"})
			case 'b':
				add(Entry{Key: key, Name: name, Section: sec, Value: n.raw, Kind: "bool"})
			case 'z':
				add(Entry{Key: key, Name: name, Section: sec, Value: "", Kind: "string"})
			}
		}
		walk(root, nil)
	default:
		return nil, fmt.Errorf("unbekanntes Format %q", format)
	}
	return out, nil
}

var (
	xmlAllProps  = regexp.MustCompile(`<property\s+name="([^"]+)"\s+value="([^"]*)"`)
	cvarName     = regexp.MustCompile(`^[A-Za-z_][A-Za-z0-9_.]*$`)
	cvarCommands = map[string]bool{"exec": true, "alias": true, "bind": true, "echo": true, "say": true, "map": true, "changelevel": true, "writeid": true, "writeip": true, "banid": true, "addip": true, "removeid": true, "sm": true, "plugin_load": true}
	numberRe     = regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`)
)

// kindOf guesses the kind of a value written as text.
func kindOf(v string) string {
	switch strings.ToLower(v) {
	case "true", "false":
		return "bool"
	}
	if numberRe.MatchString(v) {
		return "number"
	}
	return "string"
}
