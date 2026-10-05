package manager

import (
	"context"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// TestWatchdogDocker needs Docker and the fake runtime (testdata/fake/README.md).
func TestWatchdogDocker(t *testing.T) {
	if os.Getenv("GP_DOCKER_TEST") == "" {
		t.Skip("GP_DOCKER_TEST nicht gesetzt")
	}
	hangUnit = 5 * time.Second
	dir := t.TempDir()
	st, _ := store.Open(dir)
	tpls, err := templates.Load("testdata/fake")
	if err != nil {
		t.Fatal(err)
	}
	m := New(st, docker.New("/var/run/docker.sock"), tpls, dir)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	defer func() {
		for _, sv := range st.Servers() {
			_ = m.Delete(context.Background(), sv.ID, true)
		}
	}()

	wait := func(what string, d time.Duration, ok func() bool) {
		t.Helper()
		end := time.Now().Add(d)
		for !ok() {
			if time.Now().After(end) {
				t.Fatalf("Zeitüberschreitung: %s", what)
			}
			time.Sleep(time.Second)
		}
	}
	hasEvent := func(id, part string) bool {
		for _, e := range m.Events(id) {
			if strings.Contains(e.Text, part) {
				return true
			}
		}
		return false
	}

	// 1. crash loop: halted after three crashes
	crash, err := m.Create(CreateReq{Template: "fake-crash", Name: "Crash", Start: true})
	if err != nil {
		t.Fatal(err)
	}
	wait("Absturzschleife angehalten", 3*time.Minute, func() bool {
		cur, _ := st.Server(crash.ID)
		return m.Info(cur).Status == "error" && hasEvent(crash.ID, "Abstürze in")
	})
	time.Sleep(8 * time.Second) // Docker must not start it again
	cur, _ := st.Server(crash.ID)
	if in := m.Info(cur); in.Status != "error" || !strings.Contains(in.Message, "angehalten") {
		t.Fatalf("nach dem Anhalten: %s %s", in.Status, in.Message)
	}
	n := 0
	for _, e := range m.Events(crash.ID) {
		if strings.Contains(e.Text, "unerwartet beendet") || strings.Contains(e.Text, "abgestürzt") {
			n++
		}
	}
	if n < 3 {
		t.Fatalf("nur %d Absturz-Ereignisse: %+v", n, m.Events(crash.ID))
	}

	// 2. a normal start/stop by the user is not a crash
	ok, err := m.Create(CreateReq{Template: "fake-ok", Name: "Ok", Start: true})
	if err != nil {
		t.Fatal(err)
	}
	wait("ok läuft", 2*time.Minute, func() bool { c, _ := st.Server(ok.ID); return m.Info(c).Status == "running" })
	if err := m.Restart(ctx, ok.ID); err != nil {
		t.Fatal(err)
	}
	wait("ok läuft wieder", 2*time.Minute, func() bool { c, _ := st.Server(ok.ID); return m.Info(c).Status == "running" })
	if err := m.Stop(ctx, ok.ID); err != nil {
		t.Fatal(err)
	}
	time.Sleep(5 * time.Second)
	for _, e := range m.Events(ok.ID) {
		if e.Level != "info" {
			t.Fatalf("Neustart/Stopp als Absturz gezählt: %+v", e)
		}
	}

	// 3. hang: answers the query, then not any more → restart
	hang, err := m.Create(CreateReq{Template: "fake-hang", Name: "Hang", Start: true})
	if err != nil {
		t.Fatal(err)
	}
	if err := m.SetWatch(ctx, hang.ID, store.Watch{HangRestart: true, HangMinutes: 2}); err != nil {
		t.Fatal(err)
	}
	wait("Hänger erkannt", 3*time.Minute, func() bool { return hasEvent(hang.ID, "antwortet seit") })
	wait("nach Hänger wieder gestartet", 2*time.Minute, func() bool { c, _ := st.Server(hang.ID); return m.Info(c).Status == "running" })
	for _, e := range m.Events(hang.ID) {
		if strings.Contains(e.Text, "unerwartet") {
			t.Fatalf("Neustart nach Hänger als Absturz gezählt: %+v", m.Events(hang.ID))
		}
	}

	// 4. a task run by hand + update
	_ = m.SetTasks(ok.ID, []store.Task{{Action: "command", At: "04:00", Command: "say hallo", Enabled: true}})
	c, _ := st.Server(ok.ID)
	if err := m.Start(ctx, ok.ID); err != nil {
		t.Fatal(err)
	}
	wait("ok läuft für Aufgabe", 2*time.Minute, func() bool { c, _ := st.Server(ok.ID); return m.Info(c).Status == "running" })
	if err := m.RunTaskByID(ok.ID, c.Tasks[0].ID); err != nil {
		t.Fatal(err)
	}
	wait("Aufgabe ausgeführt", 30*time.Second, func() bool { return hasEvent(ok.ID, "Aufgabe Befehl") })
	msg, err := m.UpdateServer(ctx, ok.ID)
	if err != nil || !strings.Contains(msg, "SteamCMD") {
		t.Fatalf("Update: %q %v", msg, err)
	}
	if _, err := os.Stat(m.ServerDir(ok.ID) + "/server/.gp-installed"); err == nil {
		t.Fatal("Installationsmarke nicht entfernt")
	}
	wait("nach Update läuft", 2*time.Minute, func() bool { c, _ := st.Server(ok.ID); return m.Info(c).Status == "running" })
	for _, e := range m.Events(ok.ID) {
		if e.Level == "warn" || e.Level == "error" {
			t.Fatalf("Update als Absturz gezählt: %+v", m.Events(ok.ID))
		}
	}
	for _, e := range m.Events(hang.ID) {
		t.Logf("hang: %s %s", e.Level, e.Text)
	}
	for _, e := range m.Events(crash.ID) {
		t.Logf("crash: %s %s", e.Level, e.Text)
	}
}
