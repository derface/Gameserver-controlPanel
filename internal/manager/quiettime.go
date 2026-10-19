package manager

import (
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Tasks "at the quietest time": instead of a fixed clock time the task runs
// in the hour in which, over the last 30 days, the fewest players were
// online. Until a week of data exists the task's own time is used. The
// hour is worked out once a day, so a task never jumps around within a day.

type quietEntry struct {
	day  string // date the hour was worked out
	hour int    // -1 = not enough data
	avg  float64
	days int
}

var quietCache struct {
	sync.Mutex
	m map[string]quietEntry
}

// QuietHour is the quietest hour of a server (local time) and the average
// number of players then; ok is false with less than 7 days of data.
func (m *Manager) QuietHour(id string, prefer int) (hour int, avg float64, ok bool) {
	now := time.Now()
	day := now.Format("2006-01-02")
	key := id + "/" + strconv.Itoa(prefer)
	quietCache.Lock()
	if e, hit := quietCache.m[key]; hit && e.day == day {
		quietCache.Unlock()
		return e.hour, e.avg, e.hour >= 0
	}
	quietCache.Unlock()

	statsMu.Lock()
	s := m.loadStats(id)
	statsMu.Unlock()
	e := quietFrom(s.Hours, now, prefer)
	e.day = day
	quietCache.Lock()
	if quietCache.m == nil {
		quietCache.m = map[string]quietEntry{}
	}
	quietCache.m[key] = e
	quietCache.Unlock()
	return e.hour, e.avg, e.hour >= 0
}

// quietFrom picks the hour with the lowest average player count. Hours
// that are about as quiet (within 0.1 players) count as equal; then the
// one closest to the preferred hour wins, so "4:00 or quieter" stays 4:00
// when it makes no difference.
func quietFrom(hours []hourBucket, now time.Time, prefer int) quietEntry {
	from := now.Add(-30 * 24 * time.Hour).Unix()
	var sum [24]float64
	var n [24]int
	days := map[string]bool{}
	for _, b := range hours {
		if b.H < from || b.N == 0 {
			continue
		}
		t := time.Unix(b.H, 0).In(now.Location())
		sum[t.Hour()] += float64(b.Sum) / float64(b.N)
		n[t.Hour()]++
		days[t.Format("2006-01-02")] = true
	}
	e := quietEntry{hour: -1, days: len(days)}
	if len(days) < 7 {
		return e
	}
	low := -1.0
	for h := 0; h < 24; h++ {
		if n[h] < 3 {
			continue
		}
		if avg := sum[h] / float64(n[h]); low < 0 || avg < low {
			low = avg
		}
	}
	if low < 0 {
		return e
	}
	dist := func(h int) int {
		d := h - prefer
		if d < 0 {
			d = -d
		}
		return min(d, 24-d)
	}
	for h := 0; h < 24; h++ {
		if n[h] < 3 {
			continue
		}
		if avg := sum[h] / float64(n[h]); avg <= low+0.1 && (e.hour < 0 || dist(h) < dist(e.hour)) {
			e.hour, e.avg = h, avg
		}
	}
	return e
}

// effective is the task with the clock time it will really run at.
func (m *Manager) effective(id string, t store.Task) store.Task {
	t, _ = m.effectiveOK(id, t)
	return t
}

// effectiveOK also says whether the quietest hour is known.
func (m *Manager) effectiveOK(id string, t store.Task) (store.Task, bool) {
	if !t.Quiet || t.EveryHours > 0 {
		return t, false
	}
	pref := 4
	if mm := hhmm.FindStringSubmatch(t.At); mm != nil {
		pref, _ = strconv.Atoi(mm[1])
	}
	h, _, ok := m.QuietHour(id, pref)
	if ok {
		t.At = fmt.Sprintf("%02d:00", h)
	}
	return t, ok
}

// nextRunFor is NextRun for a task of a server, including the quietest time.
// A quiet task runs at most once per day even when the hour moves later.
func (m *Manager) nextRunFor(id string, t store.Task) time.Time {
	if !t.Enabled {
		return time.Time{}
	}
	e := m.effective(id, t)
	from := e.LastRun
	if from.IsZero() {
		from = e.Created
	} else if e.Quiet && e.EveryHours == 0 {
		y, mo, d := from.Date()
		from = time.Date(y, mo, d, 23, 59, 59, 0, from.Location())
	}
	return nextRun(e, from)
}
