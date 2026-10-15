package api

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/auth"
	"github.com/derface/Gameserver-controlPanel/internal/manager"
	"github.com/derface/Gameserver-controlPanel/internal/nodes"
	"github.com/derface/Gameserver-controlPanel/internal/store"
)

// Moving a server between this machine and the other machines. The panel
// the user works in runs the move: it fetches the package from the old
// machine, hands it to the new one and deletes the old server only when
// the new one has its save games.

func (a *API) routesMove(mux *http.ServeMux) {
	mux.HandleFunc("POST /api/move", a.need(auth.Admin, a.moveStart))
	mux.HandleFunc("GET /api/move/{mid}", a.need(auth.Admin, a.moveGet))
	// on the machines
	mux.HandleFunc("GET /api/node/export/{id}", a.need(auth.Admin, a.nodeExport))
	mux.HandleFunc("POST /api/node/import", a.need(auth.Admin, a.nodeImport))
}

type moveJob struct {
	ID      string    `json:"id"`
	Server  string    `json:"server"`
	Name    string    `json:"name"`
	From    string    `json:"from"`
	To      string    `json:"to"`
	Step    string    `json:"step"`
	Done    bool      `json:"done"`
	Error   string    `json:"error,omitempty"`
	NewID   string    `json:"new_id,omitempty"`
	Started time.Time `json:"started"`
	Bytes   int64     `json:"bytes,omitempty"` // size of the package
}

var moves = struct {
	sync.Mutex
	m map[string]*moveJob
}{m: map[string]*moveJob{}}

func (j *moveJob) set(f func(j *moveJob)) {
	moves.Lock()
	defer moves.Unlock()
	f(j)
}

func (a *API) moveGet(w http.ResponseWriter, r *http.Request) {
	moves.Lock()
	defer moves.Unlock()
	j, ok := moves.m[r.PathValue("mid")]
	if !ok {
		fail(w, 404, errors.New("Diesen Umzug gibt es nicht (mehr)"))
		return
	}
	writeJSON(w, 200, j)
}

// machineName is "Dieser Rechner" or the name of a node.
func (a *API) machineName(nid string) string {
	if nid == "" {
		return "Dieser Rechner"
	}
	if n, ok := a.Nodes.Get(nid); ok {
		return n.Name
	}
	return nid
}

func (a *API) moveStart(w http.ResponseWriter, r *http.Request) {
	var in struct {
		Server string `json:"server"`
		Target string `json:"target"` // "" = this machine, else node id
	}
	if err := decode(r, &in); err != nil {
		fail(w, 400, err)
		return
	}
	if a.Nodes == nil {
		fail(w, 400, errors.New("Keine weiteren Rechner eingetragen"))
		return
	}
	src, rid, remote := nodes.Split(in.Server)
	if !remote {
		src, rid = "", in.Server
	}
	if src == in.Target {
		fail(w, 400, errors.New("Der Server ist schon auf diesem Rechner"))
		return
	}
	name := ""
	if !remote {
		sv, ok := a.Store.Server(rid)
		if !ok {
			fail(w, 404, errors.New("Server nicht gefunden"))
			return
		}
		name = sv.Name
	} else if s, ok := a.Nodes.Server(in.Server); !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	} else {
		name, _ = s["name"].(string)
		if s["node_offline"] == true {
			fail(w, 400, errors.New("Der Rechner dieses Servers antwortet gerade nicht"))
			return
		}
	}
	if in.Target != "" {
		if st := a.nodeStatus(in.Target); st == nil || !st.Online {
			fail(w, 400, errors.New("Der Ziel-Rechner antwortet gerade nicht"))
			return
		} else if st.Version != a.Version {
			fail(w, 400, fmt.Errorf("Der Ziel-Rechner hat Version %s, hier läuft %s. Bitte erst beide auf denselben Stand bringen", st.Version, a.Version))
			return
		}
	}
	if src != "" {
		if st := a.nodeStatus(src); st != nil && st.Version != a.Version {
			fail(w, 400, fmt.Errorf("Der Rechner des Servers hat Version %s, hier läuft %s. Bitte erst beide auf denselben Stand bringen", st.Version, a.Version))
			return
		}
	}
	moves.Lock()
	for _, j := range moves.m {
		if j.Server == in.Server && !j.Done {
			moves.Unlock()
			fail(w, 400, errors.New("Dieser Server zieht gerade schon um"))
			return
		}
	}
	j := &moveJob{ID: newToken()[:12], Server: in.Server, Name: name, From: a.machineName(src), To: a.machineName(in.Target), Step: "Wird vorbereitet", Started: time.Now()}
	moves.m[j.ID] = j
	moves.Unlock()
	Note(r, j.From+" → "+j.To)
	go a.runMove(j, src, rid, in.Target, userOf(r).Name)
	writeJSON(w, 202, j)
}

func (a *API) nodeStatus(nid string) *nodes.Status {
	for _, s := range a.Nodes.Status() {
		if s.ID == nid {
			return &s
		}
	}
	return nil
}

func (a *API) runMove(j *moveJob, src, rid, target, user string) {
	ctx, cancel := context.WithTimeout(context.Background(), 6*time.Hour)
	defer cancel()
	step := func(s string) { j.set(func(j *moveJob) { j.Step = s }) }
	finish := func(err error, newID string) {
		j.set(func(j *moveJob) {
			j.Done = true
			if err != nil {
				j.Error = err.Error()
				j.Step = "Abgebrochen"
			} else {
				j.NewID, j.Step = newID, "Fertig"
			}
		})
		a.Nodes.Refresh()
	}
	f, err := a.Mgr.MoveFile()
	if err != nil {
		finish(err, "")
		return
	}
	pkg := f.Name()
	defer os.Remove(pkg)

	// 1. package from the old machine (it stops the server)
	step("Server wird gestoppt und gepackt")
	if src == "" {
		_, err = a.Mgr.ExportMove(ctx, rid, f)
		f.Close()
	} else {
		n, _ := a.Nodes.Get(src)
		var resp *http.Response
		resp, err = a.Nodes.Stream(ctx, n, "GET", "/api/node/export/"+rid, nil, -1, user)
		if err == nil {
			step("Paket wird vom Rechner „" + n.Name + "“ geholt")
			_, err = io.Copy(f, resp.Body)
			resp.Body.Close()
		}
		f.Close()
	}
	meta, merr := manager.ReadMoveMeta(pkg)
	if err == nil {
		err = merr
	}
	if fi, e := os.Stat(pkg); e == nil {
		j.set(func(j *moveJob) { j.Bytes = fi.Size() })
	}
	restartOld := func() {
		if !meta.WasRunning {
			return
		}
		_ = a.fleetPower(context.Background(), fullID(src, rid), "start", user)
	}
	if err != nil {
		restartOld()
		finish(fmt.Errorf("Packen auf dem alten Rechner: %w", err), "")
		return
	}

	// 2. unpack on the new machine
	step("Server wird auf „" + a.machineName(target) + "“ angelegt")
	var newID string
	if target == "" {
		var sv store.Server
		// ImportMove deletes the file; keep ours for the defer
		cp, cerr := copyToMoveFile(a.Mgr, pkg)
		if cerr != nil {
			err = cerr
		} else {
			sv, err = a.Mgr.ImportMove(cp, user, meta.WasRunning)
			newID = sv.ID
		}
	} else {
		n, _ := a.Nodes.Get(target)
		var in *os.File
		in, err = os.Open(pkg)
		if err == nil {
			fi, _ := in.Stat()
			q := ""
			if meta.WasRunning {
				q = "?start=1"
			}
			var resp *http.Response
			resp, err = a.Nodes.Stream(ctx, n, "POST", "/api/node/import"+q, in, fi.Size(), user)
			in.Close()
			if err == nil {
				var sv struct {
					ID string `json:"id"`
				}
				err = json.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&sv)
				resp.Body.Close()
				if err == nil && sv.ID == "" {
					err = errors.New("Der Rechner hat keinen Server gemeldet")
				}
				newID = target + nodes.Sep + sv.ID
				a.Nodes.PollNode(ctx, n)
			}
		}
	}
	if err != nil {
		restartOld()
		finish(fmt.Errorf("Anlegen auf dem neuen Rechner: %w", err), "")
		return
	}

	// 3. remove the old server; its folder stays as a leftover until the
	// user deletes it under Speicher (safety net)
	step("Alter Server wird entfernt")
	old := fullID(src, rid)
	if src == "" {
		err = a.Mgr.Delete(ctx, rid, false)
	} else {
		err = a.remoteCall(ctx, old, "DELETE", "", nil, user)
	}
	a.renameServerRefs(old, newID)
	if err != nil {
		finish(fmt.Errorf("Umgezogen, aber der alte Server ließ sich nicht entfernen (bitte von Hand löschen): %w", err), newID)
		return
	}
	finish(nil, newID)
}

func fullID(node, id string) string {
	if node == "" {
		return id
	}
	return node + nodes.Sep + id
}

func copyToMoveFile(m *manager.Manager, src string) (string, error) {
	in, err := os.Open(src)
	if err != nil {
		return "", err
	}
	defer in.Close()
	out, err := m.MoveFile()
	if err != nil {
		return "", err
	}
	_, err = io.Copy(out, in)
	if cerr := out.Close(); err == nil {
		err = cerr
	}
	if err != nil {
		os.Remove(out.Name())
		return "", err
	}
	return out.Name(), nil
}

// renameServerRefs points users, status page and Discord at the new id.
func (a *API) renameServerRefs(old, new string) {
	if old == "" || new == "" {
		return
	}
	_ = a.Store.Update(func(d *store.Data) error {
		for i := range d.Users {
			for k, id := range d.Users[i].Servers {
				if id == old {
					d.Users[i].Servers[k] = new
				}
			}
		}
		if e, ok := d.Settings.Status.Servers[old]; ok {
			delete(d.Settings.Status.Servers, old)
			d.Settings.Status.Servers[new] = e
		}
		for _, list := range []*[]string{&d.Settings.Discord.Servers, &d.Settings.Discord.WLServers} {
			for k, id := range *list {
				if id == old {
					(*list)[k] = new
				}
			}
		}
		for i := range d.Settings.DiscordLinks {
			l := &d.Settings.DiscordLinks[i]
			if v, ok := l.Applied[old]; ok {
				delete(l.Applied, old)
				l.Applied[new] = v
			}
		}
		return nil
	})
	statusCache.Lock()
	statusCache.body = [2]map[string]any{}
	statusCache.Unlock()
}

// nodeExport: the controlling panel takes a server away from this machine.
func (a *API) nodeExport(w http.ResponseWriter, r *http.Request) {
	id := r.PathValue("id")
	if _, ok := a.Store.Server(id); !ok {
		fail(w, 404, errors.New("Server nicht gefunden"))
		return
	}
	w.Header().Set("Content-Type", "application/x-tar")
	ctx, cancel := context.WithTimeout(r.Context(), 6*time.Hour)
	defer cancel()
	if _, err := a.Mgr.ExportMove(ctx, id, w); err != nil {
		// the answer has begun; a broken package is noticed on the other side
		Note(r, "Fehler: "+err.Error())
	}
}

// nodeImport: the controlling panel brings a server to this machine.
func (a *API) nodeImport(w http.ResponseWriter, r *http.Request) {
	f, err := a.Mgr.MoveFile()
	if err != nil {
		fail(w, 500, err)
		return
	}
	_, err = io.Copy(f, r.Body)
	f.Close()
	if err != nil {
		os.Remove(f.Name())
		fail(w, 400, fmt.Errorf("Paket nicht vollständig angekommen: %w", err))
		return
	}
	by := strings.TrimSpace(r.Header.Get("X-Node-User"))
	if by == "" {
		by = userOf(r).Name
	}
	sv, err := a.Mgr.ImportMove(f.Name(), by, r.URL.Query().Get("start") == "1")
	if err != nil {
		fail(w, 400, err)
		return
	}
	writeJSON(w, 201, a.Mgr.Info(sv))
}
