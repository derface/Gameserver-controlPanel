package manager

import (
	"testing"

	"github.com/derface/Gameserver-controlPanel/internal/store"
	"github.com/derface/Gameserver-controlPanel/internal/stream"
)

func TestStreamRules(t *testing.T) {
	r := store.StreamRule{Enabled: true, Trigger: "command", Match: "!Gewitter", Server: "mc", Commands: []string{"weather thunder\n", " "}, Who: "subs"}
	if err := ValidateStreamRule(&r); err != nil || r.Match != "gewitter" || len(r.Commands) != 1 || r.Source != "any" {
		t.Fatal(err, r)
	}
	ok, text := streamMatch(r, stream.Event{Source: "twitch", Kind: "chat", Text: "!GEWITTER bitte jetzt", Sub: true})
	if !ok || text != "bitte jetzt" {
		t.Fatal(ok, text)
	}
	if ok, _ := streamMatch(r, stream.Event{Source: "twitch", Kind: "chat", Text: "!gewitter"}); ok {
		t.Fatal("ohne Abo erlaubt")
	}
	if ok, _ := streamMatch(r, stream.Event{Source: "twitch", Kind: "chat", Text: "!gewitterx", Sub: true}); ok {
		t.Fatal("anderes Wort")
	}
	bits := store.StreamRule{Enabled: true, Trigger: "bits", MinAmount: 100, Server: "mc", Say: "Danke {{user}}"}
	_ = ValidateStreamRule(&bits)
	if ok, _ := streamMatch(bits, stream.Event{Source: "twitch", Kind: "bits", Amount: 99}); ok {
		t.Fatal("zu wenig Bits")
	}
	if ok, _ := streamMatch(bits, stream.Event{Source: "twitch", Kind: "bits", Amount: 100}); !ok {
		t.Fatal("Bits")
	}
	bad := store.StreamRule{Trigger: "command", Match: "zwei worte", Server: "mc", Say: "x"}
	if ValidateStreamRule(&bad) == nil {
		t.Fatal("zwei Wörter angenommen")
	}
	// viewer input must not smuggle anything into the console
	if got := cleanViewerText(`"; op Griefer /kill @a [x] {y}` + "\nstop"); got != "op Griefer kill a x y stop" {
		t.Fatalf("%q", got)
	}
	if got := cleanViewer("Bj@rn<script>"); got != "Bjrnscript" {
		t.Fatalf("%q", got)
	}
}
