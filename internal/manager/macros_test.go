package manager

import (
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestValidateMacros(t *testing.T) {
	got, err := ValidateMacros([]store.Macro{{Name: " Event ", Lines: []string{"say Los geht's\nwarte 5\n\n# Kommentar", "weather clear"}}})
	if err != nil || len(got) != 1 || got[0].ID == "" || got[0].Name != "Event" || len(got[0].Lines) != 4 {
		t.Fatalf("%+v %v", got, err)
	}
	if d, ok := macroWait("Wait 10s"); !ok || d != 10*time.Second {
		t.Error("wait")
	}
	bad := [][]store.Macro{
		{{Name: "", Lines: []string{"say x"}}},
		{{Name: "a", Lines: []string{"warte 5"}}},                              // no command
		{{Name: "a", Lines: []string{"say", "warte 900"}}},                     // pause too long
		{{Name: "a", Lines: []string{"x"}}, {Name: "A", Lines: []string{"y"}}}, // same name
		{{Name: "a", Lines: []string{strings.Repeat("x", 501)}}},
	}
	for i, b := range bad {
		if _, err := ValidateMacros(b); err == nil {
			t.Errorf("case %d accepted", i)
		}
	}
	if _, ok := FindMacro(store.Server{Macros: got}, "event"); !ok {
		t.Error("find by name")
	}
}
