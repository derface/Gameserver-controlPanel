package manager

// Game settings as a form: the template's start variables plus values in the
// game's own configuration files (see package gameconfig).

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/derface/Gameserver-controlPanel/internal/files"
	"github.com/derface/Gameserver-controlPanel/internal/gameconfig"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

const secretMask = "••••••"

type VarValue struct {
	templates.Var
	Value string `json:"value"`
}

type SettingValue struct {
	templates.Setting
	Value   string `json:"value"`
	Present bool   `json:"present"` // the key is in the file (or can be added)
}

type FileView struct {
	Title    string         `json:"title"`
	File     string         `json:"file"`
	Note     string         `json:"note,omitempty"`
	Exists   bool           `json:"exists"`
	Editable bool           `json:"editable"`
	Error    string         `json:"error,omitempty"`
	Settings []SettingValue `json:"settings"`
}

type SettingsView struct {
	Vars    []VarValue `json:"vars"`
	Files   []FileView `json:"files"`
	Running bool       `json:"running"`
}

func (m *Manager) configRoot(sv store.Server, tpl templates.Template) files.Root {
	uid := containerUID
	if tpl.DataUID > 0 {
		uid = tpl.DataUID
	}
	return files.New(m.ServerDir(sv.ID)).WithOwner(uid)
}

func configRel(sv store.Server, c templates.ConfigFile) string {
	p := c.File
	for k, v := range sv.Vars {
		p = strings.ReplaceAll(p, "{{"+k+"}}", v)
	}
	return filepath.ToSlash(filepath.Clean("/" + p))
}

// readConfig returns the file content, or the init_from content when the
// file does not exist yet. exists reports whether there is anything to edit.
func (m *Manager) readConfig(sv store.Server, tpl templates.Template, c templates.ConfigFile) (data []byte, exists bool, err error) {
	root := m.configRoot(sv, tpl)
	read := func(rel string) ([]byte, bool, error) {
		abs, err := root.Resolve(rel)
		if err != nil {
			if errors.Is(err, fs.ErrNotExist) {
				return nil, false, nil
			}
			return nil, false, err
		}
		b, err := os.ReadFile(abs)
		if errors.Is(err, fs.ErrNotExist) {
			return nil, false, nil
		}
		if err != nil {
			return nil, false, err
		}
		if len(b) > 4<<20 {
			return nil, false, errors.New("Datei ist zu groß")
		}
		return b, true, nil
	}
	data, exists, err = read(configRel(sv, c))
	if err != nil {
		return nil, false, err
	}
	if (!exists || len(strings.TrimSpace(string(data))) == 0) && c.InitFrom != "" {
		if d, ok, err := read(configRel(sv, templates.ConfigFile{File: c.InitFrom})); err == nil && ok {
			return d, true, nil
		}
	}
	return data, exists, nil
}

func uiValue(s templates.Setting, raw string) string {
	switch s.Type {
	case "bool":
		t, _ := s.BoolValues()
		return strconv.FormatBool(strings.EqualFold(raw, t))
	case "select":
		for _, o := range s.Options { // games are not always consistent with case
			if strings.EqualFold(o, raw) {
				return o
			}
		}
	}
	return raw
}

// Settings returns the form for one server.
func (m *Manager) Settings(id string) (SettingsView, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return SettingsView{}, err
	}
	v := SettingsView{Running: m.isUp(id), Vars: []VarValue{}, Files: []FileView{}}
	for _, tv := range tpl.Vars {
		if tpl.Custom && (tv.Key == "app_id" || tv.Key == "ports") {
			continue // fixed when the server was created
		}
		val := sv.Vars[tv.Key]
		if tv.Type == "secret" && val != "" {
			val = secretMask
		}
		v.Vars = append(v.Vars, VarValue{Var: tv, Value: val})
	}
	for _, c := range tpl.Config {
		fv := FileView{Title: c.Title, File: configRel(sv, c), Note: c.Note, Settings: []SettingValue{}}
		data, exists, err := m.readConfig(sv, tpl, c)
		fv.Exists = exists
		fv.Editable = exists || gameconfig.CanCreate(c.Format)
		if err != nil {
			fv.Error, fv.Editable = err.Error(), false
		}
		for _, s := range c.Settings {
			sval := SettingValue{Setting: s}
			if exists && err == nil {
				raw, ok, gerr := gameconfig.Get(c.Format, data, s.Key)
				if gerr != nil {
					fv.Error, fv.Editable = gerr.Error(), false
				}
				sval.Present = ok || gameconfig.CanCreate(c.Format)
				if ok {
					sval.Value = uiValue(s, raw)
				}
			} else {
				sval.Present = fv.Editable
			}
			if s.Type == "secret" && sval.Value != "" {
				sval.Value = secretMask
			}
			fv.Settings = append(fv.Settings, sval)
		}
		v.Files = append(v.Files, fv)
	}
	return v, nil
}

// fileValue checks a value from the form and turns it into the file's spelling.
func fileValue(s templates.Setting, v string) (string, error) {
	v = strings.TrimSpace(v)
	if strings.ContainsAny(v, "\r\n\x00") || len(v) > 300 {
		return "", fmt.Errorf("„%s“: ungültiger Wert", s.Label)
	}
	switch s.Type {
	case "bool":
		t, f := s.BoolValues()
		if v == "true" {
			return t, nil
		}
		if v == "false" {
			return f, nil
		}
		return "", fmt.Errorf("„%s“: ja oder nein", s.Label)
	case "number":
		n, err := strconv.ParseFloat(v, 64)
		if err != nil {
			return "", fmt.Errorf("„%s“ muss eine Zahl sein", s.Label)
		}
		if (s.Min != nil && n < *s.Min) || (s.Max != nil && n > *s.Max) {
			lo, hi := "", ""
			if s.Min != nil {
				lo = strconv.FormatFloat(*s.Min, 'f', -1, 64)
			}
			if s.Max != nil {
				hi = strconv.FormatFloat(*s.Max, 'f', -1, 64)
			}
			return "", fmt.Errorf("„%s“: erlaubt %s bis %s", s.Label, lo, hi)
		}
	case "select":
		ok := false
		for _, o := range s.Options {
			ok = ok || o == v
		}
		if !ok {
			return "", fmt.Errorf("„%s“: ungültige Auswahl", s.Label)
		}
	}
	return v, nil
}

// ApplySettings saves changed values. Config files are only written while
// the game is stopped (many games overwrite them on shutdown), so a running
// server is stopped and started again. Changed start variables recreate the
// container. It returns the labels of what changed.
func (m *Manager) ApplySettings(ctx context.Context, id string, vars map[string]string, vals map[int]map[string]string) ([]string, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return nil, err
	}
	var changed []string

	// start variables
	newVars := map[string]string{}
	for k, v := range sv.Vars {
		newVars[k] = v
	}
	varsChanged := false
	for _, tv := range tpl.Vars {
		v, ok := vars[tv.Key]
		if !ok || (tv.Type == "secret" && v == secretMask) {
			continue
		}
		if tpl.Custom && (tv.Key == "app_id" || tv.Key == "ports") {
			continue
		}
		newVars[tv.Key] = v
	}
	if len(vars) > 0 {
		resolved, err := tpl.ResolveVars(newVars)
		if err != nil {
			return nil, err
		}
		for _, tv := range tpl.Vars {
			if resolved[tv.Key] != sv.Vars[tv.Key] {
				varsChanged = true
				changed = append(changed, tv.Label)
			}
		}
		newVars = resolved
	}

	// config files
	type write struct {
		rel  string
		data []byte
	}
	var writes []write
	for i, c := range tpl.Config {
		in := vals[i]
		if len(in) == 0 {
			continue
		}
		data, exists, err := m.readConfig(sv, tpl, c)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Title, err)
		}
		if !exists && !gameconfig.CanCreate(c.Format) {
			return nil, fmt.Errorf("%s gibt es noch nicht. Starte den Server einmal, dann entsteht die Datei", c.Title)
		}
		set, keys := map[string]string{}, []string{}
		for _, s := range c.Settings {
			v, ok := in[s.Key]
			if !ok || (s.Type == "secret" && v == secretMask) {
				continue
			}
			fv, err := fileValue(s, v)
			if err != nil {
				return nil, err
			}
			cur, present, _ := gameconfig.Get(c.Format, data, s.Key)
			if present && cur == fv {
				continue
			}
			if !present && fv == "" && s.Type != "bool" {
				continue
			}
			set[s.Key], keys = fv, append(keys, s.Key)
			changed = append(changed, s.Label)
		}
		if len(keys) == 0 {
			continue
		}
		out, err := gameconfig.Set(c.Format, data, set, keys)
		if err != nil {
			return nil, fmt.Errorf("%s: %w", c.Title, err)
		}
		writes = append(writes, write{configRel(sv, c), out})
	}
	if len(changed) == 0 {
		return nil, nil
	}

	up := m.isUp(id)
	if len(writes) > 0 && up {
		if err := m.Stop(ctx, id); err != nil {
			return nil, fmt.Errorf("Server ließ sich nicht stoppen: %w", err)
		}
	}
	root := m.configRoot(sv, tpl)
	uid := containerUID
	if tpl.DataUID > 0 {
		uid = tpl.DataUID
	}
	for _, w := range writes {
		if err := writeConfig(root, m.ServerDir(id), uid, w.rel, w.data); err != nil {
			if up {
				_ = m.Start(ctx, id)
			}
			return nil, err
		}
	}
	if varsChanged {
		if err := m.st.ModifyServer(id, func(cur *store.Server) error { cur.Vars = newVars; return nil }); err != nil {
			return nil, err
		}
		if sv.ContainerID != "" {
			m.Recreate(id, up)
		}
	} else if len(writes) > 0 && up {
		if err := m.Start(ctx, id); err != nil {
			return changed, err
		}
	}
	return changed, nil
}

func writeConfig(root files.Root, serverDir string, uid int, rel string, data []byte) error {
	// the folder may not exist before the first start (properties, ini, cvar);
	// create it owned by the game's user so the game can write there too
	base := filepath.Clean(serverDir)
	if err := os.MkdirAll(base, 0o755); err != nil {
		return err
	}
	cur := base
	for _, part := range strings.Split(strings.Trim(filepath.Dir(filepath.FromSlash(rel)), string(os.PathSeparator)), string(os.PathSeparator)) {
		if part == "" || part == "." || part == ".." {
			continue
		}
		cur = filepath.Join(cur, part)
		if _, err := os.Lstat(cur); errors.Is(err, fs.ErrNotExist) {
			if err := os.Mkdir(cur, 0o755); err != nil {
				return err
			}
			_ = os.Chown(cur, uid, uid)
		}
	}
	if len(data) > files.MaxEditSize {
		return errors.New("Konfigurationsdatei ist zu groß")
	}
	return root.WriteText(rel, string(data))
}
