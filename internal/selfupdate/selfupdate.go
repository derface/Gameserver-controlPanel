// Package selfupdate replaces the panel binary with the newest GitHub
// release. The download is checked against the release's SHA256SUMS.
package selfupdate

import (
	"bufio"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"time"
)

// Repo is where releases come from (GP_REPO overrides it).
var Repo = "derface/Gameserver-controlPanel"

var apiBase = "https://api.github.com"

func init() {
	if r := strings.TrimSpace(os.Getenv("GP_REPO")); r != "" {
		Repo = r
	}
}

type Info struct {
	Current   string    `json:"current"`
	Latest    string    `json:"latest,omitempty"`
	Available bool      `json:"available"`
	DevBuild  bool      `json:"dev_build,omitempty"` // current version is not a release
	Notes     string    `json:"notes,omitempty"`
	URL       string    `json:"url,omitempty"` // release page
	Published time.Time `json:"published,omitempty"`
	Checked   time.Time `json:"checked"`
	Error     string    `json:"error,omitempty"`
	Source    string    `json:"source"` // repository page (AGPL: link to the source)
	asset     string
	sums      string
}

var client = &http.Client{Timeout: 30 * time.Second}

func get(ctx context.Context, url string, accept string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, "GET", url, nil)
	if err != nil {
		return nil, err
	}
	if accept != "" {
		req.Header.Set("Accept", accept)
	}
	req.Header.Set("User-Agent", "bytewise-panel")
	if t := os.Getenv("GP_GITHUB_TOKEN"); t != "" {
		req.Header.Set("Authorization", "Bearer "+t)
	}
	return client.Do(req)
}

// Check asks GitHub for the newest release.
func Check(ctx context.Context, current string) Info {
	in := Info{Current: current, Checked: time.Now(), Source: "https://github.com/" + Repo}
	resp, err := get(ctx, apiBase+"/repos/"+Repo+"/releases/latest", "application/vnd.github+json")
	if err != nil {
		in.Error = "GitHub ist nicht erreichbar"
		return in
	}
	defer resp.Body.Close()
	if resp.StatusCode == 404 {
		in.Error = "Keine veröffentlichte Version gefunden (ist das Repository öffentlich und gibt es schon ein Release?)"
		return in
	}
	if resp.StatusCode != 200 {
		in.Error = fmt.Sprintf("GitHub meldet HTTP %d", resp.StatusCode)
		return in
	}
	var r struct {
		Tag       string    `json:"tag_name"`
		Body      string    `json:"body"`
		HTMLURL   string    `json:"html_url"`
		Published time.Time `json:"published_at"`
		Assets    []struct {
			Name string `json:"name"`
			URL  string `json:"browser_download_url"`
		} `json:"assets"`
	}
	if err := json.NewDecoder(io.LimitReader(resp.Body, 4<<20)).Decode(&r); err != nil {
		in.Error = "Antwort von GitHub unlesbar"
		return in
	}
	in.Latest, in.Notes, in.URL, in.Published = r.Tag, strings.TrimSpace(r.Body), r.HTMLURL, r.Published
	want := "gamepanel-linux-" + runtime.GOARCH
	for _, a := range r.Assets {
		switch a.Name {
		case want:
			in.asset = a.URL
		case "SHA256SUMS":
			in.sums = a.URL
		}
	}
	cur, okC := parse(current)
	lat, okL := parse(r.Tag)
	in.DevBuild = !okC || strings.Contains(current, "-")
	in.Available = okL && (!okC || newer(lat, cur))
	if okC && !strings.Contains(current, "-") && !newer(lat, cur) {
		in.Available = false
	}
	if in.Available && in.asset == "" {
		in.Error = "Das Release enthält keine Datei für diese Plattform (" + want + ")"
		in.Available = false
	}
	return in
}

var semver = regexp.MustCompile(`^v?(\d+)\.(\d+)\.(\d+)`)

func parse(v string) ([3]int, bool) {
	m := semver.FindStringSubmatch(strings.TrimSpace(v))
	if m == nil {
		return [3]int{}, false
	}
	var out [3]int
	for i := range out {
		out[i], _ = strconv.Atoi(m[i+1])
	}
	return out, true
}

func newer(a, b [3]int) bool {
	for i := range a {
		if a[i] != b[i] {
			return a[i] > b[i]
		}
	}
	return false
}

// Apply downloads the release binary, checks it and replaces exe.
func Apply(ctx context.Context, in Info, exe string) error {
	if in.asset == "" {
		return errors.New("Kein Download für diese Version")
	}
	if in.sums == "" {
		return errors.New("Das Release hat keine Prüfsummen (SHA256SUMS); aus Sicherheitsgründen wird nicht installiert")
	}
	want, err := expectedSum(ctx, in.sums, "gamepanel-linux-"+runtime.GOARCH)
	if err != nil {
		return err
	}
	dir := filepath.Dir(exe)
	tmp, err := os.CreateTemp(dir, ".gamepanel-new-*")
	if err != nil {
		return fmt.Errorf("Kann in %s nicht schreiben: %w", dir, err)
	}
	defer os.Remove(tmp.Name())
	resp, err := get(ctx, in.asset, "application/octet-stream")
	if err != nil {
		tmp.Close()
		return errors.New("Download fehlgeschlagen")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		tmp.Close()
		return fmt.Errorf("Download fehlgeschlagen: HTTP %d", resp.StatusCode)
	}
	h := sha256.New()
	if _, err := io.Copy(io.MultiWriter(tmp, h), io.LimitReader(resp.Body, 300<<20)); err != nil {
		tmp.Close()
		return fmt.Errorf("Download abgebrochen: %w", err)
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	if got := hex.EncodeToString(h.Sum(nil)); got != want {
		return errors.New("Prüfsumme stimmt nicht: Download beschädigt oder verändert, nichts installiert")
	}
	if err := os.Chmod(tmp.Name(), 0o755); err != nil {
		return err
	}
	// keep the old binary next to it for a manual way back
	_ = os.Remove(exe + ".old")
	if err := os.Link(exe, exe+".old"); err != nil {
		_ = copyFile(exe, exe+".old")
	}
	return os.Rename(tmp.Name(), exe)
}

func expectedSum(ctx context.Context, url, name string) (string, error) {
	resp, err := get(ctx, url, "application/octet-stream")
	if err != nil {
		return "", errors.New("Prüfsummen nicht ladbar")
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		return "", fmt.Errorf("Prüfsummen nicht ladbar: HTTP %d", resp.StatusCode)
	}
	sc := bufio.NewScanner(io.LimitReader(resp.Body, 1<<20))
	for sc.Scan() {
		f := strings.Fields(sc.Text())
		if len(f) == 2 && strings.TrimPrefix(f[1], "*") == name && len(f[0]) == 64 {
			return strings.ToLower(f[0]), nil
		}
	}
	return "", errors.New("Keine Prüfsumme für " + name + " im Release")
}

func copyFile(a, b string) error {
	in, err := os.Open(a)
	if err != nil {
		return err
	}
	defer in.Close()
	out, err := os.OpenFile(b, os.O_CREATE|os.O_WRONLY|os.O_TRUNC, 0o755)
	if err != nil {
		return err
	}
	if _, err := io.Copy(out, in); err != nil {
		out.Close()
		return err
	}
	return out.Close()
}
