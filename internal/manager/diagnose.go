package manager

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/ai"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// AI diagnosis of a server: the panel collects facts, its own rule hits,
// the history and the relevant console lines, removes secrets and asks the
// AI provider. Results are kept per server; proposals are only carried out
// when a user clicks them. With "auto" on, crashes, hangs and sustained
// high load trigger a diagnosis by themselves (rate-limited).

const (
	sampleEvery  = time.Minute
	sampleKeep   = 360 // 6 hours
	sustainedMin = 15  // minutes of high load before an automatic check
	autoCooldown = 6 * time.Hour
	autoPerDay   = 12
	diagKeep     = 5
)

type loadSample struct {
	T       time.Time
	CPU     float64 // percent of the cores the server may use
	MemMB   float64
	LimitMB float64
	Players int
}

type metrics struct {
	mu sync.Mutex
	s  []loadSample
}

type autoDiagState struct {
	mu   sync.Mutex
	last map[string]time.Time // server -> last automatic diagnosis
	day  []time.Time
	busy map[string]bool
}

var autoDiag = autoDiagState{last: map[string]time.Time{}, busy: map[string]bool{}}

func (m *Manager) metricsFor(id string) *metrics {
	v, _ := m.hist.LoadOrStore(id, &metrics{})
	return v.(*metrics)
}

func (m *Manager) cores(sv store.Server) float64 {
	if sv.CPUs > 0 {
		return sv.CPUs
	}
	return float64(runtime.NumCPU())
}

// recordSample keeps one sample per minute of a running server and starts
// an automatic diagnosis when RAM or CPU stay at the limit.
func (m *Manager) recordSample(sv store.Server, l *live) {
	if !l.State.Running || l.Stats.MemLimit == 0 {
		return
	}
	mt := m.metricsFor(sv.ID)
	mt.mu.Lock()
	if n := len(mt.s); n > 0 && time.Since(mt.s[n-1].T) < sampleEvery {
		mt.mu.Unlock()
		return
	}
	s := loadSample{T: time.Now(), CPU: l.Stats.CPUPercent / m.cores(sv), MemMB: float64(l.Stats.MemUsed) / 1048576,
		LimitMB: float64(l.Stats.MemLimit) / 1048576, Players: l.Players}
	mt.s = append(mt.s, s)
	if len(mt.s) > sampleKeep {
		mt.s = mt.s[len(mt.s)-sampleKeep:]
	}
	recent := append([]loadSample(nil), mt.s...)
	mt.mu.Unlock()
	if sv.CreatedBy == testCreator {
		return
	}
	if why := sustained(recent, l.State.StartedAt); why != "" {
		m.AutoDiagnose(sv.ID, why)
	}
}

// sustained reports high load over the last minutes of the current run.
func sustained(s []loadSample, startedAt string) string {
	if len(s) < sustainedMin {
		return ""
	}
	last := s[len(s)-sustainedMin:]
	if t, err := time.Parse(time.RFC3339Nano, startedAt); err == nil && last[0].T.Before(t) {
		return "" // spans a restart
	}
	mem, cpu := true, true
	for _, x := range last {
		if x.LimitMB <= 0 || x.MemMB/x.LimitMB < 0.92 {
			mem = false
		}
		if x.CPU < 95 {
			cpu = false
		}
	}
	switch {
	case mem:
		return fmt.Sprintf("RAM seit %d Minuten fast voll", sustainedMin)
	case cpu:
		return fmt.Sprintf("CPU seit %d Minuten voll ausgelastet", sustainedMin)
	}
	return ""
}

// ---------- collecting ----------

var (
	reIPv4    = regexp.MustCompile(`\b(?:\d{1,3}\.){3}\d{1,3}\b`)
	reSteamID = regexp.MustCompile(`\b7656119\d{10}\b`)
	reToken   = regexp.MustCompile(`\b[A-Za-z0-9+/_-]{32,}={0,2}\b`)
	reEmail   = regexp.MustCompile(`[\w.+-]+@[\w-]+\.[\w.-]+`)
	reBad     = regexp.MustCompile(`(?i)\b(error|err|fatal|exception|panic|crash|failed|failure|killed|oom|out of memory|outofmemory|segmentation fault|sigsegv|sigabrt|abort|warn|warning|cannot|could not|unable|denied|not found|timed? ?out|address already in use|missing)\b`)
	reNoise   = regexp.MustCompile(`(?i)duplicate texture|shader|^\s*$`)
	reDigits  = regexp.MustCompile(`\d+`)
)

func redact(s string, secrets []string) string {
	for _, x := range secrets {
		if len(x) >= 4 {
			s = strings.ReplaceAll(s, x, "<geheim>")
		}
	}
	s = reSteamID.ReplaceAllString(s, "<steamid>")
	s = reEmail.ReplaceAllString(s, "<e-mail>")
	s = reIPv4.ReplaceAllStringFunc(s, func(ip string) string {
		if ip == "0.0.0.0" || strings.HasPrefix(ip, "127.") {
			return ip
		}
		return "<ip>"
	})
	return reToken.ReplaceAllString(s, "<geheim>")
}

// logExcerpt keeps error-like lines (deduplicated) and the last lines.
func logExcerpt(lines []string, secrets []string) (string, []string) {
	type agg struct {
		line string
		n    int
	}
	var bad []*agg
	seen := map[string]*agg{}
	for _, l := range lines {
		if reNoise.MatchString(l) || !reBad.MatchString(l) {
			continue
		}
		key := reDigits.ReplaceAllString(l, "#")
		if a, ok := seen[key]; ok {
			a.n++
			continue
		}
		a := &agg{line: l, n: 1}
		seen[key] = a
		bad = append(bad, a)
	}
	if len(bad) > 60 {
		bad = bad[len(bad)-60:]
	}
	var b strings.Builder
	if len(bad) > 0 {
		b.WriteString("[Auffällige Zeilen]\n")
		for _, a := range bad {
			l := a.line
			if len(l) > 300 {
				l = l[:300] + "…"
			}
			if a.n > 1 {
				l += fmt.Sprintf("   (×%d)", a.n)
			}
			b.WriteString(l + "\n")
		}
	}
	tail := lines
	if len(tail) > 40 {
		tail = tail[len(tail)-40:]
	}
	b.WriteString("[Letzte Zeilen]\n")
	var lastBad []string
	for _, l := range tail {
		if reNoise.MatchString(l) {
			continue
		}
		if len(l) > 300 {
			l = l[:300] + "…"
		}
		b.WriteString(l + "\n")
	}
	for _, a := range bad {
		lastBad = append(lastBad, a.line)
	}
	out := redact(b.String(), secrets)
	if len(out) > 14000 {
		out = out[len(out)-14000:]
	}
	return out, lastBad
}

func hostMemMB() (total, avail float64) {
	b, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	for _, l := range strings.Split(string(b), "\n") {
		f := strings.Fields(l)
		if len(f) < 2 {
			continue
		}
		v, _ := strconv.ParseFloat(f[1], 64)
		switch f[0] {
		case "MemTotal:":
			total = v / 1024
		case "MemAvailable:":
			avail = v / 1024
		}
	}
	return
}

func diskFree(dir string) (free, total float64) {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return 0, 0
	}
	return float64(st.Bavail) * float64(st.Bsize) / 1e9, float64(st.Blocks) * float64(st.Bsize) / 1e9
}

// diagInput gathers everything about one server for the AI.
func (m *Manager) diagInput(ctx context.Context, id, trigger string) (ai.DiagInput, store.Server, templates.Template, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return ai.DiagInput{}, sv, tpl, err
	}
	info := m.Info(sv)
	in := ai.DiagInput{Server: sv.Name, Game: tpl.Name, Status: info.Status, Trigger: trigger}
	var secrets []string
	for _, v := range tpl.Vars {
		val := sv.Vars[v.Key]
		if v.Type == "secret" {
			secrets = append(secrets, val)
			continue
		}
		d := fmt.Sprintf("%s = %q", v.Key, val)
		switch v.Type {
		case "select":
			d += " (Auswahl: " + strings.Join(v.Options, " | ") + ")"
		case "bool":
			d += " (true/false)"
		case "number":
			d += " (Zahl)"
		}
		in.Vars = append(in.Vars, d+" – "+v.Label)
	}
	m.mu.RLock()
	l := m.live[id]
	m.mu.RUnlock()
	facts := []string{fmt.Sprintf("Vorlage %s, Status der Vorlage im Panel: %s", tpl.ID, tpl.Status)}
	if tpl.Runtime == "wine" {
		facts = append(facts, "Läuft als Windows-Server über Wine")
	}
	if info.Message != "" {
		facts = append(facts, "Statusmeldung des Panels: "+info.Message)
	}
	facts = append(facts, fmt.Sprintf("RAM-Grenze des Containers: %d MB", sv.MemoryMB))
	cores := m.cores(sv)
	if sv.CPUs > 0 {
		facts = append(facts, fmt.Sprintf("CPU-Grenze: %.1f Kerne", sv.CPUs))
	} else {
		facts = append(facts, fmt.Sprintf("CPU: keine Grenze, Rechner hat %.0f Kerne", cores))
	}
	if l != nil {
		if l.State.Running {
			facts = append(facts, fmt.Sprintf("Jetzt: RAM %.0f MB von %.0f MB, CPU %.0f %% der erlaubten Kerne",
				float64(l.Stats.MemUsed)/1048576, float64(l.Stats.MemLimit)/1048576, l.Stats.CPUPercent/cores))
			if t, err := time.Parse(time.RFC3339Nano, l.State.StartedAt); err == nil {
				facts = append(facts, "Läuft seit "+time.Since(t).Round(time.Minute).String())
			}
			if l.Players >= 0 {
				facts = append(facts, fmt.Sprintf("Spieler: %d von %d", l.Players, l.MaxPl))
			}
		} else if l.State.StartedAt != "" {
			facts = append(facts, fmt.Sprintf("Gestoppt, letzter Exit-Code %d, vom System wegen Speicher beendet (OOMKilled): %v", l.State.ExitCode, l.State.OOMKilled))
		}
	}
	mt := m.metricsFor(id)
	mt.mu.Lock()
	hist := append([]loadSample(nil), mt.s...)
	mt.mu.Unlock()
	if len(hist) > 0 {
		var cpuSum, cpuMax, memMax, memSum, lim float64
		pmax := 0
		for _, x := range hist {
			cpuSum += x.CPU
			memSum += x.MemMB
			cpuMax, memMax = max(cpuMax, x.CPU), max(memMax, x.MemMB)
			lim = x.LimitMB
			pmax = max(pmax, x.Players)
		}
		n := float64(len(hist))
		facts = append(facts, fmt.Sprintf("Letzte %d Minuten: CPU Schnitt %.0f %%, Spitze %.0f %%; RAM Schnitt %.0f MB, Spitze %.0f MB (%.0f %% der Grenze); höchstens %d Spieler",
			len(hist), cpuSum/n, cpuMax, memSum/n, memMax, 100*memMax/max(lim, 1), pmax))
	} else {
		facts = append(facts, "Keine Messwerte der letzten Stunden (Server lief nicht oder Panel wurde neu gestartet)")
	}
	tot, avail := hostMemMB()
	free, total := diskFree(m.dataDir)
	facts = append(facts, fmt.Sprintf("Rechner: RAM %.0f MB gesamt, %.0f MB frei; Platte %.0f GB frei von %.0f GB", tot, avail, free, total))
	var others []string
	for _, o := range m.st.Servers() {
		if o.ID != id && o.CreatedBy != testCreator {
			oi := m.Info(o)
			others = append(others, fmt.Sprintf("%s (%s, %d MB, %s)", o.Name, oi.TemplateName, o.MemoryMB, oi.Status))
		}
	}
	if len(others) > 0 {
		facts = append(facts, "Weitere Server: "+strings.Join(others, "; "))
	}
	in.Facts = facts

	evs := m.Events(id)
	if len(evs) > 25 {
		evs = evs[len(evs)-25:]
	}
	for _, e := range evs {
		in.Events = append(in.Events, e.Time.Format("02.01. 15:04")+" ["+e.Level+"] "+redact(e.Text, secrets))
	}

	var lines []string
	if rc, err := m.logs(ctx, id, false, 1500, 0); err == nil {
		sc := bufio.NewScanner(rc)
		sc.Buffer(make([]byte, 64*1024), 1<<20)
		for sc.Scan() {
			if t := strings.TrimSpace(ansiRe.ReplaceAllString(sc.Text(), "")); t != "" {
				lines = append(lines, t)
			}
		}
		rc.Close()
	}
	var badLines []string
	in.Log, badLines = logExcerpt(lines, secrets)
	in.Findings = rules(sv, tpl, l, hist, evs, badLines, avail, free, total)
	return in, sv, tpl, nil
}

// rules are the panel's own, deterministic checks.
func rules(sv store.Server, tpl templates.Template, l *live, hist []loadSample, evs []Event, bad []string, availMB, diskFreeGB, diskTotalGB float64) []string {
	var out []string
	has := func(re string) string {
		r := regexp.MustCompile(re)
		for i := len(bad) - 1; i >= 0; i-- {
			if r.MatchString(bad[i]) {
				return bad[i]
			}
		}
		return ""
	}
	if l != nil && l.State.OOMKilled {
		out = append(out, "Docker meldet: Container wegen Speichermangel beendet (OOMKilled)")
	}
	if x := has(`(?i)\bKilled\b`); x != "" {
		out = append(out, "Ein Prozess wurde vom System beendet („Killed“), meist RAM-Grenze erreicht: "+x)
	}
	if x := has(`(?i)OutOfMemoryError|out of memory|std::bad_alloc`); x != "" {
		out = append(out, "Speicherfehler im Log: "+x)
	}
	if x := has(`(?i)segmentation fault|sigsegv|core dumped`); x != "" {
		out = append(out, "Programmabsturz (Speicherzugriffsfehler): "+x)
	}
	if x := has(`(?i)address already in use|bind.*failed|failed to bind`); x != "" {
		out = append(out, "Port belegt: "+x)
	}
	if x := has(`(?i)error while loading shared libraries|cannot open shared object`); x != "" {
		out = append(out, "Fehlende Bibliothek: "+x)
	}
	if x := has(`(?i)Error! App .* state is|No subscription|Missing configuration|Disk write failure`); x != "" {
		out = append(out, "Steam-Download-Fehler: "+x)
		if mm := steamStateRe.FindStringSubmatch(x); mm != nil {
			out = append(out, "Bedeutung von "+mm[1]+" laut SteamCMD-Fehlerliste: "+SteamStateHint(mm[1]))
		}
	}
	if x := has(`(?i)\[steam\] .*(timed out|timeout|resolve|failed|unauthor|denied)`); x != "" {
		out = append(out, "Steams eigenes Protokoll (content_log) meldet: "+x)
	}
	if len(hist) >= 5 {
		var hi, cpuHi int
		for _, x := range hist {
			if x.LimitMB > 0 && x.MemMB/x.LimitMB >= 0.9 {
				hi++
			}
			if x.CPU >= 95 {
				cpuHi++
			}
		}
		if hi*100/len(hist) >= 30 {
			out = append(out, fmt.Sprintf("RAM war in %d von %d Minuten über 90 %% der Grenze", hi, len(hist)))
		}
		if cpuHi*100/len(hist) >= 30 {
			out = append(out, fmt.Sprintf("CPU war in %d von %d Minuten voll ausgelastet", cpuHi, len(hist)))
		}
	}
	var crashes, hangs int
	for _, e := range evs {
		if time.Since(e.Time) > 24*time.Hour {
			continue
		}
		switch {
		case strings.Contains(e.Text, "antwortet seit"):
			hangs++
		case e.Level == "error" || strings.Contains(e.Text, "unerwartet") || strings.Contains(e.Text, "abgestürzt") || strings.Contains(e.Text, "zu wenig RAM"):
			crashes++
		}
	}
	if crashes > 0 {
		out = append(out, fmt.Sprintf("%d Absturz-Meldungen in den letzten 24 Stunden", crashes))
	}
	if hangs > 0 {
		out = append(out, fmt.Sprintf("%d Hänger-Neustarts in den letzten 24 Stunden", hangs))
	}
	if diskTotalGB > 0 && diskFreeGB/diskTotalGB < 0.1 {
		out = append(out, fmt.Sprintf("Platte fast voll: nur %.0f GB frei", diskFreeGB))
	}
	if availMB > 0 && availMB < 1024 {
		out = append(out, fmt.Sprintf("Rechner hat kaum freien RAM (%.0f MB)", availMB))
	}
	if sv.MemoryMB < tpl.MemoryMB {
		out = append(out, fmt.Sprintf("Weniger RAM zugewiesen (%d MB) als die Vorlage empfiehlt (%d MB)", sv.MemoryMB, tpl.MemoryMB))
	}
	return out
}

// ---------- running ----------

func (m *Manager) diagFile(id string) string { return filepath.Join(m.dataDir, "diagnose", id+".json") }

// Diagnoses returns the stored diagnoses of a server, newest first.
func (m *Manager) Diagnoses(id string) []ai.Diagnosis {
	var ds []ai.Diagnosis
	if b, err := os.ReadFile(m.diagFile(id)); err == nil {
		_ = json.Unmarshal(b, &ds)
	}
	if ds == nil {
		ds = []ai.Diagnosis{}
	}
	return ds
}

func (m *Manager) saveDiagnosis(id string, d ai.Diagnosis) {
	ds := append([]ai.Diagnosis{d}, m.Diagnoses(id)...)
	if len(ds) > diagKeep {
		ds = ds[:diagKeep]
	}
	_ = os.MkdirAll(filepath.Dir(m.diagFile(id)), 0o700)
	if b, err := json.Marshal(ds); err == nil {
		_ = os.WriteFile(m.diagFile(id), b, 0o600)
	}
}

// checkActions drops or downgrades proposals the panel cannot carry out.
func checkActions(d *ai.Diagnosis, sv store.Server, tpl templates.Template, hostMB float64) {
	out := d.Actions[:0]
	for _, a := range d.Actions {
		switch a.Kind {
		case "restart", "update", "manual":
		case "memory":
			mx := int(hostMB*0.9) / 256 * 256
			if a.MemoryMB < tpl.MinMemoryMB {
				a.MemoryMB = tpl.MinMemoryMB
			}
			if mx > 0 && a.MemoryMB > mx {
				a.MemoryMB = mx
				a.Title = fmt.Sprintf("RAM auf %.1f GB erhöhen", float64(mx)/1024)
				a.Why += " Mehr als " + fmt.Sprintf("%.1f GB", float64(mx)/1024) + " gibt der Rechner nicht her; für mehr braucht die VM mehr RAM."
			}
			a.MemoryMB = (a.MemoryMB + 255) / 256 * 256
			if a.MemoryMB == sv.MemoryMB {
				continue
			}
		case "var":
			ok := false
			for _, v := range tpl.Vars {
				if v.Key != a.Key || v.Type == "secret" {
					continue
				}
				switch v.Type {
				case "select":
					for _, o := range v.Options {
						ok = ok || o == a.Value
					}
				case "bool":
					ok = a.Value == "true" || a.Value == "false"
				case "number":
					_, err := strconv.Atoi(a.Value)
					ok = err == nil
				default:
					ok = true
				}
			}
			if !ok {
				a.Kind, a.Key, a.Value = "manual", "", ""
			}
		default:
			a.Kind = "manual"
		}
		out = append(out, a)
	}
	d.Actions = out
}

func (m *Manager) aiSettings() store.Settings {
	var s store.Settings
	m.st.Read(func(d *store.Data) { s = d.Settings })
	return s
}

// Diagnose asks the AI about one server and stores the result.
func (m *Manager) Diagnose(ctx context.Context, id, trigger string) (ai.Diagnosis, error) {
	s := m.aiSettings()
	if s.AIProvider == "" {
		return ai.Diagnosis{}, errors.New("Kein KI-Anbieter eingerichtet. Bitte unter Einstellungen → Allgemein & KI auswählen")
	}
	in, sv, tpl, err := m.diagInput(ctx, id, trigger)
	if err != nil {
		return ai.Diagnosis{}, err
	}
	d, err := ai.Diagnose(ctx, s, in)
	if err != nil {
		return d, err
	}
	tot, _ := hostMemMB()
	checkActions(&d, sv, tpl, tot)
	m.saveDiagnosis(id, d)
	return d, nil
}

// AutoDiagnose runs a diagnosis in the background after a problem, if the
// user switched it on. At most one per server every six hours and twelve
// per day, so a crash loop does not run up the AI bill.
func (m *Manager) AutoDiagnose(id, why string) {
	s := m.aiSettings()
	if !s.AIAutoDiagnose || s.AIProvider == "" {
		return
	}
	now := time.Now()
	autoDiag.mu.Lock()
	var day []time.Time
	for _, t := range autoDiag.day {
		if now.Sub(t) < 24*time.Hour {
			day = append(day, t)
		}
	}
	autoDiag.day = day
	if autoDiag.busy[id] || now.Sub(autoDiag.last[id]) < autoCooldown || len(day) >= autoPerDay {
		autoDiag.mu.Unlock()
		return
	}
	autoDiag.busy[id], autoDiag.last[id] = true, now
	autoDiag.day = append(autoDiag.day, now)
	autoDiag.mu.Unlock()
	go func() {
		defer func() { autoDiag.mu.Lock(); delete(autoDiag.busy, id); autoDiag.mu.Unlock() }()
		// give the server a moment, so the log of a crash is complete
		time.Sleep(20 * time.Second)
		ctx, cancel := context.WithTimeout(context.Background(), 3*time.Minute)
		defer cancel()
		d, err := m.Diagnose(ctx, id, why)
		if err != nil {
			m.Event(id, "warn", "KI-Diagnose nach „"+why+"“ fehlgeschlagen: "+err.Error())
			return
		}
		lvl := map[string]string{"ok": "info", "hinweis": "info", "warnung": "warn", "kritisch": "error"}[d.Severity]
		txt := "KI-Diagnose (" + why + "): " + d.Summary
		if len(d.Actions) > 0 {
			txt += " Vorschläge unter „Mit KI prüfen“."
		}
		m.EventKind(id, lvl, "ai", txt)
	}()
}
