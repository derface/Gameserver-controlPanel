package manager

import (
	"bufio"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/docker"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// Is there a newer version on Steam? The installed build comes from
// SteamCMD's app manifest in the server folder, the newest one from
// "app_info_print" in a short-lived SteamCMD container (cached 10 minutes).

var reBuildID = regexp.MustCompile(`"buildid"\s+"(\d+)"`)

// installedBuild reads the build id SteamCMD wrote after the last update.
func (m *Manager) installedBuild(id string, tpl templates.Template) string {
	p := filepath.Join(m.ServerDir(id), "server", "steamapps", "appmanifest_"+strconv.Itoa(tpl.SteamAppID)+".acf")
	b, err := os.ReadFile(p)
	if err != nil {
		return ""
	}
	if mt := reBuildID.FindSubmatch(b); mt != nil {
		return string(mt[1])
	}
	return ""
}

// parseLatestBuild finds branches/<branch>/buildid in app_info_print output.
func parseLatestBuild(out, branch string) string {
	i := strings.Index(out, `"branches"`)
	if i < 0 {
		return ""
	}
	rest := out[i:]
	j := strings.Index(rest, `"`+branch+`"`)
	if j < 0 {
		return ""
	}
	if mt := reBuildID.FindStringSubmatch(rest[j:]); mt != nil {
		return mt[1]
	}
	return ""
}

var latestCache = struct {
	sync.Mutex
	m map[string]struct {
		build string
		at    time.Time
	}
}{m: map[string]struct {
	build string
	at    time.Time
}{}}

// latestBuild asks Steam for the newest build of the server's branch.
func (m *Manager) latestBuild(ctx context.Context, tpl templates.Template) (string, error) {
	if tpl.Kind != "steam" || tpl.SteamAppID == 0 {
		return "", errors.New("Kein Steam-Spiel")
	}
	branch := tpl.SteamBeta
	if branch == "" {
		branch = "public"
	}
	key := strconv.Itoa(tpl.SteamAppID) + "/" + branch
	latestCache.Lock()
	if c, ok := latestCache.m[key]; ok && time.Since(c.at) < 10*time.Minute {
		latestCache.Unlock()
		return c.build, nil
	}
	latestCache.Unlock()
	if !m.dk.ImageExists(ctx, SteamImage) {
		return "", errors.New("Die Steam-Laufzeit ist noch nicht gebaut")
	}
	login := "anonymous"
	var binds []string
	if tpl.SteamLogin {
		if login = m.steamUser(); login == "" {
			return "", errors.New("Kein Steam-Konto angemeldet")
		}
		binds = []string{m.SteamAuthDir() + ":/home/steam/Steam"}
	}
	ctx, cancel := context.WithTimeout(ctx, 3*time.Minute)
	defer cancel()
	cid, err := m.dk.Create(ctx, "gp-steaminfo-"+newID(), docker.ContainerConfig{
		Image:      SteamImage,
		Entrypoint: []string{"/opt/steamcmd/steamcmd.sh"},
		Cmd:        []string{"+login", login, "+app_info_update", "1", "+app_info_print", strconv.Itoa(tpl.SteamAppID), "+quit"},
		Labels:     map[string]string{"gamepanel.role": "steam-info"},
		HostConfig: docker.HostConfig{Binds: binds, RestartPolicy: docker.RestartPolicy{Name: "no"}},
	})
	if err != nil {
		return "", err
	}
	defer func() {
		rctx, rcancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer rcancel()
		_ = m.dk.Remove(rctx, cid)
	}()
	if err := m.dk.Start(ctx, cid); err != nil {
		return "", err
	}
	rc, err := m.dk.Logs(ctx, cid, true, -1, 0)
	if err != nil {
		return "", err
	}
	defer rc.Close()
	var b strings.Builder
	sc := bufio.NewScanner(io.LimitReader(rc, 8<<20))
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		b.WriteString(sc.Text())
		b.WriteByte('\n')
	}
	build := parseLatestBuild(b.String(), branch)
	if build == "" {
		if strings.Contains(strings.ToLower(b.String()), "login failure") || strings.Contains(b.String(), "No subscription") {
			return "", errors.New("Steam gibt die Versionsinfo ohne passendes Konto nicht heraus")
		}
		return "", errors.New("Steam hat keine Versionsnummer geliefert")
	}
	latestCache.Lock()
	latestCache.m[key] = struct {
		build string
		at    time.Time
	}{build, time.Now()}
	latestCache.Unlock()
	return build, nil
}

// UpdateState says whether a newer version is available.
type UpdateState struct {
	Installed string    `json:"installed,omitempty"`
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available"`
	Checked   time.Time `json:"checked"`
	Error     string    `json:"error,omitempty"`
}

// CheckUpdate compares the installed with the newest build.
func (m *Manager) CheckUpdate(ctx context.Context, id string) (UpdateState, error) {
	_, tpl, err := m.get(id)
	if err != nil {
		return UpdateState{}, err
	}
	st := UpdateState{Checked: time.Now(), Installed: m.installedBuild(id, tpl)}
	st.Latest, err = m.latestBuild(ctx, tpl)
	if err != nil {
		st.Error = err.Error()
		return st, nil
	}
	if st.Installed == "" {
		st.Error = "Die installierte Version ist unbekannt (noch nie vollständig installiert?)"
		return st, nil
	}
	st.Available = st.Latest != st.Installed
	return st, nil
}
