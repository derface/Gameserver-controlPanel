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

// TestSelfTestDocker runs the self-test against fake templates. It needs a
// Docker daemon and a fake gamepanel/steam-runtime image (see
// testdata/fake/README.md), so it only runs with GP_DOCKER_TEST=1.
func TestSelfTestDocker(t *testing.T) {
	if os.Getenv("GP_DOCKER_TEST") == "" {
		t.Skip("GP_DOCKER_TEST nicht gesetzt")
	}
	testTick, testQueryWait, testSettle, testSettleRaw, testStartWait = time.Second, 8*time.Second, 3*time.Second, 5*time.Second, 25*time.Second

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	tpls, err := templates.Load("testdata/fake")
	if err != nil {
		t.Fatal(err)
	}
	m := New(st, docker.New("/var/run/docker.sock"), tpls, dir)
	if err := st.SaveServer(store.Server{ID: "eigener", Name: "Eigener", Template: "fake-ok", CreatedBy: "manual"}); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	go m.Run(ctx)
	time.Sleep(200 * time.Millisecond)

	ids := []string{"fake-ok", "fake-noquery", "fake-crash", "fake-noready", "fake-installfail", "fake-bare"}
	if err := m.StartTest(ids); err != nil {
		t.Fatal(err)
	}
	if err := m.StartTest(ids); err == nil {
		t.Fatal("zweiter Lauf parallel erlaubt")
	}
	deadline := time.Now().Add(5 * time.Minute)
	for m.TestStatus().Running {
		if time.Now().After(deadline) {
			t.Fatalf("Selbsttest hängt: %+v", m.TestStatus())
		}
		time.Sleep(time.Second)
	}
	want := map[string]string{"fake-ok": "ok", "fake-noquery": "unsicher", "fake-crash": "fehler", "fake-noready": "fehler", "fake-installfail": "fehler", "fake-bare": "unsicher"}
	r := m.TestStatus()
	if len(r.Results) != len(want) {
		t.Fatalf("%d Ergebnisse: %+v", len(r.Results), r.Results)
	}
	for _, x := range r.Results {
		t.Logf("%-17s %-9s dl=%v start=%v ready=%v query=%v %s %s | %s", x.Template, x.Result, x.Download, x.Start, x.Ready, x.Query, x.Players, x.Error, strings.Join(x.Log, " / "))
		if want[x.Template] != x.Result {
			t.Errorf("%s: %s, erwartet %s (%s)", x.Template, x.Result, want[x.Template], x.Error)
		}
	}
	for _, sv := range st.Servers() {
		if sv.CreatedBy == testCreator {
			t.Errorf("Testserver %s nicht aufgeräumt", sv.ID)
		}
	}
	if len(st.Servers()) != 1 {
		t.Errorf("eigener Server fehlt oder zu viele: %d", len(st.Servers()))
	}
	rep := m.TestReport()
	if !strings.Contains(rep, "| Fake ok | fake-ok | ok |") || !strings.Contains(rep, "## Fake crash") {
		t.Errorf("Bericht unvollständig:\n%s", rep)
	}

	// abort: the running test server is removed, the template stays queued
	testQueryWait = time.Minute
	if err := m.StartTest([]string{"fake-noquery", "fake-ok"}); err != nil {
		t.Fatal(err)
	}
	for m.TestStatus().ServerID == "" {
		time.Sleep(200 * time.Millisecond)
	}
	time.Sleep(3 * time.Second)
	m.StopTest()
	for m.TestStatus().Running {
		time.Sleep(200 * time.Millisecond)
	}
	r = m.TestStatus()
	if r.Stopped == "" || len(r.Queue) != 2 {
		t.Errorf("Abbruch: stopped=%q queue=%v", r.Stopped, r.Queue)
	}
	for _, sv := range st.Servers() {
		if sv.CreatedBy == testCreator {
			t.Errorf("nach Abbruch: Testserver %s übrig", sv.ID)
		}
	}
	// earlier results of other templates survive a new run
	if len(r.Results) != 4 {
		t.Errorf("alte Ergebnisse: %d, erwartet 4", len(r.Results))
	}
}
