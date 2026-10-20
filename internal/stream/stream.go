// Package stream reads Twitch chat (anonymously via IRC) and YouTube live
// chat (Data API) and turns them into viewer events: chat messages,
// channel point rewards, bits, super chats, subscriptions and raids.
package stream

import (
	"bufio"
	"context"
	"crypto/tls"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Event is one thing a viewer did.
type Event struct {
	Source   string    `json:"source"` // twitch | youtube
	Kind     string    `json:"kind"`   // chat | reward | bits | superchat | sub | raid
	User     string    `json:"user"`
	Text     string    `json:"text,omitempty"`
	Amount   int       `json:"amount,omitempty"` // bits, cents, raid viewers, months
	Sub      bool      `json:"sub,omitempty"`
	Mod      bool      `json:"mod,omitempty"` // moderator or the streamer
	RewardID string    `json:"reward_id,omitempty"`
	Time     time.Time `json:"time"`
}

var reChannel = regexp.MustCompile(`^[a-z0-9_]{3,25}$`)

// CleanChannel turns "https://twitch.tv/Name" or "#name" into "name".
func CleanChannel(s string) (string, error) {
	s = strings.ToLower(strings.TrimSpace(s))
	if i := strings.LastIndex(s, "twitch.tv/"); i >= 0 {
		s = s[i+len("twitch.tv/"):]
	}
	s = strings.Trim(strings.TrimPrefix(s, "#"), "/ ")
	if !reChannel.MatchString(s) {
		return "", errors.New("Twitch-Kanal bitte als Name angeben, z. B. meinkanal")
	}
	return s, nil
}

// TwitchAddr is where the chat lives; tests point it elsewhere.
var TwitchAddr = "irc.chat.twitch.tv:6697"
var twitchTLS = true

// Twitch reads the chat of channel until ctx ends, calling on for each
// event and status for connection changes. It reconnects by itself.
func Twitch(ctx context.Context, channel string, on func(Event), status func(ok bool, msg string)) {
	wait := time.Second
	for ctx.Err() == nil {
		err := twitchOnce(ctx, channel, on, status)
		if ctx.Err() != nil {
			return
		}
		msg := "Verbindung getrennt"
		if err != nil {
			msg = err.Error()
		}
		status(false, msg)
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, time.Minute)
	}
}

func twitchOnce(ctx context.Context, channel string, on func(Event), status func(bool, string)) error {
	d := net.Dialer{Timeout: 10 * time.Second}
	var conn net.Conn
	var err error
	if twitchTLS {
		conn, err = tls.DialWithDialer(&d, "tcp", TwitchAddr, &tls.Config{ServerName: strings.Split(TwitchAddr, ":")[0]})
	} else {
		conn, err = d.DialContext(ctx, "tcp", TwitchAddr)
	}
	if err != nil {
		return fmt.Errorf("Twitch nicht erreichbar (%v)", err)
	}
	defer conn.Close()
	go func() { <-ctx.Done(); conn.Close() }()
	nick := fmt.Sprintf("justinfan%d", 10000+rand.Intn(80000)) // anonymous, read only
	fmt.Fprintf(conn, "CAP REQ :twitch.tv/tags twitch.tv/commands\r\nPASS SCHMOOPIIE\r\nNICK %s\r\nJOIN #%s\r\n", nick, channel)
	sc := bufio.NewScanner(conn)
	sc.Buffer(make([]byte, 64*1024), 1<<20)
	joined := false
	for sc.Scan() {
		_ = conn.SetReadDeadline(time.Now().Add(6 * time.Minute)) // Twitch pings every ~5 min
		line := sc.Text()
		if strings.HasPrefix(line, "PING") {
			fmt.Fprintf(conn, "PONG%s\r\n", strings.TrimPrefix(line, "PING"))
			continue
		}
		tags, cmd, args, text := parseIRC(line)
		switch cmd {
		case "JOIN", "ROOMSTATE":
			if !joined {
				joined = true
				status(true, "verbunden mit #"+channel)
			}
		case "RECONNECT":
			return errors.New("Twitch bittet um neue Verbindung")
		case "NOTICE":
			if strings.Contains(text, "failed") || strings.Contains(text, "Improperly") {
				return errors.New("Twitch: " + text)
			}
		case "PRIVMSG":
			if len(args) == 0 {
				continue
			}
			on(twitchEvent(tags, text))
		case "USERNOTICE":
			if e, ok := twitchNotice(tags, text); ok {
				on(e)
			}
		}
	}
	if err := sc.Err(); err != nil {
		return err
	}
	return io.EOF
}

// parseIRC splits "@tags :prefix CMD args :text".
func parseIRC(line string) (map[string]string, string, []string, string) {
	tags := map[string]string{}
	if strings.HasPrefix(line, "@") {
		t, rest, _ := strings.Cut(line[1:], " ")
		for _, kv := range strings.Split(t, ";") {
			k, v, _ := strings.Cut(kv, "=")
			tags[k] = unescapeTag(v)
		}
		line = rest
	}
	if strings.HasPrefix(line, ":") {
		_, rest, _ := strings.Cut(line, " ")
		line = rest
	}
	head, text, _ := strings.Cut(line, " :")
	f := strings.Fields(head)
	if len(f) == 0 {
		return tags, "", nil, text
	}
	return tags, f[0], f[1:], text
}

func unescapeTag(v string) string {
	return strings.NewReplacer(`\s`, " ", `\:`, ";", `\\`, `\`, `\r`, "", `\n`, "").Replace(v)
}

func twitchUser(tags map[string]string) (name string, sub, mod bool) {
	name = tags["display-name"]
	if name == "" {
		name = tags["login"]
	}
	badges := tags["badges"]
	sub = tags["subscriber"] == "1" || strings.Contains(badges, "subscriber/") || strings.Contains(badges, "founder/")
	mod = tags["mod"] == "1" || strings.Contains(badges, "broadcaster/") || strings.Contains(badges, "moderator/")
	return
}

func twitchEvent(tags map[string]string, text string) Event {
	name, sub, mod := twitchUser(tags)
	e := Event{Source: "twitch", Kind: "chat", User: name, Text: text, Sub: sub, Mod: mod, Time: time.Now()}
	if id := tags["custom-reward-id"]; id != "" {
		e.Kind, e.RewardID = "reward", id
	} else if b, err := strconv.Atoi(tags["bits"]); err == nil && b > 0 {
		e.Kind, e.Amount = "bits", b
	}
	return e
}

func twitchNotice(tags map[string]string, text string) (Event, bool) {
	name, sub, mod := twitchUser(tags)
	e := Event{Source: "twitch", User: name, Text: text, Sub: sub, Mod: mod, Time: time.Now()}
	switch tags["msg-id"] {
	case "sub", "resub", "subgift", "submysterygift", "giftpaidupgrade":
		e.Kind, e.Sub = "sub", true
		e.Amount, _ = strconv.Atoi(tags["msg-param-cumulative-months"])
		if tags["msg-id"] == "submysterygift" {
			e.Amount, _ = strconv.Atoi(tags["msg-param-mass-gift-count"])
		}
	case "raid":
		e.Kind = "raid"
		e.User = tags["msg-param-displayName"]
		e.Amount, _ = strconv.Atoi(tags["msg-param-viewerCount"])
	default:
		return e, false
	}
	return e, true
}

// ---------- YouTube ----------

var reVideo = regexp.MustCompile(`^[A-Za-z0-9_-]{11}$`)

// VideoID finds the id in a YouTube link (watch?v=, youtu.be/, /live/).
func VideoID(s string) (string, error) {
	s = strings.TrimSpace(s)
	if reVideo.MatchString(s) {
		return s, nil
	}
	u, err := url.Parse(s)
	if err == nil {
		if v := u.Query().Get("v"); reVideo.MatchString(v) {
			return v, nil
		}
		parts := strings.Split(strings.Trim(u.Path, "/"), "/")
		if last := parts[len(parts)-1]; reVideo.MatchString(last) {
			return last, nil
		}
	}
	return "", errors.New("Bitte den Link zum laufenden YouTube-Stream angeben")
}

// YTBase is the API address; tests point it elsewhere.
var YTBase = "https://www.googleapis.com/youtube/v3"

var ytClient = &http.Client{Timeout: 15 * time.Second}

// ytMinPoll keeps the daily API quota for long streams.
var ytMinPoll = 10 * time.Second

func ytGet(ctx context.Context, path string, q url.Values, out any) error {
	req, err := http.NewRequestWithContext(ctx, "GET", YTBase+path+"?"+q.Encode(), nil)
	if err != nil {
		return err
	}
	resp, err := ytClient.Do(req)
	if err != nil {
		return fmt.Errorf("YouTube nicht erreichbar (%v)", err)
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 4<<20))
	if resp.StatusCode >= 300 {
		var e struct {
			Error struct {
				Message string `json:"message"`
			} `json:"error"`
		}
		_ = json.Unmarshal(b, &e)
		if strings.Contains(e.Error.Message, "quota") {
			return errors.New("YouTube: Tageskontingent der API aufgebraucht, morgen geht es weiter")
		}
		return fmt.Errorf("YouTube: %s (HTTP %d)", e.Error.Message, resp.StatusCode)
	}
	return json.Unmarshal(b, out)
}

// YouTube polls the live chat of video until ctx ends.
func YouTube(ctx context.Context, key, video string, on func(Event), status func(ok bool, msg string)) {
	wait := 10 * time.Second
	for ctx.Err() == nil {
		err := youtubeOnce(ctx, key, video, on, status)
		if ctx.Err() != nil {
			return
		}
		if err != nil {
			status(false, err.Error())
		}
		select {
		case <-ctx.Done():
			return
		case <-time.After(wait):
		}
		wait = min(wait*2, 5*time.Minute)
	}
}

func youtubeOnce(ctx context.Context, key, video string, on func(Event), status func(bool, string)) error {
	var v struct {
		Items []struct {
			Live struct {
				Chat string `json:"activeLiveChatId"`
			} `json:"liveStreamingDetails"`
		} `json:"items"`
	}
	if err := ytGet(ctx, "/videos", url.Values{"part": {"liveStreamingDetails"}, "id": {video}, "key": {key}}, &v); err != nil {
		return err
	}
	if len(v.Items) == 0 || v.Items[0].Live.Chat == "" {
		return errors.New("YouTube: kein laufender Live-Chat zu diesem Video")
	}
	chat := v.Items[0].Live.Chat
	token, first := "", true
	for ctx.Err() == nil {
		var r struct {
			Next     string `json:"nextPageToken"`
			Interval int    `json:"pollingIntervalMillis"`
			Offline  string `json:"offlineAt"`
			Items    []struct {
				Snippet struct {
					Type      string `json:"type"`
					Message   string `json:"displayMessage"`
					SuperChat struct {
						Micros  string `json:"amountMicros"`
						Comment string `json:"userComment"`
					} `json:"superChatDetails"`
				} `json:"snippet"`
				Author struct {
					Name    string `json:"displayName"`
					Mod     bool   `json:"isChatModerator"`
					Owner   bool   `json:"isChatOwner"`
					Sponsor bool   `json:"isChatSponsor"`
				} `json:"authorDetails"`
			} `json:"items"`
		}
		q := url.Values{"liveChatId": {chat}, "part": {"snippet,authorDetails"}, "key": {key}, "maxResults": {"200"}}
		if token != "" {
			q.Set("pageToken", token)
		}
		if err := ytGet(ctx, "/liveChat/messages", q, &r); err != nil {
			return err
		}
		if first {
			status(true, "verbunden mit dem YouTube-Live-Chat")
		}
		if !first { // the first page is history; only new messages count
			for _, it := range r.Items {
				e := Event{Source: "youtube", User: it.Author.Name, Text: it.Snippet.Message, Sub: it.Author.Sponsor, Mod: it.Author.Mod || it.Author.Owner, Time: time.Now()}
				switch it.Snippet.Type {
				case "textMessageEvent":
					e.Kind = "chat"
				case "superChatEvent":
					e.Kind, e.Text = "superchat", it.Snippet.SuperChat.Comment
					m, _ := strconv.ParseInt(it.Snippet.SuperChat.Micros, 10, 64)
					e.Amount = int(m / 10000) // cents
				case "newSponsorEvent", "memberMilestoneChatEvent", "membershipGiftingEvent":
					e.Kind, e.Sub = "sub", true
				default:
					continue
				}
				on(e)
			}
		}
		first = false
		token = r.Next
		if r.Offline != "" {
			return errors.New("YouTube: der Stream ist beendet")
		}
		// the API tells how often to ask; never more often than every 10 s
		// so the daily quota lasts for a long stream
		d := max(time.Duration(r.Interval)*time.Millisecond, ytMinPoll)
		select {
		case <-ctx.Done():
			return nil
		case <-time.After(d):
		}
	}
	return nil
}
