package api

import (
	"compress/gzip"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"testing/fstest"
)

func TestStaticCache(t *testing.T) {
	big := strings.Repeat("console.log('bytewise');\n", 400)
	fsys := fstest.MapFS{"app.js": {Data: []byte(big)}, "vendor/three.min.js": {Data: []byte(big)}, "img/x.png": {Data: []byte("\x89PNG....")}}
	c := &staticCache{}
	get := func(name string, hdr map[string]string) *httptest.ResponseRecorder {
		r := httptest.NewRequest("GET", "/"+name, nil)
		for k, v := range hdr {
			r.Header.Set(k, v)
		}
		w := httptest.NewRecorder()
		if !c.serve(w, r, fsys, name) {
			t.Fatal("nicht gefunden", name)
		}
		return w
	}
	w := get("app.js", map[string]string{"Accept-Encoding": "gzip, br"})
	if w.Header().Get("Content-Encoding") != "gzip" || w.Body.Len() >= len(big)/4 || w.Header().Get("Cache-Control") != "no-cache" {
		t.Fatalf("%v %d", w.Header(), w.Body.Len())
	}
	zr, _ := gzip.NewReader(w.Body)
	if b, _ := io.ReadAll(zr); string(b) != big {
		t.Fatal("Inhalt falsch")
	}
	etag := w.Header().Get("ETag")
	if w := get("app.js", map[string]string{"If-None-Match": etag}); w.Code != http.StatusNotModified || w.Body.Len() != 0 {
		t.Fatal("kein 304", w.Code)
	}
	if w := get("app.js", nil); w.Header().Get("Content-Encoding") != "" || w.Body.String() != big {
		t.Fatal("ohne gzip")
	}
	if w := get("vendor/three.min.js", nil); !strings.Contains(w.Header().Get("Cache-Control"), "max-age") {
		t.Fatal(w.Header())
	}
	if w := get("img/x.png", map[string]string{"Accept-Encoding": "gzip"}); w.Header().Get("Content-Encoding") != "" {
		t.Fatal("Bild komprimiert")
	}
	if c.serve(httptest.NewRecorder(), httptest.NewRequest("GET", "/fehlt.js", nil), fsys, "fehlt.js") {
		t.Fatal("fehlende Datei")
	}
}
