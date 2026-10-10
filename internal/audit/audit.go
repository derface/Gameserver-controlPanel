// Package audit keeps the protocol of who did what in the panel. Entries are
// JSON lines in <data>/audit.log; at 2 MB the file moves to audit.log.1, so
// the protocol never grows beyond about 4 MB.
package audit

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"time"
)

type Entry struct {
	Time   time.Time `json:"time"`
	User   string    `json:"user,omitempty"`
	IP     string    `json:"ip,omitempty"`
	Action string    `json:"action"`
	Target string    `json:"target,omitempty"`
	Detail string    `json:"detail,omitempty"`
	OK     bool      `json:"ok"`
	Error  string    `json:"error,omitempty"`
}

type Log struct {
	mu   sync.Mutex
	path string
	Max  int64
}

func Open(dir string) *Log {
	return &Log{path: filepath.Join(dir, "audit.log"), Max: 2 << 20}
}

func clip(s string, n int) string {
	s = strings.ReplaceAll(strings.TrimSpace(s), "\n", " ")
	if len(s) > n {
		// do not cut a UTF-8 character in half
		for n > 0 && s[n]&0xc0 == 0x80 {
			n--
		}
		s = s[:n] + "…"
	}
	return s
}

func (l *Log) Add(e Entry) {
	if e.Time.IsZero() {
		e.Time = time.Now()
	}
	e.Detail, e.Error, e.Target = clip(e.Detail, 300), clip(e.Error, 300), clip(e.Target, 120)
	b, err := json.Marshal(e)
	if err != nil {
		return
	}
	l.mu.Lock()
	defer l.mu.Unlock()
	if fi, err := os.Stat(l.path); err == nil && fi.Size()+int64(len(b)) > l.Max {
		_ = os.Rename(l.path, l.path+".1")
	}
	f, err := os.OpenFile(l.path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return
	}
	defer f.Close()
	_, _ = f.Write(append(b, '\n'))
}

// Recent returns up to n entries, newest first, that match the filter.
func (l *Log) Recent(n int, match func(Entry) bool) []Entry {
	l.mu.Lock()
	var all []Entry
	for _, p := range []string{l.path + ".1", l.path} {
		b, err := os.ReadFile(p)
		if err != nil {
			continue
		}
		sc := bufio.NewScanner(bytes.NewReader(b))
		sc.Buffer(make([]byte, 64<<10), 1<<20)
		for sc.Scan() {
			var e Entry
			if json.Unmarshal(sc.Bytes(), &e) == nil {
				all = append(all, e)
			}
		}
	}
	l.mu.Unlock()
	out := []Entry{}
	for i := len(all) - 1; i >= 0 && len(out) < n; i-- {
		if match == nil || match(all[i]) {
			out = append(out, all[i])
		}
	}
	return out
}
