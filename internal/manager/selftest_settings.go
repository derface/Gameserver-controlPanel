package manager

// Self-test of the settings page: after a game ran, its config files are
// read, every value must survive being written back unchanged, and a changed
// server name must still let the game start (and stay in the file).

import (
	"bytes"
	"context"
	"fmt"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/gameconfig"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

var nameKeys = map[string]bool{"servername": true, "hostname": true, "name": true, "server_name": true, "sessionname": true,
	"server-name": true, "motd": false, "gamename": true, "serverdisplayname": true, "publicname": true}

// settingsTest fills res.Settings ("ok", "fehler", "keine"), SettingsCount and SettingsNote.
func (m *Manager) settingsTest(ctx context.Context, id string, tpl templates.Template, wait time.Duration, res *TestResult) {
	m.testStep("Einstellungen werden geprüft")
	sv, ok := m.st.Server(id)
	if !ok {
		return
	}
	files := m.configFiles(sv, tpl)
	if len(files) == 0 {
		res.Settings, res.SettingsNote = "keine", "Keine Konfigurationsdatei bekannt"
		return
	}
	missing := 0
	for _, f := range files {
		if !f.exists {
			missing++
		}
	}
	if missing > 0 { // many games write their config only when they stop
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_ = m.Stop(sctx, id)
		cancel()
		files = m.configFiles(sv, tpl)
		defer func() {
			sctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			if !m.isUp(id) {
				_ = m.Start(sctx, id)
			}
		}()
	}
	var problems []string
	count := 0
	var nameFile = -1
	var nameKey, nameVal string
	for i, f := range files {
		if f.err != nil {
			problems = append(problems, f.c.Title+": "+f.err.Error())
			continue
		}
		if !f.exists {
			continue
		}
		count += len(f.c.Settings)
		entries, err := gameconfig.List(f.c.Format, f.data)
		if err != nil {
			problems = append(problems, f.c.Title+": "+err.Error())
			continue
		}
		for _, e := range entries {
			out, err := gameconfig.SetKinds(f.c.Format, f.data, map[string]string{e.Key: e.Value}, []string{e.Key}, map[string]string{e.Key: e.Kind})
			if err != nil {
				problems = append(problems, fmt.Sprintf("%s: %s lässt sich nicht schreiben (%v)", f.c.Title, e.Key, err))
				break
			}
			if f.c.Format != "json" && !bytes.Equal(out, f.data) {
				problems = append(problems, fmt.Sprintf("%s: %s verändert beim Zurückschreiben die Datei", f.c.Title, e.Key))
				break
			}
		}
		if nameFile < 0 {
			for _, s := range f.c.Settings {
				if (s.Type == "" || s.Type == "text") && nameKeys[strings.ToLower(lastPart(s.Key))] {
					if v, ok, _ := gameconfig.Get(f.c.Format, f.data, s.Key); ok && v != "" && !strings.ContainsAny(v, `"\`) {
						nameFile, nameKey, nameVal = i, s.Key, v
						break
					}
				}
			}
		}
	}
	res.SettingsCount = count
	if count == 0 && len(problems) == 0 {
		res.Settings, res.SettingsNote = "keine", "Das Spiel hat keine der bekannten Dateien angelegt"
		return
	}
	if len(problems) > 0 {
		res.Settings, res.SettingsNote = "fehler", strings.Join(problems, "; ")
		return
	}
	if nameFile < 0 {
		res.Settings, res.SettingsNote = "ok", fmt.Sprintf("%d Einstellungen lesbar (kein Servername zum Ändern gefunden)", count)
		return
	}
	// change the name, restart, check the game comes back and keeps it
	m.testStep("Einstellungen: Servername wird geändert, Neustart")
	newName := nameVal + " GP"
	if len(newName) > 60 {
		newName = "Panel Test GP"
	}
	actx, cancel := context.WithTimeout(ctx, 5*time.Minute)
	_, err := m.ApplySettings(actx, id, nil, map[int]map[string]string{nameFile: {nameKey: newName}})
	cancel()
	if err != nil {
		res.Settings, res.SettingsNote = "fehler", "Speichern ging nicht: "+err.Error()
		return
	}
	if !m.isUp(id) {
		sctx, cancel := context.WithTimeout(ctx, 2*time.Minute)
		_ = m.Start(sctx, id)
		cancel()
	}
	since := time.Now()
	deadline := since.Add(wait + 10*time.Minute) // the start also checks for updates
	for {
		select {
		case <-ctx.Done():
			return
		case <-time.After(testTick):
		}
		cur, ok := m.st.Server(id)
		if !ok {
			return
		}
		in := m.Info(cur)
		if in.Status == "error" || (in.Status == "stopped" && time.Since(since) > 45*time.Second) {
			res.Settings, res.SettingsNote = "fehler", "Nach geändertem Servernamen startet das Spiel nicht mehr: "+in.Message
			return
		}
		if in.Status == "running" && time.Since(since) > 10*time.Second {
			break
		}
		if time.Now().After(deadline) {
			res.Settings, res.SettingsNote = "fehler", "Nach geändertem Servernamen kommt das Spiel nicht mehr hoch"
			return
		}
	}
	time.Sleep(testSettle)
	files = m.configFiles(sv, tpl)
	if nameFile < len(files) {
		if v, _, _ := gameconfig.Get(files[nameFile].c.Format, files[nameFile].data, nameKey); v != newName {
			res.Settings, res.SettingsNote = "fehler", fmt.Sprintf("Das Spiel überschreibt den geänderten Namen (%s steht wieder auf %q)", nameKey, v)
			return
		}
	}
	res.Settings, res.SettingsNote = "ok", fmt.Sprintf("%d Einstellungen, Namensänderung übernommen", count)
}
