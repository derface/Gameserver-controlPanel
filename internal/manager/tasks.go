package manager

// Scheduled tasks (restart, update, command, backup, start, stop) with an
// optional warning to the players, plus "update now".

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

var taskActions = map[string]string{
	"restart": "Neustart",
	"update":  "Update",
	"command": "Befehl",
	"backup":  "Backup",
	"start":   "Starten",
	"stop":    "Stoppen",
}

// TaskLabel is the German name of an action.
func TaskLabel(a string) string { return taskActions[a] }

var hhmm = regexp.MustCompile(`^([01][0-9]|2[0-3]):([0-5][0-9])$`)

// missedAfter: a run that is this late (panel was off) is skipped, so a
// 4 a.m. restart does not happen at noon.
const missedAfter = 30 * time.Minute

// ValidateTask checks and normalises a task.
func ValidateTask(t *store.Task, tpl templates.Template) error {
	if _, ok := taskActions[t.Action]; !ok {
		return errors.New("Unbekannte Aktion")
	}
	t.Command = strings.TrimSpace(strings.NewReplacer("\n", " ", "\r", " ").Replace(t.Command))
	if t.Action == "command" {
		if t.Command == "" {
			return errors.New("Der Befehl fehlt")
		}
		if len(t.Command) > 500 {
			return errors.New("Der Befehl ist zu lang")
		}
	} else {
		t.Command = ""
	}
	if t.EveryHours > 0 {
		if t.EveryHours > 168 {
			return errors.New("Höchstens alle 168 Stunden")
		}
		t.At, t.Days = "", nil
	} else {
		if !hhmm.MatchString(t.At) {
			return errors.New("Uhrzeit bitte als HH:MM angeben")
		}
		seen := map[int]bool{}
		var days []int
		for _, d := range t.Days {
			if d < 0 || d > 6 {
				return errors.New("Ungültiger Wochentag")
			}
			if !seen[d] {
				seen[d] = true
				days = append(days, d)
			}
		}
		sort.Ints(days)
		if len(days) == 7 {
			days = nil
		}
		t.Days = days
	}
	switch t.WarnMin {
	case 0, 1, 5, 10, 15, 30:
	default:
		return errors.New("Vorwarnung: 0, 1, 5, 10, 15 oder 30 Minuten")
	}
	if t.Action != "restart" && t.Action != "update" && t.Action != "stop" {
		t.WarnMin = 0
	}
	if t.Action == "update" && tpl.Kind != "steam" && tpl.Image == "" {
		return errors.New("Dieses Spiel lässt sich nicht automatisch aktualisieren")
	}
	return nil
}

// nextRun returns the first scheduled time after "after".
func nextRun(t store.Task, after time.Time) time.Time {
	if t.EveryHours > 0 {
		base := t.LastRun
		if base.IsZero() {
			base = t.Created
		}
		next := base.Add(time.Duration(t.EveryHours) * time.Hour)
		for !next.After(after) {
			next = next.Add(time.Duration(t.EveryHours) * time.Hour)
		}
		return next
	}
	m := hhmm.FindStringSubmatch(t.At)
	if m == nil {
		return time.Time{}
	}
	h, _ := strconv.Atoi(m[1])
	mi, _ := strconv.Atoi(m[2])
	allowed := func(wd time.Weekday) bool {
		if len(t.Days) == 0 {
			return true
		}
		for _, d := range t.Days {
			if time.Weekday(d) == wd {
				return true
			}
		}
		return false
	}
	day := time.Date(after.Year(), after.Month(), after.Day(), 0, 0, 0, 0, after.Location())
	for i := 0; i < 8; i++ {
		c := time.Date(day.Year(), day.Month(), day.Day()+i, h, mi, 0, 0, after.Location())
		if c.After(after) && allowed(c.Weekday()) {
			return c
		}
	}
	return time.Time{}
}

// NextRun is the next time a task will run (zero when disabled).
func NextRun(t store.Task) time.Time {
	if !t.Enabled {
		return time.Time{}
	}
	from := t.LastRun
	if from.IsZero() {
		from = t.Created
	}
	return nextRun(t, from)
}

// runTasks checks every 20 s which tasks are due (including their warning).
func (m *Manager) runTasks(ctx context.Context) {
	tick := time.NewTicker(20 * time.Second)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-tick.C:
		}
		m.checkTasks(ctx, time.Now())
	}
}

func (m *Manager) checkTasks(ctx context.Context, now time.Time) {
	for _, sv := range m.st.Servers() {
		for _, t := range sv.Tasks {
			if !t.Enabled {
				continue
			}
			sched := NextRun(t)
			if sched.IsZero() {
				continue
			}
			if now.Sub(sched) > missedAfter {
				// the panel was off: skip, but remember so it is not retried
				m.finishTask(sv.ID, t.ID, sched, "verpasst (Panel war aus)")
				m.Event(sv.ID, "warn", fmt.Sprintf("Aufgabe „%s“ um %s verpasst, das Panel lief nicht", TaskLabel(t.Action), sched.Format("02.01. 15:04")))
				continue
			}
			if now.Before(sched.Add(-time.Duration(t.WarnMin) * time.Minute)) {
				continue
			}
			key := sv.ID + "/" + t.ID
			m.mu.Lock()
			if m.pendingTasks == nil {
				m.pendingTasks = map[string]bool{}
			}
			busy := m.pendingTasks[key]
			m.pendingTasks[key] = true
			m.mu.Unlock()
			if busy {
				continue
			}
			go func(id string, t store.Task, sched time.Time) {
				defer func() {
					m.mu.Lock()
					delete(m.pendingTasks, key)
					m.mu.Unlock()
				}()
				m.warnAndRun(ctx, id, t, sched)
			}(sv.ID, t, sched)
		}
	}
}

// warnAndRun tells the players what is coming, then runs the task at sched.
func (m *Manager) warnAndRun(ctx context.Context, id string, t store.Task, sched time.Time) {
	if t.WarnMin > 0 && m.isUp(id) {
		for _, left := range []int{30, 15, 10, 5, 1} {
			if left > t.WarnMin {
				continue
			}
			at := sched.Add(-time.Duration(left) * time.Minute)
			if !sleepUntil(ctx, at) {
				return
			}
			m.Broadcast(id, warnText(t.Action, left))
		}
	}
	if !sleepUntil(ctx, sched) {
		return
	}
	// the task may have been changed or removed while we waited
	cur, ok := m.st.Server(id)
	if !ok {
		return
	}
	still := false
	for _, x := range cur.Tasks {
		still = still || (x.ID == t.ID && x.Enabled && x.Action == t.Action && x.Command == t.Command)
	}
	if !still {
		return
	}
	res := m.RunTaskNow(ctx, id, t)
	m.finishTask(id, t.ID, sched, res)
}

func sleepUntil(ctx context.Context, at time.Time) bool {
	d := time.Until(at)
	if d <= 0 {
		return ctx.Err() == nil
	}
	select {
	case <-ctx.Done():
		return false
	case <-time.After(d):
		return true
	}
}

func warnText(action string, min int) string {
	what := map[string]string{"restart": "neu gestartet", "update": "für ein Update neu gestartet", "stop": "gestoppt"}[action]
	unit := "Minuten"
	if min == 1 {
		unit = "Minute"
	}
	return fmt.Sprintf("Der Server wird in %d %s %s.", min, unit, what)
}

func (m *Manager) finishTask(id, taskID string, ran time.Time, result string) {
	_ = m.st.ModifyServer(id, func(sv *store.Server) error {
		for i := range sv.Tasks {
			if sv.Tasks[i].ID == taskID {
				sv.Tasks[i].LastRun = ran
				sv.Tasks[i].LastResult = result
			}
		}
		return nil
	})
}

// RunTaskNow runs a task's action immediately and returns a short result.
func (m *Manager) RunTaskNow(ctx context.Context, id string, t store.Task) string {
	actx, cancel := context.WithTimeout(ctx, time.Hour)
	defer cancel()
	label := TaskLabel(t.Action)
	var (
		out string
		err error
	)
	switch t.Action {
	case "restart":
		m.expect(id)
		err = m.Restart(actx, id)
	case "start":
		m.expect(id)
		err = m.Start(actx, id)
	case "stop":
		m.expect(id)
		err = m.Stop(actx, id)
	case "command":
		out, err = m.Command(actx, id, t.Command)
		label += " „" + t.Command + "“"
	case "backup":
		if !m.AutoBackups() {
			err = errors.New("Geplante Backups gehören nicht zu deiner Edition")
			break
		}
		var b BackupInfo
		b, err = m.CreateBackup(actx, id, KindAuto)
		out = b.Name
	case "update":
		out, err = m.UpdateServer(actx, id)
	default:
		err = errors.New("Unbekannte Aktion")
	}
	if err != nil {
		m.Event(id, "error", "Aufgabe "+label+" fehlgeschlagen: "+err.Error())
		return "Fehler: " + err.Error()
	}
	msg := "Aufgabe " + label + " ausgeführt"
	if out != "" && t.Action != "command" {
		msg += ": " + out
	}
	m.Event(id, "info", msg)
	if out == "" {
		out = "ok"
	}
	if len(out) > 200 {
		out = out[:200] + " …"
	}
	return out
}

// isUp reports whether the game is running (or starting).
func (m *Manager) isUp(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	l := m.live[id]
	return l != nil && (l.Phase == "running" || l.Phase == "starting")
}

var unsafeMsg = regexp.MustCompile(`["'\\;$` + "`" + `\n\r]`)

// Broadcast shows a message to the players, if the game has a way to do so.
func (m *Manager) Broadcast(id, msg string) bool {
	sv, tpl, err := m.get(id)
	if err != nil || tpl.Broadcast == "" || sv.ContainerID == "" {
		return false
	}
	cmd := strings.ReplaceAll(tpl.Broadcast, "{{msg}}", unsafeMsg.ReplaceAllString(msg, ""))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	_, err = m.Command(ctx, id, cmd)
	return err == nil
}

// UpdateServer installs the newest game version. Steam: SteamCMD runs on the
// next start (forced); image servers pull the image and are recreated.
func (m *Manager) UpdateServer(ctx context.Context, id string) (string, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return "", err
	}
	up := m.isUp(id)
	m.Event(id, "info", "Update wird gestartet")
	switch {
	case tpl.Kind == "steam":
		_ = os.Remove(filepath.Join(m.ServerDir(id), "server", ".gp-installed"))
		if !up {
			return "wird beim nächsten Start installiert", nil
		}
		m.expect(id)
		if sv.ContainerID == "" {
			return "", m.Start(ctx, id)
		}
		return "Neustart mit SteamCMD-Update", m.Restart(ctx, id)
	case tpl.Image != "":
		m.setJob(id, "Neue Version wird geladen: "+tpl.Image)
		err := m.dk.Pull(ctx, tpl.Image, func(string) {})
		m.clearJob(id)
		if err != nil {
			return "", err
		}
		m.expect(id)
		m.Recreate(id, up)
		return "Image aktualisiert, Container wird neu angelegt", nil
	}
	return "", errors.New("Dieses Spiel lässt sich nicht automatisch aktualisieren")
}

// TaskView is what the "Aufgaben" tab shows.
type TaskView struct {
	Tasks        []TaskInfo  `json:"tasks"`
	Watch        store.Watch `json:"watch"`
	CanBroadcast bool        `json:"can_broadcast"`
	CanQuery     bool        `json:"can_query"`
	CanUpdate    bool        `json:"can_update"`
	Events       []Event     `json:"events"`
}

type TaskInfo struct {
	store.Task
	Label   string    `json:"label"`
	NextRun time.Time `json:"next_run,omitempty"`
}

func (m *Manager) Tasks(id string) (TaskView, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return TaskView{}, err
	}
	v := TaskView{Watch: sv.Watch, CanBroadcast: tpl.Broadcast != "", CanQuery: tpl.Query != nil && tpl.Query.Type != "",
		CanUpdate: tpl.Kind == "steam" || tpl.Image != "", Events: m.Events(id), Tasks: []TaskInfo{}}
	for _, t := range sv.Tasks {
		v.Tasks = append(v.Tasks, TaskInfo{Task: t, Label: TaskLabel(t.Action), NextRun: NextRun(t)})
	}
	return v, nil
}

const maxTasks = 20

// SetTasks replaces the task list; run history of kept tasks survives.
func (m *Manager) SetTasks(id string, in []store.Task) error {
	_, tpl, err := m.get(id)
	if err != nil {
		return err
	}
	if len(in) > maxTasks {
		return fmt.Errorf("Höchstens %d Aufgaben pro Server", maxTasks)
	}
	for i := range in {
		if err := ValidateTask(&in[i], tpl); err != nil {
			return fmt.Errorf("Aufgabe %d: %w", i+1, err)
		}
	}
	return m.st.ModifyServer(id, func(sv *store.Server) error {
		old := map[string]store.Task{}
		for _, t := range sv.Tasks {
			old[t.ID] = t
		}
		out := make([]store.Task, 0, len(in))
		for _, t := range in {
			if o, ok := old[t.ID]; ok && t.ID != "" {
				t.Created, t.LastRun, t.LastResult = o.Created, o.LastRun, o.LastResult
				if o.At != t.At || o.EveryHours != t.EveryHours || !sameDays(o.Days, t.Days) || (!o.Enabled && t.Enabled) {
					// new schedule: count from now, not from the old last run
					t.Created, t.LastRun = time.Now(), time.Time{}
				}
			} else {
				t.ID, t.Created, t.LastRun, t.LastResult = newID(), time.Now(), time.Time{}, ""
			}
			out = append(out, t)
		}
		sv.Tasks = out
		return nil
	})
}

func sameDays(a, b []int) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

// SetWatch stores the watchdog settings and applies the restart policy.
func (m *Manager) SetWatch(ctx context.Context, id string, w store.Watch) error {
	if w.HangMinutes != 0 && (w.HangMinutes < 2 || w.HangMinutes > 60) {
		return errors.New("Wartezeit: 2 bis 60 Minuten")
	}
	var cid string
	err := m.st.ModifyServer(id, func(sv *store.Server) error {
		sv.Watch = w
		cid = sv.ContainerID
		return nil
	})
	if err != nil || cid == "" {
		return err
	}
	sv, _ := m.st.Server(id)
	return m.dk.SetRestartPolicy(ctx, cid, restartPolicy(sv))
}

// RunTaskByID runs one task now, in the background.
func (m *Manager) RunTaskByID(id, taskID string) error {
	sv, ok := m.st.Server(id)
	if !ok {
		return errors.New("Server nicht gefunden")
	}
	for _, t := range sv.Tasks {
		if t.ID == taskID {
			go func(t store.Task) {
				res := m.RunTaskNow(context.Background(), id, t)
				_ = m.st.ModifyServer(id, func(sv *store.Server) error {
					for i := range sv.Tasks {
						if sv.Tasks[i].ID == t.ID {
							sv.Tasks[i].LastResult = res + " (von Hand)"
						}
					}
					return nil
				})
			}(t)
			return nil
		}
	}
	return errors.New("Aufgabe nicht gefunden")
}
