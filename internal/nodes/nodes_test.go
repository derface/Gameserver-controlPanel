package nodes

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func TestCodeAndURL(t *testing.T) {
	c := Code{URL: "https://10.0.0.5:8420", Token: "gpn_abc"}
	got, err := DecodeCode("  " + EncodeCode(c) + "\n")
	if err != nil || got != c {
		t.Fatalf("roundtrip: %v %v", got, err)
	}
	for _, bad := range []string{"", "gpnode:", "gpnode:!!", "abc", EncodeCode(Code{URL: "x", Token: "nope"})} {
		if _, err := DecodeCode(bad); err == nil {
			t.Errorf("%q accepted", bad)
		}
	}
	for in, want := range map[string]string{"10.0.0.5:8420": "https://10.0.0.5:8420", "http://keller:8420/": "http://keller:8420"} {
		if got, err := CleanURL(in); err != nil || got != want {
			t.Errorf("CleanURL(%q) = %q, %v", in, got, err)
		}
	}
	for _, bad := range []string{"ftp://x", "https://u:p@x", "https://x/panel", "https://"} {
		if _, err := CleanURL(bad); err == nil {
			t.Errorf("CleanURL(%q) accepted", bad)
		}
	}
	if n, s, ok := Split("n1~abc"); !ok || n != "n1" || s != "abc" {
		t.Error("split")
	}
	if _, _, ok := Split("abc"); ok {
		t.Error("split without node")
	}
	b := RewriteIDs([]byte(`{"id":"abc","x":{"server":"abc"},"other":"abc","id2":"abcd"}`), "abc", "n1~abc")
	if string(b) != `{"id":"n1~abc","x":{"server":"n1~abc"},"other":"abc","id2":"abcd"}` {
		t.Error(string(b))
	}
}

// fakeNode answers like a panel that accepts the key "gpn_ok".
func fakeNode(t *testing.T) *httptest.Server {
	return httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Header.Get("Authorization") != "Bearer gpn_ok" || r.Header.Get("Cookie") != "" {
			w.WriteHeader(401)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		switch r.URL.Path {
		case "/api/node/hello":
			_ = json.NewEncoder(w).Encode(Hello{Version: "1.0", Hostname: "keller", Servers: 1})
		case "/api/servers":
			_, _ = io.WriteString(w, `[{"id":"s1","name":"Valheim","status":"running"}]`)
		case "/api/servers/s1/settings":
			_, _ = io.WriteString(w, `{"id":"s1","user":"`+r.Header.Get("X-Node-User")+`","q":"`+r.URL.RawQuery+`"}`)
		default:
			_, _ = io.WriteString(w, `{}`)
		}
	}))
}

func TestProbePinAndProxy(t *testing.T) {
	srv := fakeNode(t)
	defer srv.Close()
	ctx := context.Background()
	if _, _, err := Probe(ctx, srv.URL, "gpn_wrong"); err == nil || !strings.Contains(err.Error(), "Schlüssel") {
		t.Fatalf("wrong key: %v", err)
	}
	fp, h, err := Probe(ctx, srv.URL, "gpn_ok")
	if err != nil || fp != Fingerprint(srv.Certificate().Raw) || h.Hostname != "keller" {
		t.Fatalf("probe: %q %+v %v", fp, h, err)
	}

	dir := t.TempDir()
	st, err := store.Open(dir)
	if err != nil {
		t.Fatal(err)
	}
	n := store.Node{ID: "n1", Name: "Keller", URL: srv.URL, Token: "gpn_ok", FP: fp, Added: time.Now()}
	_ = st.Update(func(d *store.Data) error { d.Nodes = []store.Node{n}; return nil })
	p := New(st)
	p.pollAll(ctx)
	list := p.Servers()
	if len(list) != 1 || list[0]["id"] != "n1~s1" || list[0]["node_name"] != "Keller" {
		t.Fatalf("servers: %v", list)
	}

	rec := httptest.NewRecorder()
	req := httptest.NewRequest("GET", "/api/servers/n1~s1/settings?x=1", nil)
	req.Header.Set("Cookie", "gp_session=secret")
	p.Proxy(rec, req, n, "/api/servers/s1/settings", "s1", "david")
	if body := rec.Body.String(); !strings.Contains(body, `"id":"n1~s1"`) || !strings.Contains(body, `"user":"david"`) || !strings.Contains(body, `"q":"x=1"`) {
		t.Fatalf("proxy: %d %s", rec.Code, body)
	}

	// another certificate: the pinned one no longer matches
	_ = st.Update(func(d *store.Data) error { d.Nodes[0].FP = strings.Repeat("ab", 32); return nil })
	p.pollAll(ctx)
	s := p.Status()[0]
	if s.Online || !strings.Contains(s.Error, "Zertifikat") {
		t.Fatalf("pin: %+v", s)
	}
	if l := p.Servers(); len(l) != 1 || l[0]["node_offline"] != true {
		t.Fatalf("offline list: %v", l)
	}
}
