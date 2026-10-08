package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"mime"
	"net/http"
	"os"
	"path"
	"strconv"
	"strings"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/files"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/remote"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

func (a *API) routesManage(mux *http.ServeMux) {
	mux.HandleFunc("GET /api/servers/{id}/backups", a.guard(a.backups))
	mux.HandleFunc("POST /api/servers/{id}/backups", a.guard(a.backupCreate))
	mux.HandleFunc("PUT /api/servers/{id}/backup-plan", a.guard(a.backupPlan))
	mux.HandleFunc("GET /api/servers/{id}/backups/{name}", a.guard(a.backupDownload))
	mux.HandleFunc("DELETE /api/servers/{id}/backups/{name}", a.guard(a.backupDelete))
	mux.HandleFunc("POST /api/servers/{id}/backups/{name}/restore", a.guard(a.backupRestore))
	mux.HandleFunc("POST /api/servers/{id}/backups/{name}/upload", a.guard(a.backupUpload))
	mux.HandleFunc("GET /api/remote", a.guard(a.remoteGet))
	mux.HandleFunc("PUT /api/remote", a.guard(a.remotePut))
	mux.HandleFunc("POST /api/remote/test", a.guard(a.remoteTest))
	mux.HandleFunc("GET /api/servers/{id}/settings", a.guard(a.settingsGet))
	mux.HandleFunc("PUT /api/servers/{id}/settings", a.guard(a.settingsPut))
	mux.HandleFunc("GET /api/servers/{id}/tasks", a.guard(a.tasksGet))
	mux.HandleFunc("PUT /api/servers/{id}/tasks", a.guard(a.tasksPut))
	mux.HandleFunc("POST /api/servers/{id}/tasks/{tid}/run", a.guard(a.taskRun))
	mux.HandleFunc("PUT /api/servers/{id}/watch", a.guard(a.watchPut))
	mux.HandleFunc("GET /api/steam", a.guard(a.steamGet))
	mux.HandleFunc("POST /api/steam/login", a.guard(a.steamLogin))
	mux.HandleFunc("DELETE /api/steam", a.guard(a.steamLogout))
	mux.HandleFunc("GET /api/selftest", a.guard(a.selftestGet))
	mux.HandleFunc("POST /api/selftest", a.guard(a.selftestStart))
	mux.HandleFunc("DELETE /api/selftest", a.guard(a.selftestStop))
	mux.HandleFunc("GET /api/selftest/report", a.guard(a.selftestReport))

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
	list, remoteErr, err := a.Mgr.BackupsWithRemote(r.Context(), id)
	if err != nil {
		fail(w, 500, err)
		return
	}
	remoteName := ""
	if t, _ := a.Mgr.Remote(); t != nil {
		remoteName = t.Describe()
	}
	scope, _ := a.Mgr.BackupScope(id)
	var total int64
	for _, b := range list {
		if b.Local {
			total += b.Size
		}
	}
	writeJSON(w, 200, map[string]any{
		"backups":      list,
		"total_size":   total,
		"scope":        scope,
		"plan":         sv.Backup,
		"auto_allowed": a.License().Edition.AutoBackup,
		"remote":       remoteName,
		"remote_error": remoteErr,
	})
}

func (a *API) backupCreate(w http.ResponseWriter, r *http.Request) {
	b, err := a.Mgr.CreateBackup(r.Context(), r.PathValue("id"), manager.KindManual)
	if err != nil {
		fail(w, 400, err)
		return
	}
	a.Mgr.Event(r.PathValue("id"), "info", "Backup "+b.Name+" erstellt von "+a.Auth.User(r))
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
	sv, ok := a.Store.Server(r.PathValue("id"))
	if !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	}
	name := fmt.Sprintf("%s_%s", safeFileName(sv.Name), r.PathValue("name"))
	p, err := a.Mgr.BackupFile(r.PathValue("id"), r.PathValue("name"))
	w.Header().Set("Content-Disposition", mime.FormatMediaType("attachment", map[string]string{"filename": name}))
	w.Header().Set("Content-Type", "application/gzip")
	if err == nil {
		http.ServeFile(w, r, p)
		return
	}
	// nur extern vorhanden: durchreichen
	if err := a.Mgr.StreamRemote(r.Context(), sv.ID, r.PathValue("name"), w); err != nil {
		w.Header().Del("Content-Disposition")
		fail(w, 404, err)
	}
}

func (a *API) backupDelete(w http.ResponseWriter, r *http.Request) {
	if err := a.Mgr.DeleteBackupEverywhere(r.Context(), r.PathValue("id"), r.PathValue("name")); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) backupRestore(w http.ResponseWriter, r *http.Request) {
	a.Mgr.Event(r.PathValue("id"), "warn", "Backup "+r.PathValue("name")+" wird wiederhergestellt ("+a.Auth.User(r)+")")
	if err := a.Mgr.RestoreBackup(r.PathValue("id"), r.PathValue("name")); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 202, map[string]bool{"ok": true})
}

func (a *API) backupUpload(w http.ResponseWriter, r *http.Request) {
	if err := a.Mgr.UploadBackup(r.PathValue("id"), r.PathValue("name")); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 202, map[string]bool{"ok": true})
}

// ---------- externes Backup-Ziel ----------

const masked = "••••••••"

func (a *API) remoteCfg() remote.Config {
	var c remote.Config
	a.Store.Read(func(d *store.Data) { c = d.Settings.Remote })
	return c
}

func (a *API) remoteGet(w http.ResponseWriter, r *http.Request) {
	c := a.remoteCfg()
	if c.SecretKey != "" {
		c.SecretKey = masked
	}
	writeJSON(w, 200, c)
}

// mergeRemote takes the form input and keeps the stored secret if unchanged.
func (a *API) mergeRemote(r *http.Request) (remote.Config, error) {
	var in remote.Config
	if err := decode(r, &in); err != nil {
		return in, err
	}
	if in.SecretKey == masked {
		in.SecretKey = a.remoteCfg().SecretKey
	}
	if in.Keep == 0 {
		in.Keep = 14
	}
	if in.Keep < 1 || in.Keep > 365 {
		return in, errors.New("Extern behalten: 1 bis 365")
	}
	_, err := remote.New(in, a.Mgr.DataDir())
	return in, err
}

func (a *API) remotePut(w http.ResponseWriter, r *http.Request) {
	c, err := a.mergeRemote(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	_ = a.Store.Update(func(d *store.Data) error { d.Settings.Remote = c; return nil })
	a.remoteGet(w, r)
}

func (a *API) remoteTest(w http.ResponseWriter, r *http.Request) {
	c, err := a.mergeRemote(r)
	if err != nil {
		fail(w, 400, err)
		return
	}
	t, _ := remote.New(c, a.Mgr.DataDir())
	if t == nil {
		fail(w, 400, errors.New("Kein Ziel ausgewählt"))
		return
	}
	ctx, cancel := context.WithTimeout(r.Context(), 30*time.Second)
	defer cancel()
	if err := remote.Test(ctx, t); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 200, map[string]string{"ok": t.Describe()})
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
	sv, ok := a.Store.Server(id)
	if !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return files.Root{}, false
	}
	dir := a.Mgr.ServerDir(id)
	_ = os.MkdirAll(dir, 0o755)
	tpl, _ := a.Mgr.Templates().Get(sv.Template)
	return files.New(dir).WithOwner(tpl.DataUID), true
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

// ---------- self-test ----------

func (a *API) selftestGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.Mgr.TestStatus())
}

func (a *API) selftestStart(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Which string   `json:"which"`
		IDs   []string `json:"ids"`
	}
	if err := json.NewDecoder(io.LimitReader(r.Body, 64<<10)).Decode(&req); err != nil {
		fail(w, 400, errors.New("Ungültige Anfrage"))
		return
	}
	if err := a.Mgr.StartTest(a.Mgr.TestSelection(req.Which, req.IDs)); err != nil {
		fail(w, 409, err)
		return
	}
	writeJSON(w, 200, a.Mgr.TestStatus())
}

func (a *API) selftestStop(w http.ResponseWriter, r *http.Request) {
	a.Mgr.StopTest()
	writeJSON(w, 200, map[string]bool{"ok": true})
}

func (a *API) selftestReport(w http.ResponseWriter, r *http.Request) {
	w.Header().Set("Content-Type", "text/markdown; charset=utf-8")
	w.Header().Set("Content-Disposition", `attachment; filename="selbsttest-`+time.Now().Format("2006-01-02")+`.md"`)
	_, _ = io.WriteString(w, a.Mgr.TestReport())
}

// ---------- tasks, watchdog, history ----------

func (a *API) tasksGet(w http.ResponseWriter, r *http.Request) {
	v, err := a.Mgr.Tasks(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *API) tasksPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Tasks []store.Task `json:"tasks"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	id := r.PathValue("id")
	if err := a.Mgr.SetTasks(id, req.Tasks); err != nil {
		fail(w, 400, err)
		return
	}
	a.Mgr.Event(id, "info", "Aufgaben geändert von "+a.Auth.User(r))
	a.tasksGet(w, r)
}

func (a *API) taskRun(w http.ResponseWriter, r *http.Request) {
	if err := a.Mgr.RunTaskByID(r.PathValue("id"), r.PathValue("tid")); err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 202, map[string]bool{"ok": true})
}

func (a *API) watchPut(w http.ResponseWriter, r *http.Request) {
	var req store.Watch
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if err := a.Mgr.SetWatch(r.Context(), r.PathValue("id"), req); err != nil {
		fail(w, 400, err)
		return
	}
	a.tasksGet(w, r)
}

// ---------- game settings ----------

func (a *API) settingsGet(w http.ResponseWriter, r *http.Request) {
	v, err := a.Mgr.Settings(r.PathValue("id"))
	if err != nil {
		fail(w, 404, err)
		return
	}
	writeJSON(w, 200, v)
}

func (a *API) settingsPut(w http.ResponseWriter, r *http.Request) {
	var req struct {
		Vars  map[string]string            `json:"vars"`
		Files map[string]map[string]string `json:"files"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	vals := map[int]map[string]string{}
	for k, v := range req.Files {
		i, err := strconv.Atoi(k)
		if err != nil || i < 0 {
			fail(w, 400, errors.New("Ungültige Anfrage"))
			return
		}
		vals[i] = v
	}
	id := r.PathValue("id")
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute) // a stop may take a while
	defer cancel()
	changed, err := a.Mgr.ApplySettings(ctx, id, req.Vars, vals)
	if err != nil {
		fail(w, 400, err)
		return
	}
	if len(changed) > 0 {
		a.Mgr.Event(id, "info", "Einstellungen geändert von "+a.Auth.User(r)+": "+strings.Join(changed, ", "))
	}
	v, _ := a.Mgr.Settings(id)
	writeJSON(w, 200, map[string]any{"changed": changed, "settings": v})
}

// ---------- Steam account ----------

func (a *API) steamGet(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, 200, a.Mgr.Steam())
}

func (a *API) steamLogin(w http.ResponseWriter, r *http.Request) {
	var req struct {
		User     string `json:"user"`
		Password string `json:"password"`
		Code     string `json:"code"`
	}
	if err := decode(r, &req); err != nil {
		fail(w, 400, err)
		return
	}
	if err := a.Mgr.SteamLogin(req.User, req.Password, req.Code); err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 202, a.Mgr.Steam())
}

func (a *API) steamLogout(w http.ResponseWriter, r *http.Request) {
	if err := a.Mgr.SteamLogout(); err != nil {
		fail(w, 409, err)
		return
	}
	writeJSON(w, 200, a.Mgr.Steam())
}
