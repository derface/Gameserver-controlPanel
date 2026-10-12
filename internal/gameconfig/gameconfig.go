// Package gameconfig reads and changes single values in game configuration
// files without touching anything else in them: comments, order and unknown
// keys stay as they are.
//
// Formats and what a key means:
//
//	properties  key=value lines (Minecraft server.properties, Zomboid .ini)
//	ini         "Section/Key" in an INI file (Unreal GameUserSettings.ini)
//	json        dotted path, numbers for array indexes ("userGroups.0.password")
//	cvar        Source/GoldSrc config lines: key "value"
//	xmlprop     <property name="Key" value="…"/> (7 Days to Die)
//	palworld    a field inside OptionSettings=(…) of PalWorldSettings.ini
//	armacfg     key = value; lines of Arma 3 / DayZ server.cfg (top level only)
package gameconfig

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

// Formats lists the supported formats.
var Formats = map[string]bool{"properties": true, "ini": true, "json": true, "cvar": true, "xmlprop": true, "palworld": true, "armacfg": true}

// CanCreate reports whether a missing file can be started from scratch.
// The others need the structure the game writes on its first start.
func CanCreate(format string) bool {
	return format == "properties" || format == "cvar" || format == "ini" || format == "armacfg"
}

// Get returns the value of key and whether it exists.
func Get(format string, data []byte, key string) (string, bool, error) {
	switch format {
	case "properties":
		_, v, ok := findProp(data, key)
		return v, ok, nil
	case "ini":
		sec, k, err := splitIni(key)
		if err != nil {
			return "", false, err
		}
		_, v, ok := findIni(data, sec, k)
		return v, ok, nil
	case "cvar":
		_, v, ok := findCvar(data, key)
		return v, ok, nil
	case "xmlprop":
		loc := xmlPropRe(key).FindSubmatchIndex(data)
		if loc == nil {
			return "", false, nil
		}
		return xmlUnescape(string(data[loc[2]:loc[3]])), true, nil
	case "palworld":
		_, v, ok := findPal(data, key)
		return v, ok, nil
	case "armacfg":
		_, v, _, ok := findArma(data, key)
		return v, ok, nil
	case "json":
		var doc any
		dec := json.NewDecoder(bytes.NewReader(data))
		dec.UseNumber() // keep numbers as written ("1.0" stays "1.0")
		if err := dec.Decode(&doc); err != nil {
			return "", false, fmt.Errorf("JSON ungültig: %w", err)
		}
		v, ok := jsonGet(doc, key)
		if !ok {
			return "", false, nil
		}
		switch x := v.(type) {
		case string:
			return x, true, nil
		case bool:
			return strconv.FormatBool(x), true, nil
		case json.Number:
			return x.String(), true, nil
		case nil:
			return "", true, nil
		case []any: // a list of words, e.g. server tags
			parts := make([]string, 0, len(x))
			for _, e := range x {
				str, ok := e.(string)
				if !ok {
					return "", false, nil
				}
				parts = append(parts, str)
			}
			return strings.Join(parts, ", "), true, nil
		}
		return "", false, nil // objects are not editable values
	}
	return "", false, fmt.Errorf("unbekanntes Format %q", format)
}

// Set changes the given keys and returns the new file content. Keys that
// do not exist are appended where the format allows it (properties, cvar,
// ini); for the others they are an error.
func Set(format string, data []byte, values map[string]string, keys []string) ([]byte, error) {
	return SetKinds(format, data, values, keys, nil)
}

// SetKinds is Set with the value kinds of the keys ("number", "bool",
// "list", "string"). JSON uses them to create missing values with the right
// type; without a kind a missing JSON value is an error.
func SetKinds(format string, data []byte, values map[string]string, keys []string, kinds map[string]string) ([]byte, error) {
	if format == "json" {
		return setJSON(data, values, keys, kinds)
	}
	for _, k := range keys {
		v, ok := values[k]
		if !ok {
			continue
		}
		if strings.ContainsAny(v, "\r\n") {
			return nil, fmt.Errorf("%s: Zeilenumbrüche sind nicht erlaubt", k)
		}
		var err error
		switch format {
		case "properties":
			data = setProp(data, k, v)
		case "ini":
			data, err = setIni(data, k, v)
		case "cvar":
			if strings.Contains(v, `"`) {
				return nil, fmt.Errorf("%s: Anführungszeichen sind nicht erlaubt", k)
			}
			data = setCvar(data, k, v)
		case "xmlprop":
			loc := xmlPropRe(k).FindSubmatchIndex(data)
			if loc == nil {
				return nil, fmt.Errorf("%s fehlt in der Datei", k)
			}
			data = splice(data, loc[2], loc[3], xmlEscape(v))
		case "palworld":
			data, err = setPal(data, k, v)
		case "armacfg":
			data, err = setArma(data, k, v)
		default:
			return nil, fmt.Errorf("unbekanntes Format %q", format)
		}
		if err != nil {
			return nil, err
		}
	}
	return data, nil
}

func splice(data []byte, a, b int, s string) []byte {
	out := make([]byte, 0, len(data)-(b-a)+len(s))
	out = append(out, data[:a]...)
	out = append(out, s...)
	return append(out, data[b:]...)
}

// ---------- properties ----------

func lines(data []byte) []string {
	return strings.Split(string(data), "\n")
}

func findProp(data []byte, key string) (int, string, bool) {
	for i, l := range lines(data) {
		t := strings.TrimSpace(strings.TrimSuffix(l, "\r"))
		if t == "" || t[0] == '#' || t[0] == '!' || t[0] == ';' {
			continue
		}
		if k, v, ok := strings.Cut(t, "="); ok && strings.TrimSpace(k) == key {
			return i, strings.TrimSpace(v), true
		}
	}
	return -1, "", false
}

func setProp(data []byte, key, val string) []byte {
	ls := lines(data)
	if i, _, ok := findProp(data, key); ok {
		ls[i] = replaceValue(ls[i], val)
		return []byte(strings.Join(ls, "\n"))
	}
	return appendLine(data, key+"="+val)
}

// replaceValue swaps the value of a "key = value" line and keeps the
// indentation, the spacing around "=" and a Windows line end.
func replaceValue(line, val string) string {
	cr := strings.HasSuffix(line, "\r")
	line = strings.TrimSuffix(line, "\r")
	eq := strings.Index(line, "=")
	rest := line[eq+1:]
	lead := rest[:len(rest)-len(strings.TrimLeft(rest, " \t"))]
	trail := rest[len(strings.TrimRight(rest, " \t")):]
	if strings.TrimSpace(rest) == "" {
		trail = ""
	}
	out := line[:eq+1] + lead + val + trail
	if cr {
		out += "\r"
	}
	return out
}

func appendLine(data []byte, line string) []byte {
	if len(data) > 0 && data[len(data)-1] != '\n' {
		data = append(data, '\n')
	}
	return append(append(data, line...), '\n')
}

// ---------- ini ----------

func splitIni(key string) (string, string, error) {
	i := strings.LastIndex(key, "/")
	if i <= 0 {
		return "", "", fmt.Errorf("INI-Schlüssel %q braucht die Form Abschnitt/Name", key)
	}
	return key[:i], key[i+1:], nil
}

// findIni returns the line index of the key (or -1), its value, and found.
func findIni(data []byte, sec, key string) (int, string, bool) {
	cur := ""
	for i, l := range lines(data) {
		t := strings.TrimSpace(strings.TrimSuffix(l, "\r"))
		if strings.HasPrefix(t, "[") && strings.HasSuffix(t, "]") {
			cur = t[1 : len(t)-1]
			continue
		}
		if cur != sec || t == "" || t[0] == ';' || t[0] == '#' {
			continue
		}
		if k, v, ok := strings.Cut(t, "="); ok && strings.TrimSpace(k) == key {
			return i, strings.TrimSpace(v), true
		}
	}
	return -1, "", false
}

func setIni(data []byte, full, val string) ([]byte, error) {
	sec, key, err := splitIni(full)
	if err != nil {
		return nil, err
	}
	ls := lines(data)
	if i, _, ok := findIni(data, sec, key); ok {
		ls[i] = replaceValue(ls[i], val)
		return []byte(strings.Join(ls, "\n")), nil
	}
	// insert after the section header, or add the section
	for i, l := range ls {
		if strings.TrimSpace(strings.TrimSuffix(l, "\r")) == "["+sec+"]" {
			out := append(append(append([]string{}, ls[:i+1]...), key+"="+val), ls[i+1:]...)
			return []byte(strings.Join(out, "\n")), nil
		}
	}
	return appendLine(appendLine(data, "["+sec+"]"), key+"="+val)[:], nil
}

// ---------- cvar ----------

func findCvar(data []byte, key string) (int, string, bool) {
	i, a, b, _ := cvarSpan(data, key)
	if i < 0 {
		return -1, "", false
	}
	return i, lines(data)[i][a:b], true
}

// cvarSpan finds `key value` or `key "value"` and returns the line and the
// byte range of the value inside it (without quotes).
func cvarSpan(data []byte, key string) (line, start, end int, quoted bool) {
	for i, l := range lines(data) {
		t := strings.TrimLeft(l, " \t")
		if t == "" || strings.HasPrefix(t, "//") {
			continue
		}
		f := strings.Fields(t)
		if len(f) == 0 || f[0] != key {
			continue
		}
		off := len(l) - len(t) + len(f[0])
		rest := l[off:]
		lead := len(rest) - len(strings.TrimLeft(rest, " \t"))
		p := off + lead
		if strings.HasPrefix(l[p:], `"`) {
			if j := strings.Index(l[p+1:], `"`); j >= 0 {
				return i, p + 1, p + 1 + j, true
			}
			return i, p + 1, len(strings.TrimRight(l, "\r")), true
		}
		e := p
		for e < len(l) && l[e] != ' ' && l[e] != '\t' && l[e] != '\r' && l[e] != ';' && !strings.HasPrefix(l[e:], "//") {
			e++
		}
		return i, p, e, false
	}
	return -1, 0, 0, false
}

func setCvar(data []byte, key, val string) []byte {
	i, a, b, quoted := cvarSpan(data, key)
	if i < 0 {
		return appendLine(data, key+` "`+val+`"`)
	}
	ls := lines(data)
	l := ls[i]
	if !quoted && (val == "" || strings.ContainsAny(val, " \t;/")) {
		ls[i] = l[:a] + `"` + val + `"` + l[b:]
	} else {
		ls[i] = l[:a] + val + l[b:]
	}
	return []byte(strings.Join(ls, "\n"))
}

// ---------- xml property ----------

func xmlPropRe(key string) *regexp.Regexp {
	return regexp.MustCompile(`<property\s+name="` + regexp.QuoteMeta(key) + `"\s+value="([^"]*)"`)
}

var xmlEsc = strings.NewReplacer("&", "&amp;", "<", "&lt;", ">", "&gt;", `"`, "&quot;")
var xmlUnesc = strings.NewReplacer("&quot;", `"`, "&lt;", "<", "&gt;", ">", "&amp;", "&")

func xmlEscape(s string) string   { return xmlEsc.Replace(s) }
func xmlUnescape(s string) string { return xmlUnesc.Replace(s) }

// ---------- palworld OptionSettings=(…) ----------

var palOpt = regexp.MustCompile(`OptionSettings=\(`)

// palFields returns the byte ranges of all Key=Value fields inside (…).
func palFields(data []byte) (start, end int, fields [][4]int, err error) {
	loc := palOpt.FindIndex(data)
	if loc == nil {
		return 0, 0, nil, errors.New("OptionSettings fehlt in der Datei")
	}
	i := loc[1]
	start = i
	for i < len(data) {
		// key
		ks := i
		for i < len(data) && data[i] != '=' && data[i] != ')' && data[i] != ',' {
			i++
		}
		if i >= len(data) {
			break
		}
		if data[i] == ')' {
			return start, i, fields, nil
		}
		if data[i] == ',' {
			i++
			continue
		}
		ke := i
		i++ // '='
		vs := i
		depth, inq := 0, false
		for i < len(data) {
			c := data[i]
			if inq {
				if c == '"' {
					inq = false
				}
			} else if c == '"' {
				inq = true
			} else if c == '(' {
				depth++
			} else if c == ')' {
				if depth == 0 {
					break
				}
				depth--
			} else if c == ',' && depth == 0 {
				break
			}
			i++
		}
		fields = append(fields, [4]int{ks, ke, vs, i})
		if i < len(data) && data[i] == ',' {
			i++
		}
	}
	return start, i, fields, errors.New("OptionSettings ist nicht geschlossen")
}

func findPal(data []byte, key string) (int, string, bool) {
	_, _, fields, err := palFields(data)
	if err != nil {
		return -1, "", false
	}
	for n, f := range fields {
		if strings.TrimSpace(string(data[f[0]:f[1]])) == key {
			v := string(data[f[2]:f[3]])
			if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
				v = v[1 : len(v)-1]
			}
			return n, v, true
		}
	}
	return -1, "", false
}

func setPal(data []byte, key, val string) ([]byte, error) {
	_, _, fields, err := palFields(data)
	if err != nil {
		return nil, err
	}
	for _, f := range fields {
		if strings.TrimSpace(string(data[f[0]:f[1]])) != key {
			continue
		}
		old := string(data[f[2]:f[3]])
		nv := val
		if strings.HasPrefix(old, `"`) {
			if strings.ContainsAny(val, `"`) {
				return nil, fmt.Errorf("%s: Anführungszeichen sind nicht erlaubt", key)
			}
			nv = `"` + val + `"`
		} else if strings.ContainsAny(val, `,()" `) {
			return nil, fmt.Errorf("%s: ungültiger Wert", key)
		}
		return splice(data, f[2], f[3], nv), nil
	}
	return nil, fmt.Errorf("%s fehlt in OptionSettings", key)
}

// ---------- json ----------

// CanAdd reports whether a missing key could be created in the file: for
// JSON only below objects (a missing third user group stays missing).
func CanAdd(format string, data []byte, key string) bool {
	if format != "json" {
		return CanCreate(format)
	}
	var doc any
	if json.Unmarshal(data, &doc) != nil {
		return false
	}
	cur := doc
	for _, p := range strings.Split(key, ".") {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[p]
			if !ok {
				return true // the rest is created
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(c) {
				return false
			}
			cur = c[i]
		default:
			return false
		}
	}
	return true
}

func jsonGet(doc any, path string) (any, bool) {
	cur := doc
	for _, p := range strings.Split(path, ".") {
		switch c := cur.(type) {
		case map[string]any:
			v, ok := c[p]
			if !ok {
				return nil, false
			}
			cur = v
		case []any:
			i, err := strconv.Atoi(p)
			if err != nil || i < 0 || i >= len(c) {
				return nil, false
			}
			cur = c[i]
		default:
			return nil, false
		}
	}
	return cur, true
}

// setJSON keeps the original key order by decoding into ordered nodes.
func setJSON(data []byte, values map[string]string, keys []string, kinds map[string]string) ([]byte, error) {
	root, err := parseOrdered(data)
	if err != nil {
		return nil, fmt.Errorf("JSON ungültig: %w", err)
	}
	for _, k := range keys {
		v, ok := values[k]
		if !ok {
			continue
		}
		path := strings.Split(k, ".")
		n := root.find(path)
		if n == nil && kinds[k] != "" {
			n = root.create(path, kinds[k])
		}
		if n == nil {
			return nil, fmt.Errorf("%s fehlt in der Datei", k)
		}
		if kinds[k] == "list" {
			if n.kind != 'a' {
				return nil, fmt.Errorf("%s ist keine Liste", k)
			}
			n.vals = nil
			for _, p := range strings.Split(v, ",") {
				if p = strings.TrimSpace(p); p != "" {
					n.vals = append(n.vals, &node{kind: 's', str: p})
				}
			}
			continue
		}
		switch n.kind {
		case 's':
			n.str = v
		case 'n':
			if _, err := strconv.ParseFloat(v, 64); err != nil {
				return nil, fmt.Errorf("%s muss eine Zahl sein", k)
			}
			n.raw = v
		case 'b':
			if v != "true" && v != "false" {
				return nil, fmt.Errorf("%s muss true oder false sein", k)
			}
			n.raw = v
		case 'z':
			n.kind, n.str = 's', v
		default:
			return nil, fmt.Errorf("%s ist kein einfacher Wert", k)
		}
	}
	var b bytes.Buffer
	root.write(&b, "", indentOf(data))
	b.WriteByte('\n')
	return b.Bytes(), nil
}

func indentOf(data []byte) string {
	m := regexp.MustCompile(`\n([ \t]+)"`).FindSubmatch(data)
	if m == nil {
		return "  "
	}
	return string(m[1])
}

// ---------- Arma / DayZ server.cfg ----------

var armaLine = regexp.MustCompile(`^(\s*)([A-Za-z0-9_]+)\s*=\s*(.*?)\s*;(.*)$`)

// findArma looks for a top-level "key = value;" (not inside class blocks).
func findArma(data []byte, key string) (line int, val string, quoted, ok bool) {
	depth := 0
	for i, l := range lines(data) {
		t := strings.TrimSpace(strings.TrimSuffix(l, "\r"))
		if strings.HasPrefix(t, "//") {
			continue
		}
		if depth == 0 {
			if m := armaLine.FindStringSubmatch(strings.TrimSuffix(l, "\r")); m != nil && strings.EqualFold(m[2], key) {
				v := m[3]
				if len(v) >= 2 && v[0] == '"' && v[len(v)-1] == '"' {
					return i, v[1 : len(v)-1], true, true
				}
				return i, v, false, true
			}
		}
		depth += strings.Count(t, "{") - strings.Count(t, "}")
	}
	return -1, "", false, false
}

func setArma(data []byte, key, val string) ([]byte, error) {
	if strings.ContainsAny(val, `"{};`) {
		return nil, fmt.Errorf("%s: Anführungszeichen, Klammern und Semikolon sind nicht erlaubt", key)
	}
	i, _, quoted, ok := findArma(data, key)
	isNum := regexp.MustCompile(`^-?[0-9]+(\.[0-9]+)?$`).MatchString(val)
	nv := val
	if (ok && quoted) || (!ok && !isNum) {
		nv = `"` + val + `"`
	}
	if !ok {
		return appendLine(data, key+" = "+nv+";"), nil
	}
	ls := lines(data)
	cr := strings.HasSuffix(ls[i], "\r")
	l := strings.TrimSuffix(ls[i], "\r")
	m := armaLine.FindStringSubmatchIndex(l)
	ls[i] = l[:m[6]] + nv + l[m[7]:]
	if cr {
		ls[i] += "\r"
	}
	return []byte(strings.Join(ls, "\n")), nil
}
