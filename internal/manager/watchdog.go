package manager

import (
	"context"
	"fmt"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// Crash watchdog. Docker restarts a crashed server by itself (restart policy
// "unless-stopped"); the panel notices that the container's run changed
// without the panel asking for it, logs it, and stops a server that keeps
// crashing. Optionally a server whose player query stops answering is
// restarted.

const (
	crashWindow = 10 * time.Minute
	crashLimit  = 3
)

var hangUnit = time.Minute // tests shorten it

func (m *Manager) watchdog(sv store.Server, tpl templates.Template, prev, l *live) {
	if prev != nil {
		l.haltMsg = prev.haltMsg
	}
	if l.State.Running {
		l.haltMsg = "" // started again; Docker does not restart a server we stopped
	} else if l.haltMsg != "" {
		l.Phase, l.Msg = "error", l.haltMsg
	}
	if prev != nil && prev.State.StartedAt != "" {
		run := prev.State.StartedAt
		changed := l.State.StartedAt != run || prev.State.Running != l.State.Running
		if changed && !m.consumeExpect(sv.ID) && l.countedRun != run {
			if why := crashReason(prev, l); why != "" {
				m.recordCrash(sv, tpl, l, run, why)
			}
		}
	}
	m.hangCheck(sv, l)
}

func crashReason(prev, l *live) string {
	switch {
	case prev.State.Running && l.State.StartedAt != prev.State.StartedAt:
		return "wurde unerwartet beendet" // Docker already started the next run
	case prev.State.Running && !l.State.Running:
		code := l.State.ExitCode
		switch {
		case l.State.OOMKilled:
			return "hatte zu wenig RAM"
		case code == 137:
			return "wurde vom System beendet (oft zu wenig RAM)"
		case code != 0 && code != 130 && code != 143:
			return fmt.Sprintf("ist abgestürzt (Code %d)", code)
		}
	}
	return ""
}

func (m *Manager) recordCrash(sv store.Server, tpl templates.Template, l *live, run, why string) {
	l.countedRun = run
	defer m.AutoDiagnose(sv.ID, "Server "+why)
	now := time.Now()
	var keep []time.Time
	for _, t := range l.crashes {
		if now.Sub(t) < crashWindow {
			keep = append(keep, t)
		}
	}
	l.crashes = append(keep, now)
	switch {
	case sv.Watch.NoAutoRestart:
		m.Event(sv.ID, "error", "Server "+why+". Automatischer Neustart ist ausgeschaltet.")
	case len(l.crashes) >= crashLimit:
		l.haltMsg = fmt.Sprintf("%d Abstürze in %d Minuten, der Server wurde angehalten. Details in der Konsole.", len(l.crashes), int(crashWindow.Minutes()))
		l.Phase, l.Msg = "error", l.haltMsg
		l.crashes = nil
		m.Event(sv.ID, "error", "Server "+why+". "+l.haltMsg)
		go func(id, cid string, timeout int) {
			m.expect(id)
			ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
			defer cancel()
			_ = m.dk.Stop(ctx, cid, timeout)
		}(sv.ID, sv.ContainerID, tpl.StopTimeout)
	default:
		m.Event(sv.ID, "warn", "Server "+why+" und wird automatisch neu gestartet.")
	}
}

// hangCheck restarts a server that answered the player query in this run
// but has not answered for a while.
func (m *Manager) hangCheck(sv store.Server, l *live) {
	if !sv.Watch.HangRestart || l.Phase != "running" || l.answeredAt.IsZero() {
		return
	}
	if l.answeredRun != l.State.StartedAt || l.hangRun == l.State.StartedAt {
		return
	}
	lim := sv.Watch.HangMinutes
	if lim < 2 {
		lim = 5
	}
	if time.Since(l.answeredAt) <= time.Duration(lim)*hangUnit || m.expected(sv.ID) {
		return
	}
	l.hangRun = l.State.StartedAt
	m.Event(sv.ID, "warn", fmt.Sprintf("Server antwortet seit %d Minuten nicht mehr und wird neu gestartet.", lim))
	m.AutoDiagnose(sv.ID, fmt.Sprintf("Server hing %d Minuten", lim))
	go func(id string) {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		defer cancel()
		_ = m.Restart(ctx, id)
	}(sv.ID)
}
