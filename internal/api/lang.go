package api

import (
	"bytes"
	"encoding/json"
	"net/http"

	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// The web UI translates itself in the browser (web/static/i18n.js). Texts that
// come from game templates are too many for the browser dictionary, so the
// server swaps them when a request asks for English (header X-Lang: en).

type enWriter struct{ http.ResponseWriter }

func (e *enWriter) Unwrap() http.ResponseWriter { return e.ResponseWriter }

func (e *enWriter) Flush() {
	if f, ok := e.ResponseWriter.(http.Flusher); ok {
		f.Flush()
	}
}

func withLang(h http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("X-Lang") == "en" {
			w = &enWriter{w}
		}
		h.ServeHTTP(w, r)
	})
}

// Only these JSON keys carry template texts. User content (server names,
// player names, file contents) lives under other keys and stays untouched.
var langKeys = map[string]bool{"label": true, "help": true, "group": true, "unit": true, "labels": true,
	"title": true, "note": true, "notes": true}

func localize(v any) any {
	raw, err := json.Marshal(v)
	if err != nil {
		return v
	}
	dec := json.NewDecoder(bytes.NewReader(raw))
	dec.UseNumber()
	var x any
	if dec.Decode(&x) != nil {
		return v
	}
	return walkLang(x, false)
}

func walkLang(x any, here bool) any {
	switch t := x.(type) {
	case map[string]any:
		for k, v := range t {
			t[k] = walkLang(v, langKeys[k])
		}
	case []any:
		for i, v := range t {
			t[i] = walkLang(v, here)
		}
	case string:
		if here {
			if en, ok := templates.EN(t); ok {
				return en
			}
		}
	}
	return x
}
