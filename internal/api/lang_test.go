package api

import (
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestLocalizeTemplateTexts(t *testing.T) {
	v := map[string]any{
		"name":  "Spielerplätze", // user content: stays
		"label": "Spielerplätze",
		"settings": []map[string]any{{"help": "Spieler können sich im Spiel schreiben.", "labels": []string{"Wenig", "Maximal"}}},
	}
	h := withLang(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { writeJSON(w, 200, v) }))
	for _, c := range []struct{ lang, want, not string }{
		{"en", `"label":"Player slots"`, `"name":"Player slots"`},
		{"", `"label":"Spielerplätze"`, "Player slots"},
	} {
		rec := httptest.NewRecorder()
		req := httptest.NewRequest("GET", "/api/x", nil)
		if c.lang != "" {
			req.Header.Set("X-Lang", c.lang)
		}
		h.ServeHTTP(rec, req)
		body := rec.Body.String()
		if !strings.Contains(body, c.want) || strings.Contains(body, c.not) {
			t.Errorf("lang %q: %s", c.lang, body)
		}
	}
	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/x", nil)
	req.Header.Set("X-Lang", "en")
	h.ServeHTTP(rec, req)
	if !strings.Contains(rec.Body.String(), "Players can text each other in the game.") || !strings.Contains(rec.Body.String(), `"Low","Maximum"`) {
		t.Errorf("nested: %s", rec.Body.String())
	}
}
