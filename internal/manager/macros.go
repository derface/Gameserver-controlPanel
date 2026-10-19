package manager

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"
	"unicode/utf8"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Console macros: saved sequences of commands, run one after the other.

var reWait = regexp.MustCompile(`(?i)^(warte|wait|sleep)\s+(\d{1,3})\s*s?$`)

// macroWait returns the pause of a "warte 10" line.
func macroWait(line string) (time.Duration, bool) {
	m := reWait.FindStringSubmatch(strings.TrimSpace(line))
	if m == nil {
		return 0, false
	}
	n, _ := strconv.Atoi(m[2])
	return time.Duration(n) * time.Second, true
}

// ValidateMacros checks and cleans the macros of a server.
func ValidateMacros(ms []store.Macro) ([]store.Macro, error) {
	if len(ms) > 30 {
		return nil, errors.New("Höchstens 30 Makros je Server")
	}
	out := make([]store.Macro, 0, len(ms))
	names := map[string]bool{}
	for _, m := range ms {
		m.Name = strings.TrimSpace(m.Name)
		if m.Name == "" || utf8.RuneCountInString(m.Name) > 40 {
			return nil, errors.New("Jedes Makro braucht einen Namen (bis 40 Zeichen)")
		}
		if names[strings.ToLower(m.Name)] {
			return nil, fmt.Errorf("Den Namen „%s“ gibt es zweimal", m.Name)
		}
		names[strings.ToLower(m.Name)] = true
		var lines []string
		var wait time.Duration
		cmds := 0
		for _, l := range m.Lines {
			for _, part := range strings.Split(strings.ReplaceAll(l, "\r", ""), "\n") {
				part = strings.TrimSpace(part)
				if part == "" {
					continue
				}
				if len(part) > 500 {
					return nil, fmt.Errorf("„%s“: eine Zeile ist zu lang (höchstens 500 Zeichen)", m.Name)
				}
				if d, ok := macroWait(part); ok {
					if d < time.Second || d > 10*time.Minute {
						return nil, fmt.Errorf("„%s“: Pausen dürfen 1 bis 600 Sekunden lang sein", m.Name)
					}
					wait += d
				} else if !strings.HasPrefix(part, "#") {
					cmds++
				}
				lines = append(lines, part)
			}
		}
		if cmds == 0 {
			return nil, fmt.Errorf("„%s“ enthält keinen Befehl", m.Name)
		}
		if len(lines) > 50 {
			return nil, fmt.Errorf("„%s“: höchstens 50 Zeilen", m.Name)
		}
		if wait > 30*time.Minute {
			return nil, fmt.Errorf("„%s“: alle Pausen zusammen höchstens 30 Minuten", m.Name)
		}
		if m.ID == "" {
			m.ID = newID()
		}
		m.Lines = lines
		out = append(out, m)
	}
	return out, nil
}

// SetMacros stores the macros of a server.
func (m *Manager) SetMacros(id string, ms []store.Macro) ([]store.Macro, error) {
	ms, err := ValidateMacros(ms)
	if err != nil {
		return nil, err
	}
	err = m.st.ModifyServer(id, func(sv *store.Server) error { sv.Macros = ms; return nil })
	return ms, err
}

var macroRuns sync.Map // server id + macro id → running

// FindMacro finds a macro by id or name.
func FindMacro(sv store.Server, key string) (store.Macro, bool) {
	for _, mc := range sv.Macros {
		if mc.ID == key || strings.EqualFold(mc.Name, key) {
			return mc, true
		}
	}
	return store.Macro{}, false
}

// RunMacro runs a macro in the background. by appears in the history.
func (m *Manager) RunMacro(id, key, by string) error {
	sv, ok := m.st.Server(id)
	if !ok {
		return errors.New("Server nicht gefunden")
	}
	mc, ok := FindMacro(sv, key)
	if !ok {
		return errors.New("Dieses Makro gibt es nicht (mehr)")
	}
	if !m.isUp(id) {
		return errors.New("Der Server läuft nicht")
	}
	if _, busy := macroRuns.LoadOrStore(id+"|"+mc.ID, true); busy {
		return fmt.Errorf("„%s“ läuft gerade schon", mc.Name)
	}
	go func() {
		defer macroRuns.Delete(id + "|" + mc.ID)
		ctx, cancel := context.WithTimeout(context.Background(), 35*time.Minute)
		defer cancel()
		_ = m.runMacro(ctx, id, mc, by)
	}()
	return nil
}

func (m *Manager) runMacro(ctx context.Context, id string, mc store.Macro, by string) error {
	n := 0
	for _, l := range mc.Lines {
		if d, ok := macroWait(l); ok {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d):
			}
			continue
		}
		if strings.HasPrefix(l, "#") {
			continue
		}
		if _, err := m.Command(ctx, id, l); err != nil {
			m.Event(id, "error", fmt.Sprintf("Makro „%s“ abgebrochen bei „%s“: %v", mc.Name, l, err))
			return err
		}
		n++
		time.Sleep(300 * time.Millisecond) // many consoles drop lines sent too fast
	}
	who := ""
	if by != "" {
		who = " (" + by + ")"
	}
	m.EventKind(id, "info", "task", fmt.Sprintf("Makro „%s“ ausgeführt%s: %d Befehle", mc.Name, who, n))
	return nil
}
