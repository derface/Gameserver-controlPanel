// Package hoststats reads CPU, memory and disk usage of the host.
package hoststats

import (
	"bufio"
	"context"
	"os"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"
)

type Snapshot struct {
	CPUPercent float64 `json:"cpu_percent"`
	Cores      int     `json:"cores"`
	MemTotalMB float64 `json:"mem_total_mb"`
	MemUsedMB  float64 `json:"mem_used_mb"`
	DiskTotalG float64 `json:"disk_total_gb"`
	DiskUsedG  float64 `json:"disk_used_gb"`
	Hostname   string  `json:"hostname"`
	Uptime     int64   `json:"uptime_s"`
}

type Sampler struct {
	dir string
	mu  sync.RWMutex
	cur Snapshot
}

func New(dataDir string) *Sampler { return &Sampler{dir: dataDir} }

func readCPU() (idle, total uint64) {
	f, err := os.Open("/proc/stat")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	if sc.Scan() {
		fields := strings.Fields(sc.Text())
		for i, v := range fields[1:] {
			n, _ := strconv.ParseUint(v, 10, 64)
			total += n
			if i == 3 || i == 4 { // idle + iowait
				idle += n
			}
		}
	}
	return
}

func readMem() (total, avail float64) {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0, 0
	}
	defer f.Close()
	sc := bufio.NewScanner(f)
	for sc.Scan() {
		fields := strings.Fields(sc.Text())
		if len(fields) < 2 {
			continue
		}
		v, _ := strconv.ParseFloat(fields[1], 64)
		switch fields[0] {
		case "MemTotal:":
			total = v / 1024
		case "MemAvailable:":
			avail = v / 1024
		}
	}
	return
}

func cores() int {
	b, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return 1
	}
	n := strings.Count(string(b), "processor\t:")
	if n == 0 {
		return 1
	}
	return n
}

func (s *Sampler) Run(ctx context.Context) {
	pi, pt := readCPU()
	host, _ := os.Hostname()
	nc := cores()
	t := time.NewTicker(2 * time.Second)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
		}
		i, tt := readCPU()
		snap := Snapshot{Hostname: host, Cores: nc}
		if tt > pt {
			snap.CPUPercent = 100 * (1 - float64(i-pi)/float64(tt-pt))
		}
		pi, pt = i, tt
		tot, av := readMem()
		snap.MemTotalMB, snap.MemUsedMB = tot, tot-av
		var fs syscall.Statfs_t
		if syscall.Statfs(s.dir, &fs) == nil {
			bs := float64(fs.Bsize)
			snap.DiskTotalG = float64(fs.Blocks) * bs / 1e9
			snap.DiskUsedG = float64(fs.Blocks-fs.Bavail) * bs / 1e9
		}
		if b, err := os.ReadFile("/proc/uptime"); err == nil {
			if f, err := strconv.ParseFloat(strings.Fields(string(b))[0], 64); err == nil {
				snap.Uptime = int64(f)
			}
		}
		s.mu.Lock()
		s.cur = snap
		s.mu.Unlock()
	}
}

func (s *Sampler) Get() Snapshot {
	s.mu.RLock()
	defer s.mu.RUnlock()
	return s.cur
}
