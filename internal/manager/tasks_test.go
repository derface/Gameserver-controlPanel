package manager

import (
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

func TestNextRun(t *testing.T) {
	loc := time.Local
	at := func(d, h, m int) time.Time { return time.Date(2026, 10, d, h, m, 0, 0, loc) } // 5.10.2026 = Montag
	daily := store.Task{At: "04:00", Enabled: true}
	if got := nextRun(daily, at(5, 3, 0)); !got.Equal(at(5, 4, 0)) {
		t.Fatalf("täglich vor 4 Uhr: %v", got)
	}
	if got := nextRun(daily, at(5, 4, 0)); !got.Equal(at(6, 4, 0)) {
		t.Fatalf("täglich genau 4 Uhr gelaufen: %v", got)
	}
	// nur Samstag (6) und Sonntag (0)
	we := store.Task{At: "20:30", Days: []int{0, 6}}
	if got := nextRun(we, at(5, 12, 0)); !got.Equal(at(10, 20, 30)) {
		t.Fatalf("Wochenende: %v", got)
	}
	if got := nextRun(we, at(10, 21, 0)); !got.Equal(at(11, 20, 30)) {
		t.Fatalf("Wochenende Sonntag: %v", got)
	}
	ev := store.Task{EveryHours: 6, Created: at(5, 1, 0)}
	if got := nextRun(ev, at(5, 1, 0)); !got.Equal(at(5, 7, 0)) {
		t.Fatalf("alle 6 h: %v", got)
	}
	ev.LastRun = at(5, 7, 0)
	if got := NextRun(store.Task{EveryHours: 6, LastRun: at(5, 7, 0), Enabled: true}); !got.Equal(at(5, 13, 0)) {
		t.Fatalf("alle 6 h nach Lauf: %v", got)
	}
	// new task created after today's time: first run is tomorrow
	if got := NextRun(store.Task{At: "04:00", Created: at(5, 9, 0), Enabled: true}); !got.Equal(at(6, 4, 0)) {
		t.Fatalf("neu angelegt: %v", got)
	}
	if !NextRun(store.Task{At: "04:00", Created: at(5, 9, 0)}).IsZero() {
		t.Fatal("ausgeschaltete Aufgabe hat einen nächsten Lauf")
	}
}

func TestValidateTask(t *testing.T) {
	steam := templates.Template{Kind: "steam"}
	ok := []store.Task{
		{Action: "restart", At: "04:00", WarnMin: 5},
		{Action: "command", At: "12:00", Command: "say hallo\nwelt"},
		{Action: "backup", EveryHours: 6, At: "egal"},
		{Action: "update", At: "05:30", Days: []int{1, 1, 3}},
	}
	for i := range ok {
		if err := ValidateTask(&ok[i], steam); err != nil {
			t.Fatalf("%d: %v", i, err)
		}
	}
	if ok[1].Command != "say hallo welt" || ok[2].At != "" || len(ok[3].Days) != 2 {
		t.Fatalf("nicht normalisiert: %+v", ok)
	}
	bad := []store.Task{
		{Action: "rm -rf", At: "04:00"},
		{Action: "restart", At: "25:00"},
		{Action: "command", At: "04:00"},
		{Action: "restart", At: "04:00", WarnMin: 7},
		{Action: "restart", At: "04:00", Days: []int{9}},
		{Action: "restart", EveryHours: 500},
	}
	for i := range bad {
		if ValidateTask(&bad[i], steam) == nil {
			t.Fatalf("ungültige Aufgabe %d angenommen", i)
		}
	}
	if ValidateTask(&store.Task{Action: "update", At: "04:00"}, templates.Template{Kind: "image"}) == nil {
		t.Fatal("Update ohne Image angenommen")
	}
	w := store.Task{Action: "command", At: "04:00", Command: "x", WarnMin: 5}
	_ = ValidateTask(&w, steam)
	if w.WarnMin != 0 {
		t.Fatal("Vorwarnung bei Befehl nicht entfernt")
	}
}

func TestMissedTaskIsSkipped(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	tpls, _ := templates.Load("")
	m := New(st, docker.New("/nonexistent.sock"), tpls, dir)
	created := time.Now().Add(-48 * time.Hour)
	_ = st.SaveServer(store.Server{ID: "s1", Name: "x", Template: "valheim", Tasks: []store.Task{
		{ID: "t1", Action: "restart", At: "04:00", Enabled: true, Created: created},
	}})
	m.checkTasks(t.Context(), time.Now())
	sv, _ := st.Server("s1")
	if sv.Tasks[0].LastRun.IsZero() || sv.Tasks[0].LastResult == "" {
		t.Fatalf("verpasste Aufgabe nicht vermerkt: %+v", sv.Tasks[0])
	}
	if evs := m.Events("s1"); len(evs) == 0 || evs[0].Level != "warn" {
		t.Fatalf("kein Ereignis: %+v", evs)
	}
}

func TestSetTasksKeepsHistory(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	tpls, _ := templates.Load("")
	m := New(st, docker.New("/nonexistent.sock"), tpls, dir)
	_ = st.SaveServer(store.Server{ID: "s1", Name: "x", Template: "valheim"})
	if err := m.SetTasks("s1", []store.Task{{Action: "restart", At: "04:00", Enabled: true}}); err != nil {
		t.Fatal(err)
	}
	sv, _ := st.Server("s1")
	id := sv.Tasks[0].ID
	ran := time.Now().Add(-time.Hour)
	_ = st.ModifyServer("s1", func(s *store.Server) error { s.Tasks[0].LastRun = ran; return nil })
	// same schedule, other warning: history stays
	_ = m.SetTasks("s1", []store.Task{{ID: id, Action: "restart", At: "04:00", WarnMin: 5, Enabled: true}})
	sv, _ = st.Server("s1")
	if !sv.Tasks[0].LastRun.Equal(ran) {
		t.Fatal("Verlauf verloren")
	}
	// new time: counts from now
	_ = m.SetTasks("s1", []store.Task{{ID: id, Action: "restart", At: "05:00", Enabled: true}})
	sv, _ = st.Server("s1")
	if !sv.Tasks[0].LastRun.IsZero() {
		t.Fatal("neuer Zeitplan zählt nicht neu")
	}
	if m.SetTasks("s1", []store.Task{{Action: "restart", At: "x"}}) == nil {
		t.Fatal("ungültige Aufgabe gespeichert")
	}
}

func TestCrashReason(t *testing.T) {
	run := func(started string, running bool, code int, oom bool) *live {
		return &live{State: docker.State{StartedAt: started, Running: running, ExitCode: code, OOMKilled: oom}}
	}
	cases := []struct {
		prev, now *live
		crash     bool
	}{
		{run("a", true, 0, false), run("b", true, 0, false), true}, // Docker restarted it
		{run("a", true, 0, false), run("a", false, 139, false), true},
		{run("a", true, 0, false), run("a", false, 137, true), true}, // OOM
		{run("a", true, 0, false), run("a", false, 0, false), false}, // clean exit
		{run("a", true, 0, false), run("a", false, 143, false), false},
		{run("a", false, 0, false), run("b", true, 0, false), false}, // started
	}
	for i, c := range cases {
		if got := crashReason(c.prev, c.now) != ""; got != c.crash {
			t.Errorf("Fall %d: Absturz=%v, erwartet %v", i, got, c.crash)
		}
	}
}

func TestWarnText(t *testing.T) {
	if warnText("restart", 1) != "Der Server wird in 1 Minute neu gestartet." {
		t.Fatal(warnText("restart", 1))
	}
	if unsafeMsg.ReplaceAllString(`a"b;c$d`, "") != "abcd" {
		t.Fatal("Nachricht nicht bereinigt")
	}
}

func TestTaskConditionsValidate(t *testing.T) {
	tpls, _ := templates.Load("")
	vh, _ := tpls.Get("valheim")
	mc, _ := tpls.Get("minecraft-java")
	tk := store.Task{Action: "restart", At: "04:00", WhenEmpty: true, OnlyIfUpdate: true, BackupFirst: true}
	if err := ValidateTask(&tk, vh); err != nil || tk.MaxWaitMin != 120 {
		t.Fatalf("%v %+v", err, tk)
	}
	tk = store.Task{Action: "backup", At: "04:00", WhenEmpty: true, OnlyIfUpdate: true, BackupFirst: true}
	if err := ValidateTask(&tk, vh); err != nil || tk.WhenEmpty || tk.OnlyIfUpdate || tk.BackupFirst {
		t.Fatalf("Bedingungen bei Backup nicht entfernt: %v %+v", err, tk)
	}
	tk = store.Task{Action: "update", At: "04:00", OnlyIfUpdate: true}
	if ValidateTask(&tk, mc) != nil {
		t.Fatal("Update-Prüfung (Mods) bei Minecraft abgelehnt")
	}
	tr, _ := tpls.Get("terraria")
	tk = store.Task{Action: "update", At: "04:00", OnlyIfUpdate: true}
	if tr.ID != "" && tr.Kind != "steam" && ValidateTask(&tk, tr) == nil {
		t.Fatal("Update-Prüfung bei Spiel ohne Steam und Mods angenommen")
	}
	tk = store.Task{Action: "restart", At: "04:00", WhenEmpty: true, MaxWaitMin: 7}
	if ValidateTask(&tk, vh) == nil {
		t.Fatal("ungültige Wartezeit angenommen")
	}
}

func TestTaskWaitsUntilEmpty(t *testing.T) {
	dir := t.TempDir()
	st, _ := store.Open(dir)
	tpls, _ := templates.Load("")
	m := New(st, docker.New("/nonexistent.sock"), tpls, dir)
	old := taskPoll
	taskPoll = 20 * time.Millisecond
	defer func() { taskPoll = old }()
	tk := store.Task{ID: "t1", Action: "stop", At: "04:00", Enabled: true, WhenEmpty: true, MaxWaitMin: 30}
	_ = st.SaveServer(store.Server{ID: "s1", Name: "x", Template: "valheim", Tasks: []store.Task{tk}})
	m.mu.Lock()
	m.live["s1"] = &live{Phase: "running", Players: 2}
	m.mu.Unlock()
	go func() {
		time.Sleep(120 * time.Millisecond)
		m.mu.Lock()
		m.live["s1"].Players = 0
		m.mu.Unlock()
	}()
	start := time.Now()
	m.runConditional(t.Context(), "s1", tk, time.Now())
	if time.Since(start) < 100*time.Millisecond {
		t.Fatal("hat nicht gewartet")
	}
	sv, _ := st.Server("s1")
	if !strings.Contains(sv.Tasks[0].LastResult, "Server leer") {
		t.Fatalf("Ergebnis: %q", sv.Tasks[0].LastResult)
	}
}
