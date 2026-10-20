package ai

import "testing"

func TestParsePlanTolerant(t *testing.T) {
	cases := map[string]func(Plan) bool{
		// text after the object with braces in it
		`Hier mein Vorschlag: {"action":"change","server":"en","vars":{"a":1},"reply":"ok {x}"} Noch etwas {dazu}`: func(p Plan) bool {
			return p.Action == "change" && p.Vars["a"] == "1" && p.Reply == "ok {x}"
		},
		// memory_mb as float and text, weekly as text, steps as one text, files as list
		`{"action":"change","server":"en","memory_mb":"8192","weekly":"true","steps":"neu starten","files":[{"enemy_damage":0.75}],"reply":"x"}`: func(p Plan) bool {
			return p.MemoryMB == 8192 && p.Weekly && len(p.Steps) == 1 && p.Files["0"]["enemy_damage"] == "0.75"
		},
		`{"action":"create","template":"valheim","memory_mb":4096.0,"vars":{"pw":null}}`: func(p Plan) bool { return p.MemoryMB == 4096 && p.Template == "valheim" },
	}
	for in, ok := range cases {
		var p Plan
		if err := parsePlan(in, &p); err != nil || !ok(p) {
			t.Errorf("%s: %+v %v", in, p, err)
		}
	}
	var p Plan
	if err := parsePlan(`{"action":"change","reply":"abgeschnitten`, &p); err == nil {
		t.Fatal("abgeschnittenes JSON angenommen")
	}
}
