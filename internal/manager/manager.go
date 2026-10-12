// Package manager turns templates into running Docker containers and keeps
// a live view of every server's state.
package manager

import (
	"bufio"
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/query"
	"github.com/derface/Gameserver-controlPanel/internal/remote"
	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

const containerUID = 1000

type Manager struct {
	st          *store.Store
	dk          *docker.Client
	tpls        *templates.Set
	dataDir     string
	Limit       func() (maxServers int) // from the license; <0 = unlimited
	AutoBackups func() bool             // from the license
	Remote      func() (remote.Target, remote.Config)
	up          uploads

	buildMu   sync.Mutex
	opMu      sync.Map // id -> *sync.Mutex
	refreshMu sync.Map // id -> *sync.Mutex

	testMu     sync.Mutex
	test       *TestRun
	testCancel context.CancelFunc
	baseCtx    context.Context

	ev           eventLog
	sl           steamLogin
	pendingTasks map[string]bool      // guarded by mu
	expectAt     map[string]time.Time // panel-initiated start/stop, guarded by mu

	mu   sync.RWMutex
	jobs map[string]*job
	live map[string]*live
}

type job struct {
	Msg string
	Err string
}

type live struct {
	State     docker.State
	Stats     docker.Stats
	Phase     string
	Progress  float64
	Msg       string
	readyFor  string // StartedAt of the run in which the ready line was seen
	UpdatedAt time.Time
	Players   int // -1 = unknown
	MaxPl     int
	queriedAt time.Time

	// watchdog
	crashes     []time.Time
	countedRun  string    // StartedAt of the run already counted as crash
	answeredAt  time.Time // last successful player query
	answeredRun string    // StartedAt of the run that answered
	hangRun     string    // run for which a hang restart was triggered
	haltMsg     string    // why the watchdog stopped the server
}

// Info is what the API returns for one server.
type Info struct {
	store.Server
	TemplateName string  `json:"template_name"`
	Category     string  `json:"category"`
	Console      string  `json:"console"`
	Status       string  `json:"status"` // preparing | installing | starting | running | stopped | error
	Message      string  `json:"message,omitempty"`
	Progress     float64 `json:"progress,omitempty"`
	CPU          float64 `json:"cpu"`
	MemMB        float64 `json:"mem_mb"`
	Players      int     `json:"players"`     // -1 = unbekannt
	MaxPlayers   int     `json:"max_players"` // 0 = unbekannt
	Notes        string  `json:"notes,omitempty"`
	DataDir      string  `json:"data_dir"`
}

func New(st *store.Store, dk *docker.Client, tpls *templates.Set, dataDir string) *Manager {
	if err := writeRunScript(dataDir); err != nil {
		log.Printf("[Panel] Startskript: %v", err)
	}
	return &Manager{st: st, dk: dk, tpls: tpls, dataDir: dataDir, jobs: map[string]*job{}, live: map[string]*live{},
		Limit: func() int { return -1 }, AutoBackups: func() bool { return true }}
}

func (m *Manager) lock(id string) func() {
	v, _ := m.opMu.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	return mu.Unlock
}

func newID() string {
	b := make([]byte, 4)
	_, _ = rand.Read(b)
	return hex.EncodeToString(b)
}

func (m *Manager) setJob(id, msg string) {
	m.mu.Lock()
	m.jobs[id] = &job{Msg: msg}
	m.mu.Unlock()
}

func (m *Manager) failJob(id string, err error) {
	m.mu.Lock()
	m.jobs[id] = &job{Err: err.Error()}
	m.mu.Unlock()
}

func (m *Manager) clearJob(id string) {
	m.mu.Lock()
	delete(m.jobs, id)
	m.mu.Unlock()
}

func (m *Manager) DataDir() string { return m.dataDir }

func (m *Manager) ServerDir(id string) string { return filepath.Join(m.dataDir, "servers", id) }

// ---------- create ----------

type CreateReq struct {
	Template  string            `json:"template"`
	Name      string            `json:"name"`
	Vars      map[string]string `json:"vars"`
	MemoryMB  int               `json:"memory_mb"`
	Start     bool              `json:"start"`
	CreatedBy string            `json:"-"`
	internal  bool              // self-test: not counted against the license
}

// Validate checks a request without creating anything. It returns the
// template and resolved variables.
func (m *Manager) Validate(req CreateReq) (templates.Template, map[string]string, []templates.Port, error) {
	tpl, ok := m.tpls.Get(req.Template)
	if !ok {
		return tpl, nil, nil, fmt.Errorf("Die Vorlage „%s“ gibt es nicht", req.Template)
	}
	name := strings.TrimSpace(templates.Clean(req.Name))
	if name == "" || len([]rune(name)) > 40 {
		return tpl, nil, nil, errors.New("Der Name muss 1 bis 40 Zeichen lang sein")
	}
	vars, err := tpl.ResolveVars(req.Vars)
	if err != nil {
		return tpl, nil, nil, err
	}
	ports := tpl.Ports
	if tpl.Custom {
		if ports, err = templates.ParsePorts(vars["ports"]); err != nil {
			return tpl, nil, nil, err
		}
		if n, _ := strconv.Atoi(vars["app_id"]); n <= 0 {
			return tpl, nil, nil, errors.New("Die Steam-App-ID fehlt")
		}
	}
	if tpl.SteamLogin && m.steamUser() == "" {
		return tpl, nil, nil, fmt.Errorf("%s lässt sich nur mit einem Steam-Konto herunterladen. Melde dich unter Einstellungen → Steam-Konto an", tpl.Name)
	}
	if tpl.Status == "defekt" && !req.internal {
		return tpl, nil, nil, fmt.Errorf("Die Vorlage %s funktioniert derzeit nicht und ist deshalb gesperrt", tpl.Name)
	}
	if req.MemoryMB != 0 && req.MemoryMB < tpl.MinMemoryMB {
		return tpl, nil, nil, fmt.Errorf("%s braucht mindestens %d MB RAM", tpl.Name, tpl.MinMemoryMB)
	}
	if lim := m.Limit(); !req.internal && lim >= 0 && m.countServers() >= lim {
		return tpl, nil, nil, fmt.Errorf("Deine Edition erlaubt %d Server. Für mehr bitte upgraden", lim)
	}
	return tpl, vars, ports, nil
}

func baseApp(t templates.Template) string {
	if t.SteamBase <= 0 {
		return ""
	}
	return strconv.Itoa(t.SteamBase)
}

func (m *Manager) loginFor(tpl templates.Template) string {
	if !tpl.SteamLogin {
		return ""
	}
	return m.steamUser()
}

func restartPolicy(sv store.Server) docker.RestartPolicy {
	if sv.Watch.NoAutoRestart {
		return docker.RestartPolicy{Name: "no"}
	}
	return docker.RestartPolicy{Name: "unless-stopped"}
}

// expect marks a start/stop/restart as wanted, so the watchdog does not
// count it as a crash.
func (m *Manager) expect(id string) {
	m.mu.Lock()
	if m.expectAt == nil {
		m.expectAt = map[string]time.Time{}
	}
	m.expectAt[id] = time.Now()
	m.mu.Unlock()
}

// consumeExpect is called when the watchdog sees a start/stop/restart; it
// reports whether the panel asked for it (within the last 30 minutes, which
// covers image builds and recreations) and clears the mark.
func (m *Manager) consumeExpect(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	t, ok := m.expectAt[id]
	delete(m.expectAt, id)
	return ok && time.Since(t) < 30*time.Minute
}

func (m *Manager) expected(id string) bool {
	m.mu.RLock()
	defer m.mu.RUnlock()
	t, ok := m.expectAt[id]
	return ok && time.Since(t) < 30*time.Minute
}

// countServers counts the servers that use up the license (not self-tests).
func (m *Manager) countServers() int {
	n := 0
	for _, s := range m.st.Servers() {
		if s.CreatedBy != testCreator {
			n++
		}
	}
	return n
}

func (m *Manager) Create(req CreateReq) (store.Server, error) {
	tpl, vars, tports, err := m.Validate(req)
	if err != nil {
		return store.Server{}, err
	}
	mem := req.MemoryMB
	if mem == 0 {
		mem = tpl.MemoryMB
	}
	ports, err := m.allocatePorts(tpl, tports)
	if err != nil {
		return store.Server{}, err
	}
	sv := store.Server{
		ID: newID(), Name: strings.TrimSpace(templates.Clean(req.Name)), Template: tpl.ID, Vars: vars,
		Ports: ports, MemoryMB: mem, UpdateOnRun: tpl.Kind == "steam", Created: time.Now(), CreatedBy: req.CreatedBy,
	}
	if sv.CreatedBy == "" {
		sv.CreatedBy = "manual"
	}
	if err := m.st.SaveServer(sv); err != nil {
		return sv, err
	}
	m.setJob(sv.ID, "Wird vorbereitet")
	go m.prepare(sv.ID, req.Start)
	return sv, nil
}

func portFree(port int, proto string) bool {
	if proto == "udp" {
		c, err := net.ListenPacket("udp", ":"+strconv.Itoa(port))
		if err != nil {
			return false
		}
		c.Close()
		return true
	}
	l, err := net.Listen("tcp", ":"+strconv.Itoa(port))
	if err != nil {
		return false
	}
	l.Close()
	return true
}

func (m *Manager) allocatePorts(tpl templates.Template, tports []templates.Port) ([]store.PortMap, error) {
	used := map[string]bool{}
	for _, s := range m.st.Servers() {
		for _, p := range s.Ports {
			used[fmt.Sprintf("%d/%s", p.Host, p.Proto)] = true
		}
	}
	// step keeps multi-port games (2456+2457) from overlapping the next instance
	lo, hi := 65535, 0
	for _, p := range tports {
		lo, hi = min(lo, p.Port), max(hi, p.Port)
	}
	step := 1
	if hi-lo < 10 {
		step = hi - lo + 1
	}
	tries := 50
	if tpl.FixedPorts {
		tries = 1
	}
	for k := 0; k < tries; k++ {
		off := k * step
		ok := true
		for _, p := range tports {
			hp := p.Port + off
			if hp > 65535 || used[fmt.Sprintf("%d/%s", hp, p.Proto)] || !portFree(hp, p.Proto) {
				ok = false
				break
			}
		}
		if !ok {
			continue
		}
		out := make([]store.PortMap, 0, len(tports))
		for _, p := range tports {
			hp := p.Port + off
			cp := hp // steam games are told their port directly
			if tpl.Kind == "image" {
				cp = p.Port
			}
			out = append(out, store.PortMap{Name: p.Name, Host: hp, Container: cp, Proto: p.Proto})
		}
		return out, nil
	}
	if tpl.FixedPorts {
		return nil, fmt.Errorf("Die Ports von %s sind belegt. Dieses Spiel kann nur einmal pro Rechner laufen", tpl.Name)
	}
	return nil, errors.New("Keine freien Ports gefunden")
}

func (m *Manager) ensureImage(ctx context.Context, id string, tpl templates.Template) error {
	m.buildMu.Lock()
	defer m.buildMu.Unlock()
	if tpl.Kind == "steam" {
		if !m.dk.ImageExists(ctx, SteamImage) {
			m.setJob(id, "Steam-Laufzeit wird eingerichtet (einmalig, 2 bis 5 Minuten)")
			err := m.dk.Build(ctx, SteamImage, map[string]string{"Dockerfile": steamDockerfile, "gp-run": steamRunScript}, func(line string) {
				if strings.HasPrefix(line, "Step") {
					m.setJob(id, "Steam-Laufzeit: "+line)
				}
			})
			if err != nil {
				return err
			}
		}
		img := SteamImageFor(tpl)
		if img == SteamImage || m.dk.ImageExists(ctx, img) {
			return nil
		}
		df, err := layerDockerfile(tpl)
		if err != nil {
			return err
		}
		what := "Zusatzpakete für " + tpl.Name
		if tpl.Runtime == "wine" {
			what = "Wine für Windows-Server (einmalig, 3 bis 8 Minuten)"
		}
		m.setJob(id, what+" werden eingerichtet")
		return m.dk.Build(ctx, img, map[string]string{"Dockerfile": df}, func(line string) {
			if strings.HasPrefix(line, "Step") {
				m.setJob(id, what+": "+line)
			}
		})
	}
	if m.dk.ImageExists(ctx, tpl.Image) {
		return nil
	}
	m.setJob(id, "Image wird geladen: "+tpl.Image)
	return m.dk.Pull(ctx, tpl.Image, func(line string) {
		if len(line) > 90 {
			line = line[:90]
		}
		m.setJob(id, "Image wird geladen: "+line)
	})
}

func (m *Manager) containerConfig(sv store.Server, tpl templates.Template) docker.ContainerConfig {
	hostPorts := make([]int, len(sv.Ports))
	for i, p := range sv.Ports {
		hostPorts[i] = p.Host
	}
	cfg := docker.ContainerConfig{
		Labels:      map[string]string{"gamepanel.id": sv.ID, "gamepanel.template": tpl.ID},
		OpenStdin:   true,
		StopTimeout: tpl.StopTimeout,
		StopSignal:  tpl.StopSignal,
		HostConfig: docker.HostConfig{
			Binds:         []string{m.ServerDir(sv.ID) + ":" + tpl.DataPathOr()},
			PortBindings:  map[string][]docker.PortBinding{},
			Memory:        int64(sv.MemoryMB) * 1024 * 1024,
			RestartPolicy: restartPolicy(sv),
		},
		ExposedPorts: map[string]struct{}{},
	}
	if sv.CPUs > 0 {
		cfg.HostConfig.NanoCPUs = int64(sv.CPUs * 1e9)
	}
	for _, p := range sv.Ports {
		key := fmt.Sprintf("%d/%s", p.Container, p.Proto)
		cfg.ExposedPorts[key] = struct{}{}
		cfg.HostConfig.PortBindings[key] = append(cfg.HostConfig.PortBindings[key], docker.PortBinding{HostPort: strconv.Itoa(p.Host)})
	}
	if tpl.Kind == "steam" {
		appID := tpl.SteamAppID
		start := tpl.Render(tpl.Start, sv.Vars, hostPorts)
		if tpl.Custom {
			appID, _ = strconv.Atoi(sv.Vars["app_id"])
			start = tpl.Render(start, sv.Vars, hostPorts)
		}
		upd := "0"
		if sv.UpdateOnRun {
			upd = "1"
		}
		cfg.Image = SteamImageFor(tpl)
		// the start script comes from the panel, so a new version does not
		// need a new image (and no new Wine download)
		cfg.Labels["gamepanel.script"] = runScriptHash
		cfg.HostConfig.Binds = append(cfg.HostConfig.Binds, runScriptPath(m.dataDir)+":/usr/local/bin/gp-run:ro")
		if tpl.SteamLogin {
			cfg.HostConfig.Binds = append(cfg.HostConfig.Binds, m.SteamAuthDir()+":/home/steam/Steam")
		}
		cfg.Env = []string{
			"GP_APP_ID=" + strconv.Itoa(appID),
			"GP_START=" + start,
			"GP_UPDATE=" + upd,
			"GP_BETA=" + tpl.SteamBeta,
			"GP_PLATFORM=" + tpl.Platform,
			"GP_MOD=" + tpl.SteamMod,
			"GP_PREPLATFORM=" + tpl.SteamPrePl,
			"GP_BASE_APP=" + baseApp(tpl),
			"GP_BASE_DIR=" + tpl.SteamBaseIn,
			"GP_STEAM_USER=" + m.loginFor(tpl),
			"GP_LOGFILE=" + tpl.LogFile,
			"TZ=Europe/Berlin",
		}
		return cfg
	}
	cfg.Image = tpl.Image
	for _, a := range tpl.Cmd {
		cfg.Cmd = append(cfg.Cmd, tpl.Render(a, sv.Vars, hostPorts))
	}
	for k, v := range tpl.Env {
		val := tpl.Render(v, sv.Vars, hostPorts)
		if val != "" {
			cfg.Env = append(cfg.Env, k+"="+val)
		}
	}
	if tpl.JavaMemEnv != "" {
		cfg.Env = append(cfg.Env, fmt.Sprintf("%s=%dM", tpl.JavaMemEnv, sv.MemoryMB*80/100))
	}
	return cfg
}

func (m *Manager) prepare(id string, start bool) {
	unlock := m.lock(id)
	defer unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
	defer cancel()
	err := func() error {
		sv, ok := m.st.Server(id)
		if !ok {
			return errors.New("Server wurde gelöscht")
		}
		tpl, ok := m.tpls.Get(sv.Template)
		if !ok {
			return fmt.Errorf("Vorlage %s fehlt", sv.Template)
		}
		dir := m.ServerDir(id)
		if err := os.MkdirAll(dir, 0o755); err != nil {
			return err
		}
		uid := containerUID
		if tpl.DataUID > 0 {
			uid = tpl.DataUID
		}
		_ = os.Chown(dir, uid, uid)
		if err := m.ensureImage(ctx, id, tpl); err != nil {
			return fmt.Errorf("Image: %w", err)
		}
		m.setJob(id, "Container wird angelegt")
		if sv.ContainerID != "" {
			_ = m.dk.Remove(ctx, sv.ContainerID)
		}
		_ = m.dk.Remove(ctx, "gp-"+id)
		cid, err := m.dk.Create(ctx, "gp-"+id, m.containerConfig(sv, tpl))
		if err != nil {
			return err
		}
		sv.ContainerID = cid
		if err := m.st.ModifyServer(id, func(cur *store.Server) error { cur.ContainerID = cid; return nil }); err != nil {
			return err
		}
		if start {
			m.setJob(id, "Wird gestartet")
			m.expect(id)
			m.requestUpdate(id)
			return m.dk.Start(ctx, cid)
		}
		return nil
	}()
	if err != nil {
		m.failJob(id, err)
		return
	}
	m.refresh(id) // first the live state, then drop the job, so the UI never sees a gap
	m.consumeExpect(id)
	m.clearJob(id)
}

// ---------- lifecycle ----------

func (m *Manager) get(id string) (store.Server, templates.Template, error) {
	sv, ok := m.st.Server(id)
	if !ok {
		return sv, templates.Template{}, errors.New("Server nicht gefunden")
	}
	tpl, ok := m.tpls.Get(sv.Template)
	if !ok {
		return sv, tpl, fmt.Errorf("Vorlage %s fehlt", sv.Template)
	}
	return sv, tpl, nil
}

func (m *Manager) Start(ctx context.Context, id string) error {
	m.expect(id)
	m.requestUpdate(id)
	sv, _, err := m.get(id)
	if err != nil {
		return err
	}
	if sv.ContainerID == "" {
		m.setJob(id, "Wird vorbereitet")
		go m.prepare(id, true)
		return nil
	}
	if m.outdated(ctx, sv) {
		m.setJob(id, "Laufzeit wurde aktualisiert, Container wird neu angelegt")
		go m.prepare(id, true)
		return nil
	}
	m.clearJob(id)
	err = m.dk.Start(ctx, sv.ContainerID)
	if errors.Is(err, docker.ErrNotFound) {
		m.setJob(id, "Container wird neu angelegt")
		go m.prepare(id, true)
		return nil
	}
	m.refresh(id)
	return err
}

func (m *Manager) Stop(ctx context.Context, id string) error {
	m.expect(id)
	sv, tpl, err := m.get(id)
	if err != nil || sv.ContainerID == "" {
		return err
	}
	err = m.dk.Stop(ctx, sv.ContainerID, tpl.StopTimeout)
	m.refresh(id)
	return err
}

func (m *Manager) Restart(ctx context.Context, id string) error {
	m.expect(id)
	m.requestUpdate(id)
	sv, tpl, err := m.get(id)
	if err != nil {
		return err
	}
	if sv.ContainerID == "" || m.outdated(ctx, sv) {
		return m.Start(ctx, id)
	}
	err = m.dk.Restart(ctx, sv.ContainerID, tpl.StopTimeout)
	m.refresh(id)
	return err
}

// outdated reports whether a Steam server's container was built from an
// older runtime image than the current one.
func (m *Manager) outdated(ctx context.Context, sv store.Server) bool {
	tpl, ok := m.tpls.Get(sv.Template)
	if !ok || tpl.Kind != "steam" || sv.ContainerID == "" {
		return false
	}
	img, labels, err := m.dk.InspectLabels(ctx, sv.ContainerID)
	return err == nil && img != "" && (img != SteamImageFor(tpl) || labels["gamepanel.script"] != runScriptHash)
}

// Recreate rebuilds the container with the current settings (keeps data).
func (m *Manager) Recreate(id string, start bool) {
	m.expect(id)
	m.setJob(id, "Wird neu angelegt")
	go m.prepare(id, start)
}

func (m *Manager) Delete(ctx context.Context, id string, wipe bool) error {
	unlock := m.lock(id)
	defer unlock()
	sv, ok := m.st.Server(id)
	if !ok {
		return errors.New("Server nicht gefunden")
	}
	if sv.ContainerID != "" {
		if err := m.dk.Remove(ctx, sv.ContainerID); err != nil {
			return err
		}
	}
	if wipe {
		dir := m.ServerDir(id)
		if strings.HasPrefix(dir, filepath.Join(m.dataDir, "servers")+string(os.PathSeparator)) {
			_ = os.RemoveAll(dir)
		}
		_ = os.RemoveAll(m.backupDir(id))
	}
	m.mu.Lock()
	delete(m.jobs, id)
	delete(m.live, id)
	m.mu.Unlock()
	m.dropEvents(id)
	return m.st.DeleteServer(id)
}

func (m *Manager) Command(ctx context.Context, id, cmd string) (string, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return "", err
	}
	if sv.ContainerID == "" {
		return "", errors.New("Server läuft nicht")
	}
	cmd = strings.TrimSpace(cmd)
	if cmd == "" {
		return "", nil
	}
	if tpl.Console == "exec" {
		args := append(append([]string{}, tpl.ExecPrefix...), strings.Fields(cmd)...)
		return m.dk.Exec(ctx, sv.ContainerID, args)
	}
	return "", m.dk.SendStdin(ctx, sv.ContainerID, cmd)
}

func (m *Manager) Logs(ctx context.Context, id string, follow bool, tail int) (io.ReadCloser, error) {
	return m.logs(ctx, id, follow, tail, 0)
}

func (m *Manager) logs(ctx context.Context, id string, follow bool, tail int, since int64) (io.ReadCloser, error) {
	sv, ok := m.st.Server(id)
	if !ok {
		return nil, errors.New("Server nicht gefunden")
	}
	if sv.ContainerID == "" {
		return nil, errors.New("Noch kein Container vorhanden")
	}
	return m.dk.Logs(ctx, sv.ContainerID, follow, tail, since)
}

// ---------- live state ----------

var steamProgress = regexp.MustCompile(`progress: ([0-9]+\.[0-9]+)`)

func (m *Manager) refresh(id string) {
	// one refresh per server at a time: the watchdog compares with the
	// previous state and must not see the same transition twice
	v, _ := m.refreshMu.LoadOrStore(id, &sync.Mutex{})
	mu := v.(*sync.Mutex)
	mu.Lock()
	defer mu.Unlock()
	sv, ok := m.st.Server(id)
	if !ok || sv.ContainerID == "" {
		return
	}
	tpl, _ := m.tpls.Get(sv.Template)
	ctx, cancel := context.WithTimeout(context.Background(), 8*time.Second)
	defer cancel()
	state, err := m.dk.Inspect(ctx, sv.ContainerID)

	m.mu.RLock()
	prev := m.live[id]
	m.mu.RUnlock()
	l := &live{UpdatedAt: time.Now()}
	l.Players = -1
	if prev != nil {
		l.readyFor = prev.readyFor
		l.Players, l.MaxPl, l.queriedAt = prev.Players, prev.MaxPl, prev.queriedAt
		l.crashes, l.countedRun = prev.crashes, prev.countedRun
		l.answeredAt, l.answeredRun, l.hangRun = prev.answeredAt, prev.answeredRun, prev.hangRun
	}
	if err != nil {
		l.Phase, l.Msg = "error", "Container fehlt. Mit „Starten“ wird er neu angelegt."
		if !errors.Is(err, docker.ErrNotFound) {
			l.Msg = err.Error()
		}
	} else {
		l.State = state
		switch {
		case state.Running:
			l.Stats, _ = m.dk.Stats(ctx, sv.ContainerID)
			l.Phase = "running"
			if l.readyFor != state.StartedAt {
				l.Phase = m.phaseFromLogs(ctx, sv, tpl, state, l)
			}
			if l.Phase == "running" {
				m.queryPlayers(sv, tpl, l)
			} else {
				l.Players, l.MaxPl = -1, 0
			}
		case state.OOMKilled:
			l.Phase, l.Msg = "error", "Zu wenig RAM. Bitte mehr Arbeitsspeicher zuweisen."
		default:
			l.Phase = "stopped"
			if c := state.ExitCode; c != 0 && c != 130 && c != 137 && c != 143 {
				l.Msg = fmt.Sprintf("Beendet mit Code %d. Details in der Konsole.", c)
			}
		}
	}
	if err == nil && sv.CreatedBy != testCreator {
		m.watchdog(sv, tpl, prev, l)
	}
	m.mu.Lock()
	m.live[id] = l
	m.mu.Unlock()
}

// phaseFromLogs scans the log of the current run line by line. It only
// runs until the ready line was seen once per run, so the cost is small.
func (m *Manager) phaseFromLogs(ctx context.Context, sv store.Server, tpl templates.Template, state docker.State, l *live) string {
	var since int64
	if t, err := time.Parse(time.RFC3339Nano, state.StartedAt); err == nil {
		since = t.Unix() - 1
	}
	rc, err := m.dk.Logs(ctx, sv.ContainerID, false, -1, since)
	if err != nil {
		return "running"
	}
	defer rc.Close()
	var ready *regexp.Regexp
	if tpl.Ready != "" {
		ready, _ = regexp.Compile(tpl.Ready)
	}
	installing, started, failed, isReady, noSpace, noLogin, wine := false, tpl.Kind != "steam", false, false, false, false, false
	progress := 0.0
	sc := bufio.NewScanner(io.LimitReader(rc, 64<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		line := sc.Text()
		switch {
		case strings.Contains(line, "[panel] SteamCMD"):
			installing, started, progress = true, false, 0
		case strings.Contains(line, "[panel] Installation fehlgeschlagen"):
			failed = true
		case strings.Contains(line, "[panel] Steam-Anmeldung fehlt"):
			failed, noLogin = true, true
		case installing && strings.Contains(line, "state is 0x202"):
			noSpace = true // SteamCMD: not enough disk space
		case strings.Contains(line, "[panel] Wine"):
			installing, wine = false, true // setting up Wine after the download
		case strings.Contains(line, "[panel] Start:"):
			installing, started, wine = false, true, false
		case installing:
			if mm := steamProgress.FindStringSubmatch(line); mm != nil {
				progress, _ = strconv.ParseFloat(mm[1], 64)
			}
		case started && ready != nil && !isReady:
			isReady = ready.MatchString(line)
		}
	}
	switch {
	case failed && !started:
		l.Msg = "SteamCMD-Installation fehlgeschlagen. Details in der Konsole."
		if noSpace {
			l.Msg = "SteamCMD-Installation fehlgeschlagen: nicht genug Speicherplatz auf der Platte."
		}
		if noLogin {
			l.Msg = "Steam-Anmeldung fehlt oder ist abgelaufen. Unter Einstellungen → Steam-Konto neu anmelden, dann den Server neu starten."
		}
		return "error"
	case installing:
		l.Progress = progress / 100
		l.Msg = "SteamCMD lädt die Serverdateien"
		return "installing"
	case wine:
		l.Msg = "Wine wird geprüft bzw. eingerichtet (beim ersten Mal 1 bis 3 Minuten)"
		return "starting"
	case !started:
		return "starting"
	case ready == nil || isReady:
		l.readyFor = state.StartedAt
		return "running"
	}
	return "starting"
}

// queryPlayers asks the server for its player count, at most every 10 s.
func (m *Manager) queryPlayers(sv store.Server, tpl templates.Template, l *live) {
	if tpl.Query == nil || tpl.Query.Port < 0 || tpl.Query.Port >= len(sv.Ports) {
		return
	}
	if time.Since(l.queriedAt) < 10*time.Second {
		return
	}
	l.queriedAt = time.Now()
	port := sv.Ports[tpl.Query.Port].Host
	var r query.Result
	var err error
	switch tpl.Query.Type {
	case "a2s":
		r, err = query.A2S("127.0.0.1", port)
	case "minecraft":
		r, err = query.Minecraft("127.0.0.1", port)
	default:
		return
	}
	if err != nil {
		l.Players, l.MaxPl = -1, 0
		return
	}
	l.Players, l.MaxPl = r.Players, r.Max
	l.answeredAt, l.answeredRun = time.Now(), l.State.StartedAt
}

// Run keeps the live state fresh until ctx ends.
func (m *Manager) Run(ctx context.Context) {
	m.testMu.Lock()
	m.baseCtx = ctx
	m.testMu.Unlock()
	go m.resumeTest()
	go m.runSchedule(ctx)
	go m.runTasks(ctx)
	t := time.NewTicker(3 * time.Second)
	defer t.Stop()
	for {
		var wg sync.WaitGroup
		for _, sv := range m.st.Servers() {
			if sv.ContainerID == "" {
				continue
			}
			wg.Add(1)
			go func(id string) { defer wg.Done(); m.refresh(id) }(sv.ID)
		}
		wg.Wait()
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
	}
}

func (m *Manager) Info(sv store.Server) Info {
	tpl, _ := m.tpls.Get(sv.Template)
	in := Info{Players: -1, Server: sv, TemplateName: tpl.Name, Category: tpl.Category, Console: tpl.Console, Notes: tpl.Notes, DataDir: m.ServerDir(sv.ID)}
	// never send secrets to the browser in lists
	in.Vars = map[string]string{}
	for _, v := range tpl.Vars {
		if val, ok := sv.Vars[v.Key]; ok {
			if v.Type == "secret" && val != "" {
				val = "••••••"
			}
			in.Vars[v.Key] = val
		}
	}
	m.mu.RLock()
	j := m.jobs[sv.ID]
	l := m.live[sv.ID]
	m.mu.RUnlock()
	switch {
	case j != nil && j.Err != "":
		in.Status, in.Message = "error", j.Err
	case j != nil:
		in.Status, in.Message = "preparing", j.Msg
	case l != nil:
		in.Status, in.Message, in.Progress = l.Phase, l.Msg, l.Progress
		in.CPU = l.Stats.CPUPercent
		in.Players, in.MaxPlayers = l.Players, l.MaxPl
		if l.Phase != "running" {
			in.Players, in.MaxPlayers = -1, 0
		}
		in.MemMB = float64(l.Stats.MemUsed) / 1024 / 1024
	case sv.ContainerID == "":
		in.Status = "stopped"
	default:
		in.Status = "stopped"
	}
	return in
}

func (m *Manager) List() []Info {
	svs := m.st.Servers()
	out := make([]Info, 0, len(svs))
	for _, sv := range svs {
		out = append(out, m.Info(sv))
	}
	return out
}

func (m *Manager) Templates() *templates.Set { return m.tpls }
