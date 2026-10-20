package stream

import (
	"bufio"
	"context"
	"fmt"
	"net"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"
)

func TestCleanChannelAndVideo(t *testing.T) {
	for in, want := range map[string]string{"https://www.twitch.tv/MeinKanal": "meinkanal", "#foo_bar": "foo_bar", "x y": ""} {
		got, err := CleanChannel(in)
		if got != want || (want == "" && err == nil) {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
	for in, want := range map[string]string{"https://www.youtube.com/watch?v=dQw4w9WgXcQ&t=1": "dQw4w9WgXcQ", "https://youtu.be/dQw4w9WgXcQ": "dQw4w9WgXcQ", "https://www.youtube.com/live/dQw4w9WgXcQ?si=x": "dQw4w9WgXcQ"} {
		if got, err := VideoID(in); got != want || err != nil {
			t.Errorf("%q: %q %v", in, got, err)
		}
	}
}

func TestTwitchChat(t *testing.T) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer ln.Close()
	var login []string
	go func() {
		c, err := ln.Accept()
		if err != nil {
			return
		}
		defer c.Close()
		r := bufio.NewReader(c)
		for i := 0; i < 4; i++ {
			l, _ := r.ReadString('\n')
			login = append(login, strings.TrimSpace(l))
		}
		fmt.Fprint(c, ":tmi.twitch.tv 001 justinfan :Welcome\r\n")
		fmt.Fprint(c, ":justinfan!justinfan@justinfan.tmi.twitch.tv JOIN #kanal\r\n")
		fmt.Fprint(c, "PING :tmi.twitch.tv\r\n")
		fmt.Fprint(c, "@badges=subscriber/12;display-name=Bjorn;mod=0;subscriber=1 :bjorn!bjorn@bjorn.tmi.twitch.tv PRIVMSG #kanal :!gewitter jetzt\r\n")
		fmt.Fprint(c, "@badges=;custom-reward-id=abc-123;display-name=Anna;mod=1 :anna!anna@anna.tmi.twitch.tv PRIVMSG #kanal :Diamant bitte\r\n")
		fmt.Fprint(c, "@bits=500;display-name=Max :max!max@max.tmi.twitch.tv PRIVMSG #kanal :Cheer500 los\r\n")
		fmt.Fprint(c, "@msg-id=raid;msg-param-displayName=Raider;msg-param-viewerCount=42;display-name=Raider :tmi.twitch.tv USERNOTICE #kanal\r\n")
		fmt.Fprint(c, "@msg-id=resub;msg-param-cumulative-months=7;display-name=Lisa :tmi.twitch.tv USERNOTICE #kanal :danke\r\n")
		pong, _ := r.ReadString('\n')
		login = append(login, strings.TrimSpace(pong))
		time.Sleep(200 * time.Millisecond)
	}()
	TwitchAddr, twitchTLS = ln.Addr().String(), false
	ctx, cancel := context.WithTimeout(context.Background(), 3*time.Second)
	defer cancel()
	var mu sync.Mutex
	var evs []Event
	var st []string
	go Twitch(ctx, "kanal", func(e Event) {
		mu.Lock()
		evs = append(evs, e)
		if len(evs) == 5 {
			cancel()
		}
		mu.Unlock()
	}, func(ok bool, m string) { mu.Lock(); st = append(st, fmt.Sprintf("%v %s", ok, m)); mu.Unlock() })
	<-ctx.Done()
	time.Sleep(50 * time.Millisecond)
	mu.Lock()
	defer mu.Unlock()
	if len(evs) != 5 {
		t.Fatalf("%d Ereignisse: %+v", len(evs), evs)
	}
	if e := evs[0]; e.Kind != "chat" || e.User != "Bjorn" || !e.Sub || e.Text != "!gewitter jetzt" {
		t.Errorf("%+v", e)
	}
	if e := evs[1]; e.Kind != "reward" || e.RewardID != "abc-123" || !e.Mod {
		t.Errorf("%+v", e)
	}
	if e := evs[2]; e.Kind != "bits" || e.Amount != 500 {
		t.Errorf("%+v", e)
	}
	if e := evs[3]; e.Kind != "raid" || e.Amount != 42 || e.User != "Raider" {
		t.Errorf("%+v", e)
	}
	if e := evs[4]; e.Kind != "sub" || e.Amount != 7 {
		t.Errorf("%+v", e)
	}
	if !strings.HasPrefix(login[2], "NICK justinfan") || login[3] != "JOIN #kanal" || login[4] != "PONG :tmi.twitch.tv" {
		t.Errorf("%q", login)
	}
	if len(st) == 0 || st[0] != "true verbunden mit #kanal" {
		t.Errorf("%q", st)
	}
}

func TestYouTubeChat(t *testing.T) {
	calls := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Query().Get("key") != "k" {
			w.WriteHeader(403)
			return
		}
		switch r.URL.Path {
		case "/videos":
			fmt.Fprint(w, `{"items":[{"liveStreamingDetails":{"activeLiveChatId":"chat1"}}]}`)
		case "/liveChat/messages":
			calls++
			if calls == 1 { // history, ignored
				fmt.Fprint(w, `{"nextPageToken":"p1","pollingIntervalMillis":10,"items":[{"snippet":{"type":"textMessageEvent","displayMessage":"alt"},"authorDetails":{"displayName":"A"}}]}`)
				return
			}
			fmt.Fprint(w, `{"nextPageToken":"p2","pollingIntervalMillis":10,"items":[
			  {"snippet":{"type":"textMessageEvent","displayMessage":"!zombie"},"authorDetails":{"displayName":"Ben","isChatSponsor":true}},
			  {"snippet":{"type":"superChatEvent","displayMessage":"x","superChatDetails":{"amountMicros":"5000000","userComment":"für dich"}},"authorDetails":{"displayName":"Cem"}}],"offlineAt":"2026-10-20T20:00:00Z"}`)
		}
	}))
	defer srv.Close()
	YTBase, ytMinPoll = srv.URL, 10*time.Millisecond
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	var evs []Event
	err := youtubeOnce(ctx, "k", "dQw4w9WgXcQ", func(e Event) { evs = append(evs, e) }, func(bool, string) {})
	if err == nil || !strings.Contains(err.Error(), "beendet") {
		t.Fatal(err)
	}
	if len(evs) != 2 || evs[0].Text != "!zombie" || !evs[0].Sub || evs[1].Kind != "superchat" || evs[1].Amount != 500 || evs[1].Text != "für dich" {
		t.Fatalf("%+v", evs)
	}
}
