package api

import (
	"bytes"
	"compress/gzip"
	"crypto/sha256"
	"encoding/hex"
	"io/fs"
	"mime"
	"net/http"
	"path"
	"strconv"
	"strings"
	"sync"
)

// Static files of the web interface: compressed once with gzip (about a
// quarter of the size) and served with an ETag, so a browser only
// downloads a file again after it really changed. Libraries and images may
// be kept for a week without asking.

type staticFile struct {
	data, gz []byte
	etag     string
	ctype    string
}

type staticCache struct {
	mu    sync.Mutex
	files map[string]*staticFile
}

func compressible(ctype string) bool {
	return strings.HasPrefix(ctype, "text/") || strings.Contains(ctype, "javascript") || strings.Contains(ctype, "json") ||
		strings.Contains(ctype, "svg") || strings.Contains(ctype, "manifest")
}

func (c *staticCache) get(fsys fs.FS, name string) (*staticFile, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if f, ok := c.files[name]; ok {
		return f, f != nil
	}
	if c.files == nil {
		c.files = map[string]*staticFile{}
	}
	b, err := fs.ReadFile(fsys, name)
	if err != nil {
		c.files[name] = nil
		return nil, false
	}
	sum := sha256.Sum256(b)
	f := &staticFile{data: b, etag: `"` + hex.EncodeToString(sum[:10]) + `"`, ctype: mime.TypeByExtension(path.Ext(name))}
	switch path.Ext(name) {
	case ".webmanifest":
		f.ctype = "application/manifest+json"
	case "":
		f.ctype = "application/octet-stream"
	}
	if f.ctype == "" {
		f.ctype = http.DetectContentType(b)
	}
	if compressible(f.ctype) && len(b) > 1024 {
		var buf bytes.Buffer
		zw, _ := gzip.NewWriterLevel(&buf, gzip.BestCompression)
		_, _ = zw.Write(b)
		_ = zw.Close()
		if buf.Len() < len(b) {
			f.gz = buf.Bytes()
		}
	}
	c.files[name] = f
	return f, true
}

func (c *staticCache) serve(w http.ResponseWriter, r *http.Request, fsys fs.FS, name string) bool {
	f, ok := c.get(fsys, name)
	if !ok {
		return false
	}
	h := w.Header()
	h.Set("Content-Type", f.ctype)
	h.Set("ETag", f.etag)
	h.Set("Vary", "Accept-Encoding")
	if strings.HasPrefix(name, "vendor/") || strings.HasPrefix(name, "img/") {
		h.Set("Cache-Control", "public, max-age=604800")
	} else {
		h.Set("Cache-Control", "no-cache") // ask every time, mostly answered with 304
	}
	if match := r.Header.Get("If-None-Match"); match != "" && strings.Contains(match, f.etag) {
		w.WriteHeader(http.StatusNotModified)
		return true
	}
	body := f.data
	if f.gz != nil && strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
		h.Set("Content-Encoding", "gzip")
		body = f.gz
	}
	h.Set("Content-Length", strconv.Itoa(len(body)))
	w.WriteHeader(http.StatusOK)
	if r.Method != http.MethodHead {
		_, _ = w.Write(body)
	}
	return true
}
