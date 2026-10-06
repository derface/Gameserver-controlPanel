package mods

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
)

func TestTargetFor(t *testing.T) {
	cases := map[[2]string]Target{
		{"FABRIC", "1.21.1"}:   {"mod", "fabric", "1.21.1"},
		{"neoforge", "LATEST"}: {"mod", "neoforge", ""},
		{"PAPER", " 1.20.4 "}:  {"plugin", "paper", "1.20.4"},
		{"PURPUR", ""}:         {"plugin", "purpur", ""},
	}
	for in, want := range cases {
		got, ok := TargetFor(in[0], in[1])
		if !ok || got != want {
			t.Errorf("%v: %+v %v", in, got, ok)
		}
	}
	if _, ok := TargetFor("VANILLA", "LATEST"); ok {
		t.Error("Vanilla hat keine Mods")
	}
}

func TestSearchAndLookup(t *testing.T) {
	var lastQuery string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("User-Agent") == "" {
			t.Error("kein User-Agent")
		}
		lastQuery = r.URL.RawQuery
		switch r.URL.Path {
		case "/search":
			_, _ = w.Write([]byte(`{"hits":[{"project_id":"gvQqBUqZ","slug":"lithium","title":"Lithium","description":"Optimierung","icon_url":"https://cdn.modrinth.com/data/x.webp","downloads":5,"author":"jellysquid","display_categories":["fabric"],"server_side":"optional"},
				{"project_id":"x","slug":"evil","title":"X","icon_url":"https://evil.example/x.png"}],"total_hits":2}`))
		case "/projects":
			var ids []string
			_ = json.Unmarshal([]byte(r.URL.Query().Get("ids")), &ids)
			if len(ids) != 2 {
				t.Errorf("ids: %v", ids)
			}
			_, _ = w.Write([]byte(`[{"id":"gvQqBUqZ","slug":"lithium","title":"Lithium","icon_url":"https://cdn.modrinth.com/a.png","server_side":"optional"}]`))
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	BaseURL = srv.URL

	hits, total, err := Search(context.Background(), Target{"mod", "fabric", "1.21.1"}, "lith", 0)
	if err != nil || total != 2 || len(hits) != 2 || hits[0].Slug != "lithium" || hits[0].Icon == "" {
		t.Fatalf("%+v %d %v", hits, total, err)
	}
	if hits[1].Icon != "" {
		t.Fatal("fremdes Bild durchgelassen")
	}
	q, _ := httpUnescape(lastQuery)
	for _, w := range []string{`"project_type:mod"`, `"categories:fabric"`, `"server_side:required","server_side:optional"`, `"versions:1.21.1"`, "query=lith"} {
		if !strings.Contains(q, w) {
			t.Errorf("%s fehlt in %s", w, q)
		}
	}
	_, _, _ = Search(context.Background(), Target{"plugin", "paper", ""}, "", 0)
	q, _ = httpUnescape(lastQuery)
	if !strings.Contains(q, `"categories:paper","categories:spigot","categories:bukkit"`) || strings.Contains(q, "versions:") || !strings.Contains(q, "index=downloads") {
		t.Errorf("Paper-Suche: %s", q)
	}
	ps, err := Lookup(context.Background(), []string{"lithium", "gibtsnicht"})
	if err != nil || len(ps) != 1 || ps[0].Title != "Lithium" {
		t.Fatalf("%+v %v", ps, err)
	}
}

func TestParseList(t *testing.T) {
	if b, _ := json.Marshal(ParseList("")); string(b) != "[]" {
		t.Fatalf("leere Liste als %s", b)
	}
	got := ParseList(" create, ferrite-core,,Create ,modernfix\n")
	if strings.Join(got, "|") != "create|ferrite-core|modernfix" {
		t.Fatal(got)
	}
	for _, s := range []string{"lithium", "gvQqBUqZ", "fabric-api"} {
		if !ValidSlug(s) {
			t.Fatal(s)
		}
	}
	for _, s := range []string{"a", "x;rm", "a b", strings.Repeat("a", 70)} {
		if ValidSlug(s) {
			t.Fatal(s)
		}
	}
}

func httpUnescape(s string) (string, error) {
	r, err := http.NewRequest("GET", "http://x/?"+s, nil)
	if err != nil {
		return "", err
	}
	out := ""
	for k, v := range r.URL.Query() {
		out += k + "=" + strings.Join(v, ",") + "&"
	}
	return out, nil
}
