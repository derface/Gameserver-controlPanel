package manager

// Before the settings page writes config files, their old content is kept
// (last 5 saves per server, in <data>/settings-backup/<id>/). One click
// brings a save back.

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
	"time"
)

const keepSettingsBackups = 5

// SettingsBackup is one kept state of the config files.
type SettingsBackup struct {
	ID      string    `json:"id"`
	When    time.Time `json:"when"`
	Changed []string  `json:"changed"` // what the save after it changed
	Files   []string  `json:"files"`
}

type backupFile struct {
	Rel     string `json:"rel"`
	Existed bool   `json:"existed"`
	Content string `json:"content,omitempty"`
}

type backupRecord struct {
	SettingsBackup
	Data []backupFile `json:"data"`
}

func (m *Manager) settingsBackupDir(id string) string {
	return filepath.Join(m.dataDir, "settings-backup", id)
}

// keepSettings saves the current content of the files that are about to change.
func (m *Manager) keepSettings(id string, rels []string, changed []string) error {
	sv, tpl, err := m.get(id)
	if err != nil {
		return err
	}
	root := m.configRoot(sv, tpl)
	rec := backupRecord{SettingsBackup: SettingsBackup{ID: time.Now().UTC().Format("20060102-150405.000"), When: time.Now(), Changed: changed}}
	for _, rel := range rels {
		bf := backupFile{Rel: rel}
		if txt, err := root.ReadText(rel); err == nil {
			bf.Existed, bf.Content = true, txt
		}
		rec.Data = append(rec.Data, bf)
		rec.Files = append(rec.Files, rel)
	}
	dir := m.settingsBackupDir(id)
	if err := os.MkdirAll(dir, 0o700); err != nil {
		return err
	}
	b, err := json.Marshal(rec)
	if err != nil {
		return err
	}
	if err := os.WriteFile(filepath.Join(dir, rec.ID+".json"), b, 0o600); err != nil {
		return err
	}
	// only the newest few
	list, _ := m.SettingsBackups(id)
	for _, old := range list[min(len(list), keepSettingsBackups):] {
		_ = os.Remove(filepath.Join(dir, old.ID+".json"))
	}
	return nil
}

// SettingsBackups lists the kept states, newest first.
func (m *Manager) SettingsBackups(id string) ([]SettingsBackup, error) {
	if _, ok := m.st.Server(id); !ok {
		return nil, errors.New("Server nicht gefunden")
	}
	ents, _ := os.ReadDir(m.settingsBackupDir(id))
	out := []SettingsBackup{}
	for _, e := range ents {
		if !strings.HasSuffix(e.Name(), ".json") {
			continue
		}
		rec, err := m.readSettingsBackup(id, strings.TrimSuffix(e.Name(), ".json"))
		if err == nil {
			out = append(out, rec.SettingsBackup)
		}
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID > out[j].ID })
	return out, nil
}

func (m *Manager) readSettingsBackup(id, bid string) (backupRecord, error) {
	var rec backupRecord
	if bid == "" || strings.ContainsAny(bid, `/\`) || strings.Contains(bid, "..") {
		return rec, errors.New("Unbekannter Stand")
	}
	b, err := os.ReadFile(filepath.Join(m.settingsBackupDir(id), bid+".json"))
	if err != nil {
		return rec, errors.New("Unbekannter Stand")
	}
	if err := json.Unmarshal(b, &rec); err != nil {
		return rec, err
	}
	return rec, nil
}

// RestoreSettings writes a kept state back. A running server is stopped and
// started again. The state before the restore is kept too, so it can be undone.
func (m *Manager) RestoreSettings(ctx context.Context, id, bid string) error {
	sv, tpl, err := m.get(id)
	if err != nil {
		return err
	}
	rec, err := m.readSettingsBackup(id, bid)
	if err != nil {
		return err
	}
	if err := m.keepSettings(id, rec.Files, []string{"Wiederherstellung vom " + rec.When.Local().Format("02.01. 15:04")}); err != nil {
		return err
	}
	up := m.isUp(id)
	if up {
		if err := m.Stop(ctx, id); err != nil {
			return fmt.Errorf("Server ließ sich nicht stoppen: %w", err)
		}
	}
	root := m.configRoot(sv, tpl)
	uid := containerUID
	if tpl.DataUID > 0 {
		uid = tpl.DataUID
	}
	var werr error
	for _, f := range rec.Data {
		if f.Existed {
			if err := writeConfig(root, m.ServerDir(id), uid, f.Rel, []byte(f.Content)); err != nil && werr == nil {
				werr = err
			}
		} else if err := root.Delete(f.Rel); err != nil && !errors.Is(err, os.ErrNotExist) && werr == nil {
			werr = err
		}
	}
	if up {
		if err := m.Start(ctx, id); err != nil && werr == nil {
			werr = err
		}
	}
	return werr
}
