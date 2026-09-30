package api

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"

	"github.com/derface/Gameserver-controlPanel/internal/files"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
)

func (a *API) routesManage(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/backups", a.guard(a.backups))
	mux.HandleFunc("POST /api/servers/{id}/backups", a.guard(a.backupCreate))
	mux.HandleFunc("PUT /api/servers/{id}/backup-plan", a.guard(a.backupPlan))
	mux.HandleFunc("GET /api/servers/{id}/backups/{name}", a.guard(a.backupDownload))
	mux.HandleFunc("DELETE /api/servers/{id}/backups/{name}", a.guard(a.backupDelete))
	mux.HandleFunc("POST /api/servers/{id}/backups/{name}/restore", a.guard(a.backupRestore))

	mux.HandleFunc("GET /api/servers/{id}/files", a.guard(a.fileList))
	mux.HandleFunc("DELETE /api/servers/{id}/files", a.guard(a.fileDelete))
	mux.HandleFunc("GET /api/servers/{id}/files/content", a.guard(a.fileRead))
	mux.HandleFunc("PUT /api/servers/{id}/files/content", a.guard(a.fileWrite))
	mux.HandleFunc("GET /api/servers/{id}/files/download", a.guard(a.fileDownload))
	mux.HandleFunc("POST /api/servers/{id}/files/upload", a.guard(a.fileUpload))
	mux.HandleFunc("POST /api/servers/{id}/files/mkdir", a.guard(a.fileMkdir))
	mux.HandleFunc("POST /api/servers/{id}/files/rename", a.guard(a.fileRename))
}

// ---------- backups ----------

func (a *API) backups(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	sv, ok := a.Store.Server(id)
	if !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	}
	list, err := a.Mgr.ListBackups(id)
	if err != nil {
		fail(w, 500, err)
		return
	}
	scope, _ := a.Mgr.BackupScope(id)
	var total int64
	for _, b := range list {
		total += b.Size
	}
	writeJSON(w, 200, map[string]any{
		"backups":      list,
		"total_size":   total,
		"scope":        scope,
		"plan":         sv.Backup,
		"auto_allowed": a.License().Edition.AutoBackup,
	})
}

func (a *API) backupCreate(w http.ResponseWriter, r *http.Request) {
	b, err := a.Mgr.CreateBackup(r.Context(), r.PathValue("id"), manager.KindManual)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 201, b)
}

func (a *API) backupPlan(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Every int `json:"every_hours"`
		At    int `json:"at_hour"`
		Keep  int `json:"keep"`
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if in.Every != 0 && !a.License().Edition.AutoBackup {
		fail(w, 403, errors.New("Geplante Backups sind in deiner Edition nicht enthalten"))
		return
	}
	sv, err := a.Mgr.SetBackupPlan(r.PathValue("id"), in.Every, in.At, in.Keep)
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, sv.Backup)
}

func (a *API) backupDownload(w http.ResponseWriter, r *http.Request) {
	p, err := a.Mgr.BackupFile(r.PathValue("id"), r.PathValue("name"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	sv, _ := a.Store.Server(r.PathValue("id"))
	name := fmt.Sprintf("%s_%s", safeFileName(sv.Name), r.PathValue("name"))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Type", "application/gzip")
	http.ServeFile(w, r, p)
}

func (a *API) backupDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.Mgr.DeleteBackup(r.PathValue("id"), r.PathValue("name")); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) backupRestore(w http.ResponseWriter, r *http.Request) {
	if err := a.Mgr.RestoreBackup(r.PathValue("id"), r.PathValue("name")); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 202, map[string]bool{"ok": true})
}

func safeFileName(s string) string {
	out := []rune{}
	for _, c := range s {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_':
			out = append(out, c)
		case c == ' ':
			out = append(out, '-')
		}
	}
	if len(out) == 0 {
		return "server"
	}
	return string(out)
}

// ---------- files ----------

func (a *API) root(w http.ResponseWriter, r *http.Request) (files.Root, bool) {
	id := r.PathValue("id")
	if _, ok := a.Store.Server(id); !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return files.Root{}, false
	}
	dir := a.Mgr.ServerDir(id)
	_ = os.MkdirAll(dir, 0o755)
	return files.New(dir), true
}

func qpath(r *http.Request) string { return r.URL.Query().Get("path") }

func (a *API) fileList(w http.ResponseWriter, r *http.Request) {
	root, ok := a.root(w, r)
	if !ok {
		return
	}
	list, err := root.List(qpath(r))
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, list)
}

func (a *API) fileRead(w http.ResponseWriter, r *http.Request) {
	root, ok := a.root(w, r)
	if !ok {
		return
	}
	s, err := root.ReadText(qpath(r))
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"content": s})
}

func (a *API) fileWrite(w http.ResponseWriter, r *http.Request) {
	root, ok := a.root(w, r)
	if !ok {
		return
	}
	var in struct {
		Content string `json:"content"`
	}
	if err := json.NewDecoder(http.MaxBytesReader(w, r.Body, files.MaxEditSize+(1<<20))).Decode(&in); err != nil {
		fail(w, 400, errors.New("Ungültige Anfrage oder Datei zu groß"))
		return
	}
	if err := root.WriteText(qpath(r), in.Content); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) fileDownload(w http.ResponseWriter, r *http.Request) {
	root, ok := a.root(w, r)
	if !ok {
		return
	}
	f, fi, err := root.Open(qpath(r))
	if err != nil {
		fail(w, 400, err)
		return
	}
	defer f.Close()
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": fi.Name()}))
	w.Header().Set("Content-Type", "application/octet-stream")
	http.ServeContent(w, r, fi.Name(), fi.ModTime(), f)
}

func (a *API) fileUpload(w http.ResponseWriter, r *http.Request) {
	root, ok := a.root(w, r)
	if !ok {
		return
	}
	r.Body = http.MaxBytesReader(w, r.Body, 8<<30)
	mr, err := r.MultipartReader()
	if err != nil {
		fail(w, 400, errors.New("Kein Datei-Upload"))
		return
	}
	n := 0
	for {
		part, err := mr.NextPart()
		if err == io.EOF {
			break
		}
		if err != nil {
			fail(w, 400, fmt.Errorf("Upload abgebrochen: %w", err))
			return
		}
		name := path.Base(part.FileName())
		if part.FileName() == "" {
			part.Close()
			continue
		}
		if err := root.Save(qpath(r), name, part); err != nil {
			part.Close()
			fail(w, 400, fmt.Errorf("%s: %w", name, err))
			return
		}
		part.Close()
		n++
	}
	writeJSON(w, 200, map[string]int{"saved": n})
}

type nameReq struct {
	Name string `json:"name"`
}

func (a *API) fileMkdir(w http.ResponseWriter, r *http.Request) {
	root, ok := a.root(w, r)
	if !ok {
		return
	}
	var in nameReq
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if err := root.Mkdir(qpath(r), in.Name); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) fileRename(w http.ResponseWriter, r *http.Request) {
	root, ok := a.root(w, r)
	if !ok {
		return
	}
	var in nameReq
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if err := root.Rename(qpath(r), in.Name); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) fileDelete(w http.ResponseWriter, r *http.Request) {
	root, ok := a.root(w, r)
	if !ok {
		return
	}
	if err := root.Delete(qpath(r)); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}
