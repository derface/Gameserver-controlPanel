package manager

import (
	"context"
	"errors"
	"regexp"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/stream"
)

// Streaming: Twitch and YouTube viewers trigger things in a game. A chat
// command like !gewitter, a channel point reward, bits, a super chat, a new
// subscriber or a raid runs console commands on a server and/or tells all
// players. Viewer text only reaches the console after strict cleaning.

type streamConn struct {
	On  bool   `json:"on"`
	OK  bool   `json:"ok"`
	Msg string `json:"msg,omitempty"`
}

// StreamFeedItem is one viewer event as the UI shows it.
type StreamFeedItem struct {
	Time   time.Time `json:"time"`
	Source string    `json:"source"`
	Kind   string    `json:"kind"`
	User   string    `json:"user"`
	Text   string    `json:"text,omitempty"`
	Amount int       `json:"amount,omitempty"`
	Rule   string    `json:"rule,omitempty"`
	Result string    `json:"result,omitempty"`
}

type streamState struct {
	mu       sync.Mutex
	cancel   context.CancelFunc
	twitch   streamConn
	youtube  streamConn
	feed     []StreamFeedItem
	lastRule map[string]time.Time
	lastUser map[string]time.Time
	rewards  map[string]StreamFeedItem // channel point rewards seen, by id
}

var st streamState

// StreamView is what the settings page shows.
type StreamView struct {
	Twitch  streamConn       `json:"twitch"`
	YouTube streamConn       `json:"youtube"`
	Feed    []StreamFeedItem `json:"feed"`
	Rewards []StreamFeedItem `json:"rewards"` // id in Rule
}

func (m *Manager) StreamState() StreamView {
	st.mu.Lock()
	defer st.mu.Unlock()
	v := StreamView{Twitch: st.twitch, YouTube: st.youtube, Feed: append([]StreamFeedItem{}, st.feed...), Rewards: []StreamFeedItem{}}
	for id, r := range st.rewards {
		r.Rule = id
		v.Rewards = append(v.Rewards, r)
	}
	return v
}

// ApplyStreaming (re)connects to Twitch and YouTube after a change.
func (m *Manager) ApplyStreaming() {
	var c store.Streaming
	m.st.Read(func(d *store.Data) { c = d.Stream })
	st.mu.Lock()
	if st.cancel != nil {
		st.cancel()
	}
	m.testMu.Lock()
	base := m.baseCtx
	m.testMu.Unlock()
	if base == nil {
		base = context.Background()
	}
	ctx, cancel := context.WithCancel(base)
	st.cancel = cancel
	st.twitch = streamConn{On: c.TwitchOn && c.TwitchChannel != ""}
	st.youtube = streamConn{On: c.YTOn && c.YTKey != "" && c.YTVideo != ""}
	if st.lastRule == nil {
		st.lastRule, st.lastUser, st.rewards = map[string]time.Time{}, map[string]time.Time{}, map[string]StreamFeedItem{}
	}
	st.mu.Unlock()
	if c.TwitchOn && c.TwitchChannel != "" {
		go stream.Twitch(ctx, c.TwitchChannel, m.onViewer, func(ok bool, msg string) {
			st.mu.Lock()
			st.twitch.OK, st.twitch.Msg = ok, msg
			st.mu.Unlock()
		})
	}
	if c.YTOn && c.YTKey != "" && c.YTVideo != "" {
		if id, err := stream.VideoID(c.YTVideo); err == nil {
			go stream.YouTube(ctx, c.YTKey, id, m.onViewer, func(ok bool, msg string) {
				st.mu.Lock()
				st.youtube.OK, st.youtube.Msg = ok, msg
				st.mu.Unlock()
			})
		}
	}
}

func addFeed(it StreamFeedItem) {
	st.mu.Lock()
	defer st.mu.Unlock()
	st.feed = append(st.feed, it)
	if len(st.feed) > 40 {
		st.feed = st.feed[len(st.feed)-40:]
	}
}

var (
	reCmdWord = regexp.MustCompile(`^[a-z0-9äöüß_-]{1,30}$`)
	reUser    = regexp.MustCompile(`[^A-Za-z0-9_]`)
	reText    = regexp.MustCompile(`[^\p{L}\p{N} .,!?_-]`)
)

// safe values for console commands: names letters/digits/_, text without
// quotes, slashes, brackets, semicolons or line breaks
func cleanViewer(s string) string { return trimRunes(reUser.ReplaceAllString(s, ""), 25) }
func cleanViewerText(s string) string {
	return trimRunes(strings.Join(strings.Fields(reText.ReplaceAllString(s, " ")), " "), 60)
}
func trimRunes(s string, n int) string {
	if r := []rune(s); len(r) > n {
		return string(r[:n])
	}
	return s
}

// ValidateStreamRule checks and cleans a rule.
func ValidateStreamRule(r *store.StreamRule) error {
	switch r.Source {
	case "twitch", "youtube", "any":
	default:
		r.Source = "any"
	}
	r.Match = strings.ToLower(strings.TrimPrefix(strings.TrimSpace(r.Match), "!"))
	switch r.Trigger {
	case "command":
		if !reCmdWord.MatchString(r.Match) {
			return errors.New("Chat-Befehl bitte als ein Wort angeben, z. B. gewitter (ohne !)")
		}
	case "reward":
		if r.Match == "" {
			return errors.New("Welche Kanalpunkte-Belohnung? Einmal einlösen, dann unten auswählen")
		}
		r.Source = "twitch"
	case "bits":
		r.Source = "twitch"
	case "superchat":
		r.Source = "youtube"
	case "sub", "raid":
	default:
		return errors.New("Unbekannter Auslöser")
	}
	switch r.Who {
	case "subs", "mods":
	default:
		r.Who = "all"
	}
	r.Cooldown = max(0, min(r.Cooldown, 86400))
	r.UserCooldown = max(0, min(r.UserCooldown, 86400))
	r.MinAmount = max(0, r.MinAmount)
	var cmds []string
	for _, c := range r.Commands {
		c = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(c))
		if c == "" {
			continue
		}
		if len(c) > 300 {
			return errors.New("Ein Befehl ist zu lang")
		}
		if strings.HasPrefix(c, "{{text}}") || strings.HasPrefix(c, "{{user}}") {
			// the viewer would choose the command itself
			return errors.New("Ein Befehl darf nicht mit {{text}} oder {{user}} beginnen")
		}
		cmds = append(cmds, c)
	}
	if len(cmds) > 10 {
		return errors.New("Höchstens 10 Befehle je Regel")
	}
	r.Commands = cmds
	r.Say = strings.TrimSpace(strings.NewReplacer("\r", " ", "\n", " ").Replace(r.Say))
	if len(r.Say) > 200 {
		return errors.New("Die Nachricht ist zu lang")
	}
	if len(cmds) == 0 && r.Say == "" {
		return errors.New("Bitte mindestens einen Befehl oder eine Nachricht angeben")
	}
	if r.Server == "" {
		return errors.New("Auf welchem Server?")
	}
	return nil
}

// streamMatch says whether rule r reacts to e, and returns {{text}}.
func streamMatch(r store.StreamRule, e stream.Event) (bool, string) {
	if !r.Enabled || (r.Source != "any" && r.Source != e.Source) {
		return false, ""
	}
	if r.Who == "subs" && !e.Sub && !e.Mod || r.Who == "mods" && !e.Mod {
		return false, ""
	}
	switch r.Trigger {
	case "command":
		f := strings.Fields(e.Text)
		if len(f) == 0 || !strings.EqualFold(f[0], "!"+r.Match) {
			return false, ""
		}
		return true, strings.Join(f[1:], " ")
	case "reward":
		return e.Kind == "reward" && e.RewardID == r.Match, e.Text
	case "bits", "superchat", "raid":
		return e.Kind == r.Trigger && e.Amount >= max(1, r.MinAmount), e.Text
	case "sub":
		return e.Kind == "sub", e.Text
	}
	return false, ""
}

func (m *Manager) onViewer(e stream.Event) {
	it := StreamFeedItem{Time: e.Time, Source: e.Source, Kind: e.Kind, User: e.User, Text: trimRunes(e.Text, 120), Amount: e.Amount}
	if e.Kind == "reward" {
		st.mu.Lock()
		if _, ok := st.rewards[e.RewardID]; !ok && len(st.rewards) < 50 {
			st.rewards[e.RewardID] = it
		}
		st.mu.Unlock()
	}
	var c store.Streaming
	m.st.Read(func(d *store.Data) {
		c = d.Stream
		c.Rules = append([]store.StreamRule(nil), d.Stream.Rules...)
	})
	matched := false
	for _, r := range c.Rules {
		ok, text := streamMatch(r, e)
		if !ok {
			continue
		}
		matched = true
		res := m.runStreamRule(c, r, e, text)
		item := it
		item.Rule, item.Result = streamRuleName(r), res
		addFeed(item)
	}
	// plain chat that triggers nothing stays out of the feed (too much noise)
	if !matched && e.Kind != "chat" {
		addFeed(it)
	}
}

func streamRuleName(r store.StreamRule) string {
	if r.Name != "" {
		return r.Name
	}
	switch r.Trigger {
	case "command":
		return "!" + r.Match
	case "reward":
		return "Kanalpunkte"
	case "bits":
		return "Bits"
	case "superchat":
		return "Super Chat"
	case "sub":
		return "Abo"
	case "raid":
		return "Raid"
	}
	return r.Trigger
}

// runStreamRule checks the cooldowns and runs the commands; it returns
// what happened, for the feed.
func (m *Manager) runStreamRule(c store.Streaming, r store.StreamRule, e stream.Event, text string) string {
	now := time.Now()
	userKey := r.ID + "|" + e.Source + "|" + strings.ToLower(e.User)
	st.mu.Lock()
	if last, ok := st.lastRule[r.ID]; ok && r.Cooldown > 0 && now.Sub(last) < time.Duration(r.Cooldown)*time.Second {
		st.mu.Unlock()
		return "Abklingzeit, noch " + strconv.Itoa(int((time.Duration(r.Cooldown)*time.Second-now.Sub(last)).Seconds())+1) + " s"
	}
	if last, ok := st.lastUser[userKey]; ok && r.UserCooldown > 0 && now.Sub(last) < time.Duration(r.UserCooldown)*time.Second {
		st.mu.Unlock()
		return "Abklingzeit für diesen Zuschauer"
	}
	st.lastRule[r.ID], st.lastUser[userKey] = now, now
	if len(st.lastUser) > 5000 {
		st.lastUser = map[string]time.Time{}
	}
	st.mu.Unlock()
	if !m.isUp(r.Server) {
		return "Server läuft nicht"
	}
	repl := strings.NewReplacer("{{user}}", cleanViewer(e.User), "{{text}}", cleanViewerText(text),
		"{{amount}}", strconv.Itoa(e.Amount), "{{streamer}}", cleanViewer(c.Streamer))
	ran := 0
	for _, cmd := range r.Commands {
		line := strings.TrimSpace(repl.Replace(cmd))
		if line == "" {
			continue
		}
		ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
		_, err := m.Command(ctx, r.Server, line)
		cancel()
		if err != nil {
			return "Fehler: " + err.Error()
		}
		ran++
	}
	if r.Say != "" {
		m.Broadcast(r.Server, repl.Replace(r.Say))
	}
	_ = m.st.Update(func(d *store.Data) error {
		for i := range d.Stream.Rules {
			if d.Stream.Rules[i].ID == r.ID {
				d.Stream.Rules[i].LastAt = now
			}
		}
		return nil
	})
	if ran == 0 {
		return "Nachricht im Spiel"
	}
	return "ausgeführt (" + strconv.Itoa(ran) + " Befehl" + map[bool]string{true: "", false: "e"}[ran == 1] + ")"
}

// TestStreamRule runs a rule as if a viewer had triggered it.
func (m *Manager) TestStreamRule(r store.StreamRule) string {
	var c store.Streaming
	m.st.Read(func(d *store.Data) { c = d.Stream })
	st.mu.Lock()
	if st.lastRule == nil {
		st.lastRule, st.lastUser, st.rewards = map[string]time.Time{}, map[string]time.Time{}, map[string]StreamFeedItem{}
	}
	delete(st.lastRule, r.ID)
	st.mu.Unlock()
	e := stream.Event{Source: "test", Kind: r.Trigger, User: "TestZuschauer", Text: "Test", Amount: max(1, r.MinAmount), Time: time.Now()}
	res := m.runStreamRule(c, r, e, "Test")
	addFeed(StreamFeedItem{Time: e.Time, Source: "test", Kind: r.Trigger, User: e.User, Rule: streamRuleName(r), Result: res})
	return res
}
