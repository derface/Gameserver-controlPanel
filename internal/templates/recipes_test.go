package templates

import (
	"strings"
	"testing"
)

func TestWorkshopRecipesApplied(t *testing.T) {
	set, err := Load("")
	if err != nil {
		t.Fatal(err)
	}
	for id := range workshopRecipes {
		tpl, ok := set.Get(id)
		if !ok {
			t.Errorf("Vorlage %s fehlt", id)
			continue
		}
		if tpl.Workshop == nil || tpl.Workshop.App == 0 {
			t.Errorf("%s ohne Workshop", id)
		}
		for _, rp := range workshopRecipes[id].StartReplace {
			if !strings.Contains(tpl.Start, rp[1]) {
				t.Errorf("%s: Startzeile nicht angepasst: %s", id, tpl.Start)
			}
		}
	}
	if z, _ := set.Get("zomboid"); z.Workshop == nil || z.Workshop.Mode != "zomboid" {
		t.Error("zomboid")
	}
}
