package manager

// Selbsttest: installs each chosen template once, checks that it downloads,
// starts, reports ready and answers the player query, then deletes it again.
// One game at a time, so disk and bandwidth stay predictable. The state is
// saved after every step, so a panel restart resumes the run.

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/query"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

const testCreator = "selbsttest"

// timings; tests shorten them
var (
	testTick      = 4 * time.Second
	testQueryWait = 4 * time.Minute
	testSettle    = 30 * time.Second // after the ready line
	testSettleRaw = 90 * time.Second // when there is no ready line
	testStartWait = 12 * time.Minute
	testWineWait  = 25 * time.Minute
)

type TestResult struct {
	Template string    `json:"template"`
	Name     string    `json:"name"`
	Source   string    `json:"source"` // stabil | beta | experimentell (template status at test time)
	Result   string    `json:"result"` // ok | unsicher | fehler | übersprungen
	Download bool      `json:"download"`
	Start    bool      `json:"start"`
	Ready    bool      `json:"ready"`
	Query    bool      `json:"query"`
	HasReady bool      `json:"has_ready"`
	HasQuery bool      `json:"has_query"`
	Players  string    `json:"players,omitempty"`
	Note     string    `json:"note,omitempty"`
	Error    string    `json:"error,omitempty"`
	Log      []string  `json:"log,omitempty"`
	DiskMB   int64     `json:"disk_mb"`
	Seconds  int       `json:"seconds"`
	Finished time.Time `json:"finished"`
}

type TestRun struct {
	Running   bool         `json:"running"`
	Started   time.Time    `json:"started"`
	Finished  time.Time    `json:"finished,omitempty"`
	Total     int          `json:"total"`
	Queue     []string     `json:"queue"`
	Current   string       `json:"current,omitempty"`
	ServerID  string       `json:"server_id,omitempty"`
	Step      string       `json:"step,omitempty"`
	StepSince time.Time    `json:"step_since,omitempty"`
	Stopped   string       `json:"stopped,omitempty"` // why the run ended early
	Results   []TestResult `json:"results"`
	FreeGB    float64      `json:"free_gb"`
}

func (m *Manager) testFile() string { return filepath.Join(m.dataDir, "selbsttest.json") }

func (m *Manager) loadTest() *TestRun {
	r := &TestRun{}
	if b, err := os.ReadFile(m.testFile()); err == nil {
		_ = json.Unmarshal(b, r)
	}
	return r
}

// saveTest writes the run; callers hold testMu.
func (m *Manager) saveTest() {
	b, _ := json.MarshalIndent(m.test, "", "  ")
	tmp := m.testFile() + ".tmp"
	if os.WriteFile(tmp, b, 0o600) == nil {
		_ = os.Rename(tmp, m.testFile())
	}
}

func (m *Manager) testUpdate(fn func(r *TestRun)) {
	m.testMu.Lock()
	defer m.testMu.Unlock()
	if m.test == nil {
		m.test = m.loadTest()
	}
	fn(m.test)
	m.saveTest()
}

// TestStatus returns a copy of the current or last run.
func (m *Manager) TestStatus() TestRun {
	m.testMu.Lock()
	defer m.testMu.Unlock()
	if m.test == nil {
		m.test = m.loadTest()
	}
	r := *m.test
	r.Queue = append([]string{}, r.Queue...)
	r.Results = []TestResult{}
	for _, x := range m.test.Results {
		if _, ok := m.tpls.Get(x.Template); ok { // templates removed by an update drop out
			r.Results = append(r.Results, x)
		}
	}
	r.FreeGB = freeGB(m.dataDir)
	return r
}

// TestSelection turns a choice from the UI into template ids.
func (m *Manager) TestSelection(which string, ids []string) []string {
	last := map[string]string{}
	for _, r := range m.TestStatus().Results {
		last[r.Template] = r.Result
	}
	var out []string
	for _, t := range m.tpls.All() {
		if t.Custom {
			continue
		}
		ok := false
		switch which {
		case "alle":
			ok = true
		case "nicht-stabil":
			ok = t.Status != "stabil"
		case "community":
			ok = strings.HasPrefix(t.ID, "lgsm-")
		case "offen":
			ok = last[t.ID] != "ok" && t.Status != "stabil"
		case "auswahl":
			for _, id := range ids {
				ok = ok || id == t.ID
			}
		}
		if ok {
			out = append(out, t.ID)
		}
	}
	return out
}

// StartTest begins a run over the given templates, small downloads first.
func (m *Manager) StartTest(ids []string) error {
	if m.ctx() == nil {
		return errors.New("Panel startet noch")
	}
	var tpls []templates.Template
	for _, id := range ids {
		if t, ok := m.tpls.Get(id); ok && !t.Custom {
			tpls = append(tpls, t)
		}
	}
	if len(tpls) == 0 {
		return errors.New("Keine Vorlagen ausgewählt")
	}
	if freeGB(m.dataDir) < 15 {
		return fmt.Errorf("Nur %.0f GB frei. Für den Selbsttest werden mindestens 15 GB gebraucht", freeGB(m.dataDir))
	}
	size := func(t templates.Template) int {
		if t.DiskGB == 0 {
			return 5
		}
		return t.DiskGB
	}
	sort.SliceStable(tpls, func(i, j int) bool { return size(tpls[i]) < size(tpls[j]) })
	var err error
	m.testUpdate(func(r *TestRun) {
		if r.Running {
			err = errors.New("Es läuft schon ein Selbsttest")
			return
		}
		keep := r.Results[:0]
		chosen := map[string]bool{}
		for _, t := range tpls {
			chosen[t.ID] = true
		}
		for _, res := range r.Results {
			if !chosen[res.Template] {
				keep = append(keep, res)
			}
		}
		*r = TestRun{Running: true, Started: time.Now(), Total: len(tpls), Results: keep}
		for _, t := range tpls {
			r.Queue = append(r.Queue, t.ID)
		}
	})
	if err != nil {
		return err
	}
	m.launchTest()
	return nil
}

func (m *Manager) launchTest() {
	ctx, cancel := context.WithCancel(m.ctx())
	m.testMu.Lock()
	m.testCancel = cancel
	m.testMu.Unlock()
	go m.runTests(ctx)
}

// StopTest aborts the run after cleaning up the current server.
func (m *Manager) StopTest() {
	m.testMu.Lock()
	c := m.testCancel
	m.testMu.Unlock()
	if c != nil {
		c()
	}
}

// resumeTest is called once at startup.
func (m *Manager) resumeTest() {
	r := m.TestStatus()
	m.cleanupTestServers("")
	if !r.Running {
		return
	}
	m.testUpdate(func(r *TestRun) {
		if r.Current != "" { // interrupted mid-test: try it again
			r.Queue = append([]string{r.Current}, r.Queue...)
			r.Current, r.ServerID = "", ""
		}
	})
	m.launchTest()
}

func (m *Manager) cleanupTestServers(except string) {
	for _, sv := range m.st.Servers() {
		if sv.CreatedBy == testCreator && sv.ID != except {
			ctx, cancel := context.WithTimeout(context.Background(), time.Minute)
			_ = m.Delete(ctx, sv.ID, true)
			cancel()
		}
	}
}

func (m *Manager) runTests(ctx context.Context) {
	defer func() {
		m.testMu.Lock()
		m.testCancel = nil
		m.testMu.Unlock()
	}()
	for {
		var id string
		m.testUpdate(func(r *TestRun) {
			if len(r.Queue) > 0 {
				id, r.Queue = r.Queue[0], r.Queue[1:]
				r.Current, r.Step, r.StepSince = id, "Wird angelegt", time.Now()
			}
		})
		if id == "" {
			m.finishTest("")
			return
		}
		tpl, ok := m.tpls.Get(id)
		if !ok {
			continue
		}
		res := m.testOne(ctx, tpl)
		if ctx.Err() != nil && m.ctx().Err() != nil {
			return // panel shuts down: keep the run marked as running and resume later
		}
		if ctx.Err() != nil {
			m.testUpdate(func(r *TestRun) { r.Queue = append([]string{id}, r.Queue...) })
			m.finishTest("Vom Benutzer abgebrochen")
			return
		}
		m.testUpdate(func(r *TestRun) {
			r.Results = append(r.Results, res)
			r.Current, r.ServerID, r.Step = "", "", ""
		})
		if free := freeGB(m.dataDir); free < 10 {
			m.finishTest(fmt.Sprintf("Angehalten: nur noch %.0f GB frei", free))
			return
		}
	}
}

func (m *Manager) finishTest(why string) {
	m.testUpdate(func(r *TestRun) {
		r.Running, r.Finished, r.Stopped = false, time.Now(), why
		r.Current, r.ServerID, r.Step = "", "", ""
	})
}

func (m *Manager) testStep(step string) {
	m.testUpdate(func(r *TestRun) {
		if r.Step != step {
			r.Step, r.StepSince = step, time.Now()
		}
	})
}

func (m *Manager) testOne(ctx context.Context, tpl templates.Template) (res TestResult) {
	t0 := time.Now()
	res = TestResult{Template: tpl.ID, Name: tpl.Name, Source: tpl.Status, HasReady: tpl.Ready != "", HasQuery: tpl.Query != nil && tpl.Query.Type != ""}
	defer func() {
		res.Seconds = int(time.Since(t0).Seconds())
		res.Finished = time.Now()
	}()

	need := tpl.DiskGB
	if need == 0 {
		need = 10
	}
	if free := freeGB(m.dataDir); free < float64(need)*1.2+5 {
		res.Result, res.Error = "übersprungen", fmt.Sprintf("Zu wenig Platz: %.0f GB frei, etwa %d GB nötig", free, need)
		return
	}
	if total := memTotalMB(); total > 0 && tpl.MinMemoryMB > 0 && float64(tpl.MinMemoryMB) > total {
		res.Result, res.Error = "übersprungen", fmt.Sprintf("Braucht mindestens %d MB RAM, der Rechner hat %.0f MB", tpl.MinMemoryMB, total)
		return
	}

	if tpl.SteamLogin && m.steamUser() == "" {
		res.Result, res.Error = "übersprungen", "Braucht ein Steam-Konto (Einstellungen → Steam-Konto)"
		return
	}
	vars := map[string]string{}
	for _, v := range tpl.Vars {
		if v.Default == "" && v.Needed { // optional tokens (GSLT) stay empty: a fake one breaks the start
			vars[v.Key] = "gpTest4711"
		}
	}
	name := "Test " + tpl.Name
	if r := []rune(name); len(r) > 40 {
		name = string(r[:40])
	}
	sv, err := m.Create(CreateReq{Template: tpl.ID, Name: name, Vars: vars, Start: true, CreatedBy: testCreator, internal: true})
	if err != nil {
		res.Result, res.Error = "fehler", err.Error()
		if strings.Contains(err.Error(), "Ports") {
			res.Result = "übersprungen"
		}
		return
	}
	m.testUpdate(func(r *TestRun) { r.ServerID = sv.ID })
	defer func() {
		res.DiskMB = dirSizeMB(m.ServerDir(sv.ID))
		full := m.testLog(sv.ID, 3000)
		for _, l := range full {
			if strings.Contains(l, "[panel] Start:") {
				res.Download = true
			}
		}
		if res.Result != "ok" || len(res.Log) == 0 {
			res.Log = lastRun(full, 40)
		}
		m.testStep("Wird gelöscht")
		dctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
		defer cancel()
		if cur, ok := m.st.Server(sv.ID); ok && cur.ContainerID != "" {
			_ = m.dk.Stop(dctx, cur.ContainerID, 10)
		}
		_ = m.Delete(dctx, sv.ID, true)
	}()

	wine := tpl.Runtime == "wine"
	var (
		prepDeadline  = t0.Add(30 * time.Minute)
		dlDeadline    = t0.Add(150 * time.Minute)
		startDeadline time.Time
		readyAt       time.Time
		firstStart    string
	)
	startWait := testStartWait
	if wine {
		startWait = testWineWait
	}
	fail := func(msg string) TestResult {
		res.Result, res.Error = "fehler", msg
		return res
	}
	tick := time.NewTicker(testTick)
	defer tick.Stop()
	for {
		select {
		case <-ctx.Done():
			res.Result, res.Error = "fehler", "abgebrochen"
			return
		case <-tick.C:
		}
		cur, ok := m.st.Server(sv.ID)
		if !ok {
			return fail("Testserver ist verschwunden")
		}
		in := m.Info(cur)
		m.mu.RLock()
		var started string
		if l := m.live[sv.ID]; l != nil {
			started = l.State.StartedAt
		}
		m.mu.RUnlock()
		now := time.Now()

		switch in.Status {
		case "error":
			return fail(in.Message)
		case "preparing":
			m.testStep(in.Message)
			if now.After(prepDeadline) {
				return fail("Vorbereitung dauert länger als 30 Minuten: " + in.Message)
			}
			continue
		case "stopped":
			if firstStart == "" && now.Sub(t0) < 20*time.Second {
				continue
			}
			msg := "Server hat sich beendet"
			if in.Message != "" {
				msg += ": " + in.Message
			}
			return fail(msg)
		}
		if tpl.Kind != "steam" {
			res.Download = true
		}
		if started != "" {
			if firstStart == "" {
				firstStart = started
			} else if started != firstStart {
				return fail("Server ist abgestürzt und wurde von Docker neu gestartet")
			}
		}
		switch in.Status {
		case "installing":
			m.testStep(fmt.Sprintf("Download %.0f %%", in.Progress*100))
			if now.After(dlDeadline) {
				return fail("Download dauert länger als 2,5 Stunden")
			}
			continue
		case "starting":
			res.Download = true
			if msg := m.restartLoop(sv.ID); msg != "" {
				return fail(msg)
			}
			if startDeadline.IsZero() {
				startDeadline = now.Add(startWait)
			}
			m.testStep("Startet")
			if now.After(startDeadline) {
				if tpl.Ready != "" {
					return fail(fmt.Sprintf("Keine Bereit-Meldung nach %s (gesucht: %s)", humanDur(startWait), tpl.Ready))
				}
				return fail("Server kommt nicht hoch")
			}
			continue
		case "running":
		default:
			continue
		}

		if msg := m.restartLoop(sv.ID); msg != "" {
			return fail(msg)
		}
		res.Download, res.Start = true, true
		res.Ready = tpl.Ready != ""
		if readyAt.IsZero() {
			readyAt = now
		}
		// without a ready line the process is merely alive; give it time
		settle := testSettle
		if tpl.Ready == "" {
			settle = testSettleRaw
		}
		if res.HasQuery && !res.Query {
			m.testStep("Läuft, Abfrage wird geprüft")
			if r, err := m.queryOnce(cur, tpl); err == nil {
				res.Query = true
				res.Players = fmt.Sprintf("%d/%d", r.Players, r.Max)
			} else if now.Sub(readyAt) < testQueryWait {
				continue
			}
		} else {
			m.testStep("Läuft, Stabilität wird geprüft")
		}
		if now.Sub(readyAt) < settle {
			continue
		}
		switch {
		case res.HasQuery && !res.Query:
			res.Result, res.Note = "unsicher", "Läuft, antwortet aber nicht auf die Spielerabfrage. Abfrage-Port oder Abfrageart prüfen."
		case !res.HasReady && !res.HasQuery:
			res.Result, res.Note = "unsicher", "Prozess läuft stabil. Ohne Bereit-Meldung und Abfrage ist mehr nicht prüfbar."
		default:
			res.Result = "ok"
		}
		res.Log = m.testLog(sv.ID, 12)
		return res
	}
}

func (m *Manager) queryOnce(sv store.Server, tpl templates.Template) (query.Result, error) {
	if tpl.Query == nil || tpl.Query.Port < 0 || tpl.Query.Port >= len(sv.Ports) {
		return query.Result{}, errors.New("keine Abfrage")
	}
	port := sv.Ports[tpl.Query.Port].Host
	return query.Ask(tpl.Query.Type, "127.0.0.1", port)
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;?]*[ -/]*[@-~]|\x1b[()][A-Z0-9]|\r`)

// testLog returns the last n meaningful log lines; SteamCMD progress spam is dropped.
func (m *Manager) testLog(id string, n int) []string {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	rc, err := m.logs(ctx, id, false, max(400, n*2), 0)
	if err != nil {
		return nil
	}
	defer rc.Close()
	var lines []string
	sc := bufio.NewScanner(io.LimitReader(rc, 4<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		l := strings.TrimSpace(ansiRe.ReplaceAllString(sc.Text(), ""))
		if l == "" || strings.Contains(l, "Update state (0x") || strings.Contains(l, "Thread RCON Client") {
			continue
		}
		if len(l) > 300 {
			l = l[:300] + " …"
		}
		lines = append(lines, l)
	}
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}

func freeGB(dir string) float64 {
	var s syscall.Statfs_t
	if err := syscall.Statfs(dir, &s); err != nil {
		return 0
	}
	return float64(s.Bavail) * float64(s.Bsize) / (1 << 30)
}

func memTotalMB() float64 {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		if f := strings.Fields(l); len(f) >= 2 && f[0] == "MemTotal:" {
			v, _ := strconv.ParseFloat(f[1], 64)
			return v / 1024
		}
	}
	return 0
}

func dirSizeMB(dir string) int64 {
	var n int64
	_ = filepath.WalkDir(dir, func(_ string, d fs.DirEntry, err error) error {
		if err == nil && d.Type().IsRegular() {
			if fi, err := d.Info(); err == nil {
				n += fi.Size()
			}
		}
		return nil
	})
	return n >> 20
}

// TestReport renders the results as Markdown, for sharing or pasting.
func (m *Manager) TestReport() string {
	r := m.TestStatus()
	var b strings.Builder
	count := map[string]int{}
	for _, x := range r.Results {
		count[x.Result]++
	}
	fmt.Fprintf(&b, "# Selbsttest\n\nGestartet %s", r.Started.Format("02.01.2006 15:04"))
	if !r.Finished.IsZero() {
		fmt.Fprintf(&b, ", beendet %s", r.Finished.Format("02.01.2006 15:04"))
	}
	if r.Stopped != "" {
		fmt.Fprintf(&b, " (%s)", r.Stopped)
	}
	fmt.Fprintf(&b, "\n\nok: %d · unsicher: %d · fehler: %d · übersprungen: %d · offen: %d\n\n",
		count["ok"], count["unsicher"], count["fehler"], count["übersprungen"], len(r.Queue))
	b.WriteString("| Spiel | Vorlage | Ergebnis | Download | Start | Bereit | Abfrage | Größe | Dauer |\n|---|---|---|---|---|---|---|---|---|\n")
	mark := func(ok, has bool) string {
		switch {
		case !has:
			return "–"
		case ok:
			return "✓"
		}
		return "✗"
	}
	res := append([]TestResult(nil), r.Results...)
	order := map[string]int{"fehler": 0, "unsicher": 1, "übersprungen": 2, "ok": 3}
	sort.SliceStable(res, func(i, j int) bool { return order[res[i].Result] < order[res[j].Result] })
	for _, x := range res {
		q := mark(x.Query, x.HasQuery)
		if x.Players != "" {
			q += " " + x.Players
		}
		fmt.Fprintf(&b, "| %s | %s | %s | %s | %s | %s | %s | %.1f GB | %d min |\n", x.Name, x.Template, x.Result,
			mark(x.Download, true), mark(x.Start, true), mark(x.Ready, x.HasReady), q, float64(x.DiskMB)/1024, (x.Seconds+59)/60)
	}
	for _, x := range res {
		if x.Result == "ok" {
			continue
		}
		fmt.Fprintf(&b, "\n## %s (%s): %s\n\n", x.Name, x.Template, x.Result)
		if x.Error != "" {
			fmt.Fprintf(&b, "Fehler: %s\n\n", x.Error)
		}
		if x.Note != "" {
			fmt.Fprintf(&b, "%s\n\n", x.Note)
		}
		if len(x.Log) > 0 {
			b.WriteString("```\n" + strings.Join(x.Log, "\n") + "\n```\n")
		}
	}
	return b.String()
}

func (m *Manager) ctx() context.Context {
	m.testMu.Lock()
	defer m.testMu.Unlock()
	return m.baseCtx
}

func humanDur(d time.Duration) string {
	if d >= time.Minute {
		return fmt.Sprintf("%d Minuten", int(d.Minutes()))
	}
	return fmt.Sprintf("%d Sekunden", int(d.Seconds()))
}

// crash markers of start scripts that restart the game themselves
var loopMarkers = []string{"Server restart in ", "Server Failed", "Segmentation fault", "FATAL ERROR"}

// restartLoop reports a crash that the game's own start script swallowed.
func (m *Manager) restartLoop(id string) string {
	lines := m.testLog(id, 300)
	started := false
	for _, l := range lines {
		if strings.Contains(l, "[panel] Start:") {
			started = true
			continue
		}
		if !started {
			continue
		}
		for _, mk := range loopMarkers {
			if strings.Contains(l, mk) {
				return "Server stürzt nach dem Start ab: " + strings.TrimSpace(l)
			}
		}
	}
	return ""
}

// lastRun keeps the lines of the run that crashed: when Docker restarted the
// container, the log ends with a fresh SteamCMD run that hides the cause.
func lastRun(lines []string, n int) []string {
	lastStart := -1
	for i, l := range lines {
		if strings.Contains(l, "[panel] Start:") {
			lastStart = i
		}
	}
	end := len(lines)
	if lastStart >= 0 {
		for i := lastStart + 1; i < len(lines); i++ {
			if strings.Contains(lines[i], "[panel] SteamCMD: App") {
				end = i
				break
			}
		}
	}
	lines = lines[:end]
	if len(lines) > n {
		lines = lines[len(lines)-n:]
	}
	return lines
}
