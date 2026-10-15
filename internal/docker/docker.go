// Package docker is a small client for the Docker Engine API over the
// local unix socket. It covers exactly what the panel needs and has no
// dependencies beyond the Go standard library.
package docker

import (
	"archive/tar"
	"bufio"
	"bytes"
	"context"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const apiVersion = "v1.41" // Docker 20.10+

type Client struct {
	sock string
	hc   *http.Client // for normal calls (with timeout)
	lc   *http.Client // for long-running streams (no timeout)
}

var ErrNotFound = errors.New("container nicht gefunden")

func New(sock string) *Client {
	dial := func(ctx context.Context, _, _ string) (net.Conn, error) {
		var d net.Dialer
		return d.DialContext(ctx, "unix", sock)
	}
	return &Client{
		sock: sock,
		hc:   &http.Client{Timeout: 60 * time.Second, Transport: &http.Transport{DialContext: dial}},
		lc:   &http.Client{Transport: &http.Transport{DialContext: dial}},
	}
}

func (c *Client) url(path string, q url.Values) string {
	u := "http://docker/" + apiVersion + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	return u
}

type apiError struct {
	Message string `json:"message"`
}

func readErr(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4096))
	var e apiError
	if json.Unmarshal(b, &e) == nil && e.Message != "" {
		if resp.StatusCode == http.StatusNotFound {
			return fmt.Errorf("%w: %s", ErrNotFound, e.Message)
		}
		return fmt.Errorf("docker: %s", e.Message)
	}
	if resp.StatusCode == http.StatusNotFound {
		return ErrNotFound
	}
	return fmt.Errorf("docker: HTTP %d", resp.StatusCode)
}

func (c *Client) do(ctx context.Context, hc *http.Client, method, path string, q url.Values, body any) (*http.Response, error) {
	var rd io.Reader
	ct := ""
	switch b := body.(type) {
	case nil:
	case io.Reader:
		rd = b
		ct = "application/x-tar"
	default:
		j, err := json.Marshal(b)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(j)
		ct = "application/json"
	}
	req, err := http.NewRequestWithContext(ctx, method, c.url(path, q), rd)
	if err != nil {
		return nil, err
	}
	if ct != "" {
		req.Header.Set("Content-Type", ct)
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Docker ist nicht erreichbar (%s): %w", c.sock, err)
	}
	if resp.StatusCode >= 400 {
		defer resp.Body.Close()
		return nil, readErr(resp)
	}
	return resp, nil
}

func (c *Client) simple(ctx context.Context, method, path string, q url.Values, body, out any) error {
	resp, err := c.do(ctx, c.hc, method, path, q, body)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if out != nil && resp.StatusCode != http.StatusNoContent {
		return json.NewDecoder(resp.Body).Decode(out)
	}
	_, _ = io.Copy(io.Discard, resp.Body)
	return nil
}

func (c *Client) Ping(ctx context.Context) error {
	resp, err := c.do(ctx, c.hc, "GET", "/_ping", nil, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// ---------- images ----------

func (c *Client) ImageExists(ctx context.Context, ref string) bool {
	err := c.simple(ctx, "GET", "/images/"+ref+"/json", nil, nil, &map[string]any{})
	return err == nil
}

type progressMsg struct {
	Status   string `json:"status"`
	Progress string `json:"progress"`
	Stream   string `json:"stream"`
	Error    string `json:"error"`
}

func consumeProgress(r io.Reader, cb func(string)) error {
	dec := json.NewDecoder(r)
	for {
		var m progressMsg
		if err := dec.Decode(&m); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		if m.Error != "" {
			return errors.New(m.Error)
		}
		if cb != nil {
			line := strings.TrimSpace(m.Stream)
			if line == "" {
				line = strings.TrimSpace(m.Status + " " + m.Progress)
			}
			if line != "" {
				cb(line)
			}
		}
	}
}

// Pull downloads an image, reporting progress lines to cb.
func (c *Client) Pull(ctx context.Context, ref string, cb func(string)) error {
	image, tag := ref, "latest"
	if i := strings.LastIndex(ref, ":"); i > strings.LastIndex(ref, "/") {
		image, tag = ref[:i], ref[i+1:]
	}
	resp, err := c.do(ctx, c.lc, "POST", "/images/create", url.Values{"fromImage": {image}, "tag": {tag}}, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return consumeProgress(resp.Body, cb)
}

// Build builds an image from in-memory files (a tiny build context).
func (c *Client) Build(ctx context.Context, tag string, files map[string]string, cb func(string)) error {
	var buf bytes.Buffer
	tw := tar.NewWriter(&buf)
	for name, content := range files {
		mode := int64(0o644)
		if strings.HasSuffix(name, ".sh") || !strings.Contains(name, ".") {
			mode = 0o755
		}
		if err := tw.WriteHeader(&tar.Header{Name: name, Mode: mode, Size: int64(len(content)), ModTime: time.Now()}); err != nil {
			return err
		}
		if _, err := tw.Write([]byte(content)); err != nil {
			return err
		}
	}
	if err := tw.Close(); err != nil {
		return err
	}
	resp, err := c.do(ctx, c.lc, "POST", "/build", url.Values{"t": {tag}, "rm": {"1"}, "forcerm": {"1"}}, &buf)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	return consumeProgress(resp.Body, cb)
}

// ---------- containers ----------

type PortBinding struct {
	HostIP   string `json:"HostIp,omitempty"`
	HostPort string `json:"HostPort"`
}

type RestartPolicy struct {
	Name string `json:"Name"`
}

type HostConfig struct {
	Binds         []string                 `json:"Binds,omitempty"`
	PortBindings  map[string][]PortBinding `json:"PortBindings,omitempty"`
	Memory        int64                    `json:"Memory,omitempty"`
	NanoCPUs      int64                    `json:"NanoCpus,omitempty"`
	RestartPolicy RestartPolicy            `json:"RestartPolicy"`
	Init          bool                     `json:"Init,omitempty"`
}

type ContainerConfig struct {
	Image        string              `json:"Image"`
	Entrypoint   []string            `json:"Entrypoint,omitempty"`
	Cmd          []string            `json:"Cmd,omitempty"`
	Env          []string            `json:"Env,omitempty"`
	User         string              `json:"User,omitempty"`
	WorkingDir   string              `json:"WorkingDir,omitempty"`
	Labels       map[string]string   `json:"Labels,omitempty"`
	ExposedPorts map[string]struct{} `json:"ExposedPorts,omitempty"`
	OpenStdin    bool                `json:"OpenStdin"`
	StdinOnce    bool                `json:"StdinOnce"`
	AttachStdin  bool                `json:"AttachStdin"`
	Tty          bool                `json:"Tty"`
	StopTimeout  int                 `json:"StopTimeout,omitempty"`
	StopSignal   string              `json:"StopSignal,omitempty"`
	HostConfig   HostConfig          `json:"HostConfig"`
}

func (c *Client) Create(ctx context.Context, name string, cfg ContainerConfig) (string, error) {
	var out struct {
		ID string `json:"Id"`
	}
	err := c.simple(ctx, "POST", "/containers/create", url.Values{"name": {name}}, cfg, &out)
	return out.ID, err
}

func (c *Client) Start(ctx context.Context, id string) error {
	return c.simple(ctx, "POST", "/containers/"+id+"/start", nil, nil, nil)
}

func (c *Client) Stop(ctx context.Context, id string, timeout int) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout+30)*time.Second)
	defer cancel()
	resp, err := c.do(ctx, c.lc, "POST", "/containers/"+id+"/stop", url.Values{"t": {fmt.Sprint(timeout)}}, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (c *Client) Restart(ctx context.Context, id string, timeout int) error {
	ctx, cancel := context.WithTimeout(ctx, time.Duration(timeout+60)*time.Second)
	defer cancel()
	resp, err := c.do(ctx, c.lc, "POST", "/containers/"+id+"/restart", url.Values{"t": {fmt.Sprint(timeout)}}, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

// SetRestartPolicy changes the restart policy of an existing container.
func (c *Client) SetRestartPolicy(ctx context.Context, id string, p RestartPolicy) error {
	return c.simple(ctx, "POST", "/containers/"+id+"/update", nil, map[string]any{"RestartPolicy": p}, nil)
}

func (c *Client) Remove(ctx context.Context, id string) error {
	err := c.simple(ctx, "DELETE", "/containers/"+id, url.Values{"force": {"1"}, "v": {"0"}}, nil, nil)
	if errors.Is(err, ErrNotFound) {
		return nil
	}
	return err
}

type State struct {
	Status    string `json:"Status"` // created, running, exited, ...
	Running   bool   `json:"Running"`
	ExitCode  int    `json:"ExitCode"`
	StartedAt string `json:"StartedAt"`
	OOMKilled bool   `json:"OOMKilled"`
	Error     string `json:"Error"`
}

func (c *Client) Inspect(ctx context.Context, id string) (State, error) {
	st, _, err := c.InspectImage(ctx, id)
	return st, err
}

// InspectImage also returns the image reference the container was created from.
func (c *Client) InspectImage(ctx context.Context, id string) (State, string, error) {
	var out struct {
		State  State `json:"State"`
		Config struct {
			Image string `json:"Image"`
		} `json:"Config"`
	}
	err := c.simple(ctx, "GET", "/containers/"+id+"/json", nil, nil, &out)
	return out.State, out.Config.Image, err
}

// InspectLabels returns the image and the labels a container was created with.
func (c *Client) InspectLabels(ctx context.Context, id string) (string, map[string]string, error) {
	var out struct {
		Config struct {
			Image  string            `json:"Image"`
			Labels map[string]string `json:"Labels"`
		} `json:"Config"`
	}
	err := c.simple(ctx, "GET", "/containers/"+id+"/json", nil, nil, &out)
	return out.Config.Image, out.Config.Labels, err
}

type Stats struct {
	CPUPercent float64 `json:"cpu_percent"`
	MemUsed    uint64  `json:"mem_used"`
	MemLimit   uint64  `json:"mem_limit"`
}

func (c *Client) Stats(ctx context.Context, id string) (Stats, error) {
	var raw struct {
		CPU struct {
			Usage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
			Online int    `json:"online_cpus"`
		} `json:"cpu_stats"`
		Pre struct {
			Usage struct {
				Total uint64 `json:"total_usage"`
			} `json:"cpu_usage"`
			System uint64 `json:"system_cpu_usage"`
		} `json:"precpu_stats"`
		Mem struct {
			Usage uint64            `json:"usage"`
			Limit uint64            `json:"limit"`
			Stats map[string]uint64 `json:"stats"`
		} `json:"memory_stats"`
	}
	if err := c.simple(ctx, "GET", "/containers/"+id+"/stats", url.Values{"stream": {"0"}}, nil, &raw); err != nil {
		return Stats{}, err
	}
	var s Stats
	cd := float64(raw.CPU.Usage.Total) - float64(raw.Pre.Usage.Total)
	sd := float64(raw.CPU.System) - float64(raw.Pre.System)
	if cd > 0 && sd > 0 {
		n := raw.CPU.Online
		if n == 0 {
			n = 1
		}
		s.CPUPercent = cd / sd * float64(n) * 100
	}
	s.MemUsed = raw.Mem.Usage
	if v, ok := raw.Mem.Stats["inactive_file"]; ok && v < s.MemUsed {
		s.MemUsed -= v
	}
	s.MemLimit = raw.Mem.Limit
	return s, nil
}

// Logs returns a reader of demultiplexed log lines (stdout+stderr).
// tail < 0 means all lines; since > 0 limits to lines after that unix time.
func (c *Client) Logs(ctx context.Context, id string, follow bool, tail int, since int64) (io.ReadCloser, error) {
	t := "all"
	if tail >= 0 {
		t = fmt.Sprint(tail)
	}
	q := url.Values{"stdout": {"1"}, "stderr": {"1"}, "tail": {t}}
	if since > 0 {
		q.Set("since", fmt.Sprint(since))
	}
	if follow {
		q.Set("follow", "1")
	}
	hc := c.hc
	if follow {
		hc = c.lc
	}
	resp, err := c.do(ctx, hc, "GET", "/containers/"+id+"/logs", q, nil)
	if err != nil {
		return nil, err
	}
	pr, pw := io.Pipe()
	go func() {
		defer resp.Body.Close()
		pw.CloseWithError(demux(resp.Body, pw))
	}()
	return pr, nil
}

// demux strips Docker's 8-byte stream headers (non-TTY containers).
func demux(r io.Reader, w io.Writer) error {
	br := bufio.NewReader(r)
	hdr := make([]byte, 8)
	for {
		if _, err := io.ReadFull(br, hdr); err != nil {
			if err == io.EOF {
				return nil
			}
			return err
		}
		n := binary.BigEndian.Uint32(hdr[4:])
		if _, err := io.CopyN(w, br, int64(n)); err != nil {
			return err
		}
	}
}

// SendStdin writes a line to the container's stdin via a hijacked attach.
func (c *Client) SendStdin(ctx context.Context, id, line string) error {
	var d net.Dialer
	conn, err := d.DialContext(ctx, "unix", c.sock)
	if err != nil {
		return err
	}
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(10 * time.Second))
	req := "POST /" + apiVersion + "/containers/" + id + "/attach?stream=1&stdin=1 HTTP/1.1\r\n" +
		"Host: docker\r\nConnection: Upgrade\r\nUpgrade: tcp\r\nContent-Type: application/vnd.docker.raw-stream\r\n\r\n"
	if _, err := io.WriteString(conn, req); err != nil {
		return err
	}
	br := bufio.NewReader(conn)
	status, err := br.ReadString('\n')
	if err != nil {
		return err
	}
	if !strings.Contains(status, " 101 ") && !strings.Contains(status, " 200 ") {
		return fmt.Errorf("docker attach: %s", strings.TrimSpace(status))
	}
	for {
		l, err := br.ReadString('\n')
		if err != nil {
			return err
		}
		if l == "\r\n" {
			break
		}
	}
	_, err = io.WriteString(conn, strings.TrimRight(line, "\r\n")+"\n")
	time.Sleep(150 * time.Millisecond)
	return err
}

// Exec runs a command inside a running container and returns its output.
func (c *Client) Exec(ctx context.Context, id string, cmd []string) (string, error) {
	var ex struct {
		ID string `json:"Id"`
	}
	err := c.simple(ctx, "POST", "/containers/"+id+"/exec", nil, map[string]any{
		"Cmd": cmd, "AttachStdout": true, "AttachStderr": true,
	}, &ex)
	if err != nil {
		return "", err
	}
	resp, err := c.do(ctx, c.hc, "POST", "/exec/"+ex.ID+"/start", nil, map[string]any{"Detach": false, "Tty": false})
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	var out bytes.Buffer
	if err := demux(io.LimitReader(resp.Body, 1<<20), &out); err != nil {
		return out.String(), err
	}
	return out.String(), nil
}

// IPs returns the container's addresses in its Docker networks.
func (c *Client) IPs(ctx context.Context, id string) ([]string, error) {
	var out struct {
		NetworkSettings struct {
			IPAddress string `json:"IPAddress"`
			Networks  map[string]struct {
				IPAddress string `json:"IPAddress"`
			} `json:"Networks"`
		} `json:"NetworkSettings"`
	}
	if err := c.simple(ctx, "GET", "/containers/"+id+"/json", nil, nil, &out); err != nil {
		return nil, err
	}
	var ips []string
	if ip := out.NetworkSettings.IPAddress; ip != "" {
		ips = append(ips, ip)
	}
	for _, n := range out.NetworkSettings.Networks {
		if n.IPAddress != "" && n.IPAddress != out.NetworkSettings.IPAddress {
			ips = append(ips, n.IPAddress)
		}
	}
	return ips, nil
}
