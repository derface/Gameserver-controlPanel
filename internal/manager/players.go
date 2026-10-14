package manager

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/query"
	"github.com/derface/Gameserver-controlPanel/internal/templates"
)

// Player management: who is online, kick, ban, whitelist and admins in one
// view. Games differ a lot, so each family has a small profile: a console
// command for the online list (or the Steam player query), commands for the
// actions, and list files the panel can read or edit.

type playerFile struct {
	Key, Label, Path, Format string // Format: mcjson | lines
	Hint                     string
	Editable                 bool // the panel edits the file itself (else via commands)
	AddAction, RemoveAction  string
}

type playerProfile struct {
	Game      string
	ListCmd   string
	Parse     func(lines []string) []string
	Actions   map[string]string // action -> command, %s = name
	IDLabel   string            // "Spielername" or "Steam-ID"
	Files     []playerFile
	NeedsRun  string // note shown when actions need a running server
	OnlineIDs bool   // online names can be used for the actions
}

var (
	reMCList    = regexp.MustCompile(`(?i)There are \d+ of a max(?: of)? \d+ players online:?\s*(.*)$`)
	reColor     = regexp.MustCompile(`§.`)
	rePZHead    = regexp.MustCompile(`(?i)Players connected \((\d+)\)`)
	reTerraLine = regexp.MustCompile(`^(.+?) \(\d+\.\d+\.\d+\.\d+:\d+\)$`)
	reFacHead   = regexp.MustCompile(`(?i)Online players \((\d+)\)`)
	reFacLine   = regexp.MustCompile(`^\s+(\S.*?) \(online\)$`)
)

func profileFor(tpl templates.Template) *playerProfile {
	id := tpl.ID
	switch {
	case id == "minecraft-java":
		return &playerProfile{Game: "Minecraft", ListCmd: "list", IDLabel: "Spielername", OnlineIDs: true,
			Parse: func(lines []string) []string {
				for _, l := range lines {
					if m := reMCList.FindStringSubmatch(reColor.ReplaceAllString(l, "")); m != nil {
						return splitNames(m[1], ",")
					}
				}
				return nil
			},
			Actions: map[string]string{"kick": "kick %s", "ban": "ban %s", "unban": "pardon %s", "whitelist_add": "whitelist add %s",
				"whitelist_remove": "whitelist remove %s", "op": "op %s", "deop": "deop %s"},
			Files: []playerFile{
				{Key: "whitelist", Label: "Whitelist", Path: "whitelist.json", Format: "mcjson", AddAction: "whitelist_add", RemoveAction: "whitelist_remove",
					Hint: "Wirkt nur, wenn die Whitelist an ist (white-list=true in den Einstellungen)."},
				{Key: "ops", Label: "Admins (OP)", Path: "ops.json", Format: "mcjson", AddAction: "op", RemoveAction: "deop"},
				{Key: "bans", Label: "Gebannt", Path: "banned-players.json", Format: "mcjson", AddAction: "ban", RemoveAction: "unban"},
			},
			NeedsRun: "Änderungen laufen über die Server-Konsole, der Server muss dafür laufen."}
	case id == "zomboid" || tpl.SteamAppID == 380870:
		return &playerProfile{Game: "Project Zomboid", ListCmd: "players", IDLabel: "Spielername", OnlineIDs: true,
			Parse: func(lines []string) []string {
				var out []string
				in := false
				for _, l := range lines {
					l = strings.TrimSpace(stripLogPrefix(l))
					if rePZHead.MatchString(l) {
						in, out = true, nil
						continue
					}
					if in {
						if strings.HasPrefix(l, "-") {
							out = append(out, strings.TrimPrefix(l, "-"))
						} else if l != "" {
							in = false
						}
					}
				}
				return out
			},
			Actions: map[string]string{"kick": `kickuser "%s"`, "ban": `banuser "%s"`, "unban": `unbanuser "%s"`, "op": `setaccesslevel "%s" admin`,
				"deop": `setaccesslevel "%s" none`, "whitelist_add": `addusertowhitelist "%s"`, "whitelist_remove": `removeuserfromwhitelist "%s"`},
			NeedsRun: "Zomboid speichert Bans, Admins und Whitelist in seiner Datenbank. Änderungen laufen über die Konsole, der Server muss dafür laufen."}
	case id == "terraria":
		return &playerProfile{Game: "Terraria", ListCmd: "playing", IDLabel: "Spielername", OnlineIDs: true,
			Parse: func(lines []string) []string {
				var out []string
				for _, l := range lines {
					if m := reTerraLine.FindStringSubmatch(strings.TrimSpace(l)); m != nil {
						out = append(out, m[1])
					}
				}
				return out
			},
			Actions:  map[string]string{"kick": "kick %s", "ban": "ban %s"},
			NeedsRun: "Entbannen geht bei Terraria nur über die Datei banlist.txt."}
	case id == "factorio":
		return &playerProfile{Game: "Factorio", ListCmd: "/players online", IDLabel: "Spielername", OnlineIDs: true,
			Parse: func(lines []string) []string {
				var out []string
				for _, l := range lines {
					if m := reFacLine.FindStringSubmatch(l); m != nil {
						out = append(out, m[1])
					}
				}
				return out
			},
			Actions: map[string]string{"kick": "/kick %s", "ban": "/ban %s", "unban": "/unban %s", "op": "/promote %s", "deop": "/demote %s",
				"whitelist_add": "/whitelist add %s", "whitelist_remove": "/whitelist remove %s"},
			NeedsRun: "Änderungen laufen über die Server-Konsole, der Server muss dafür laufen."}
	case id == "valheim" || tpl.SteamAppID == 896660:
		hint := "Eine Steam-ID (17 Ziffern) pro Eintrag, bei Crossplay auch „PlayFab_…“. Die ID steht im Spiel mit F2 oder in der Konsole beim Verbinden."
		return &playerProfile{Game: "Valheim", IDLabel: "Steam-ID", Files: []playerFile{
			{Key: "admins", Label: "Admins", Path: "saves/adminlist.txt", Format: "lines", Editable: true, Hint: hint},
			{Key: "bans", Label: "Gebannt", Path: "saves/bannedlist.txt", Format: "lines", Editable: true},
			{Key: "whitelist", Label: "Erlaubte Spieler (leer = alle)", Path: "saves/permittedlist.txt", Format: "lines", Editable: true},
		}}
	case tpl.Query != nil && tpl.Query.Type == "a2s" && (strings.Contains(tpl.Start, "srcds") || id == "cs2"):
		return &playerProfile{Game: "Source", IDLabel: "Spielername", OnlineIDs: true, Actions: map[string]string{"kick": `kick "%s"`}}
	}
	return nil
}

func splitNames(s, sep string) []string {
	var out []string
	for _, x := range strings.Split(s, sep) {
		if x = strings.TrimSpace(x); x != "" {
			out = append(out, x)
		}
	}
	return out
}

var reLogPrefix = regexp.MustCompile(`^(LOG|WARN|ERROR)\s*:\s*\w+\s*(f:\d+,?\s*)?(t:\d+>?,?\s*)?(st:[\d,]+>?\s*)?`)

func stripLogPrefix(l string) string { return reLogPrefix.ReplaceAllString(l, "") }

// PlayersView is the "Spieler" tab.
type PlayersView struct {
	Supported  bool           `json:"supported"`
	Game       string         `json:"game,omitempty"`
	Running    bool           `json:"running"`
	Online     []query.Player `json:"online"`
	OnlineFrom string         `json:"online_from,omitempty"` // console | query
	OnlineErr  string         `json:"online_error,omitempty"`
	Actions    []string       `json:"actions"`
	IDLabel    string         `json:"id_label,omitempty"`
	OnlineIDs  bool           `json:"online_ids"`
	Lists      []PlayerList   `json:"lists"`
	Note       string         `json:"note,omitempty"`
}

type PlayerList struct {
	Key      string   `json:"key"`
	Label    string   `json:"label"`
	Entries  []string `json:"entries"`
	Editable bool     `json:"editable"` // remove/add allowed (file or command)
	Hint     string   `json:"hint,omitempty"`
}

var playerCache sync.Map // id -> cachedPlayers

type cachedPlayers struct {
	at   time.Time
	list []query.Player
	from string
}

// Players collects the online list and the player lists of a server.
func (m *Manager) Players(ctx context.Context, id string) (PlayersView, error) {
	sv, tpl, err := m.get(id)
	if err != nil {
		return PlayersView{}, err
	}
	v := PlayersView{Running: m.isUp(id), Online: []query.Player{}, Actions: []string{}, Lists: []PlayerList{}}
	p := profileFor(tpl)
	if p != nil {
		v.Supported, v.Game, v.IDLabel, v.Note, v.OnlineIDs = true, p.Game, p.IDLabel, p.NeedsRun, p.OnlineIDs
		for a := range p.Actions {
			v.Actions = append(v.Actions, a)
		}
		sort.Strings(v.Actions)
		for _, f := range p.Files {
			l := PlayerList{Key: f.Key, Label: f.Label, Hint: f.Hint, Editable: f.Editable || (f.AddAction != "" && p.Actions[f.AddAction] != ""), Entries: []string{}}
			l.Entries = readPlayerFile(filepath.Join(m.ServerDir(id), filepath.FromSlash(f.Path)), f.Format)
			v.Lists = append(v.Lists, l)
		}
	}
	if !v.Running {
		return v, nil
	}
	if c, ok := playerCache.Load(id); ok && time.Since(c.(cachedPlayers).at) < 8*time.Second {
		v.Online, v.OnlineFrom = c.(cachedPlayers).list, c.(cachedPlayers).from
		return v, nil
	}
	switch {
	case p != nil && p.ListCmd != "":
		lines, err := m.CommandOutput(ctx, id, p.ListCmd)
		if err != nil {
			v.OnlineErr = err.Error()
			break
		}
		for _, n := range p.Parse(lines) {
			v.Online = append(v.Online, query.Player{Name: n})
		}
		v.OnlineFrom = "console"
	case tpl.Query != nil && tpl.Query.Type == "a2s" && tpl.Query.Port >= 0 && tpl.Query.Port < len(sv.Ports):
		ps, err := query.A2SPlayers("127.0.0.1", sv.Ports[tpl.Query.Port].Host)
		if err != nil {
			v.OnlineErr = "Der Server beantwortet die Spielerabfrage nicht"
			break
		}
		for _, x := range ps {
			if strings.TrimSpace(x.Name) != "" {
				v.Online = append(v.Online, x)
			}
		}
		v.OnlineFrom = "query"
		if len(v.Online) == 0 && len(ps) > 0 {
			v.Note = strings.TrimSpace(v.Note + fmt.Sprintf(" %d Spieler online, das Spiel verrät aber keine Namen.", len(ps)))
		}
	}
	if v.OnlineErr == "" {
		playerCache.Store(id, cachedPlayers{at: time.Now(), list: v.Online, from: v.OnlineFrom})
	}
	return v, nil
}

func readPlayerFile(p, format string) []string {
	b, err := os.ReadFile(p)
	if err != nil {
		return []string{}
	}
	out := []string{}
	switch format {
	case "mcjson":
		var xs []struct {
			Name string `json:"name"`
		}
		if json.Unmarshal(b, &xs) == nil {
			for _, x := range xs {
				if x.Name != "" {
					out = append(out, x.Name)
				}
			}
		}
	case "lines":
		sc := bufio.NewScanner(strings.NewReader(string(b)))
		for sc.Scan() {
			l := strings.TrimSpace(sc.Text())
			if l != "" && !strings.HasPrefix(l, "//") && !strings.HasPrefix(l, "#") {
				out = append(out, l)
			}
		}
	}
	return out
}

var (
	reSafeName = regexp.MustCompile(`^[\p{L}\p{N}_. \-\[\]()#@]{1,40}$`)
	reSafeID   = regexp.MustCompile(`^[A-Za-z0-9_:\-]{3,64}$`)
)

// PlayerAction runs kick, ban, unban, whitelist_add/_remove, op/deop, or edits
// a list file (list_add / list_remove with the list key).
func (m *Manager) PlayerAction(ctx context.Context, id, action, name, list string) (string, error) {
	_, tpl, err := m.get(id)
	if err != nil {
		return "", err
	}
	p := profileFor(tpl)
	if p == nil {
		return "", errors.New("Für dieses Spiel gibt es noch keine Spielerverwaltung")
	}
	name = strings.TrimSpace(name)
	if p.IDLabel == "Steam-ID" {
		if !reSafeID.MatchString(name) {
			return "", errors.New("Ungültige ID: nur Buchstaben, Ziffern, _ : -")
		}
	} else if !reSafeName.MatchString(name) || strings.ContainsAny(name, "\"'`;$\\") {
		return "", errors.New("Ungültiger Name")
	}
	if action == "list_add" || action == "list_remove" {
		for _, f := range p.Files {
			if f.Key != list {
				continue
			}
			if !f.Editable {
				a := f.AddAction
				if action == "list_remove" {
					a = f.RemoveAction
				}
				return m.PlayerAction(ctx, id, a, name, "")
			}
			return m.editPlayerFile(id, tpl, f, name, action == "list_add")
		}
		return "", errors.New("Unbekannte Liste")
	}
	cmd, ok := p.Actions[action]
	if !ok {
		return "", errors.New("Das kann dieses Spiel nicht")
	}
	if !m.isUp(id) {
		return "", errors.New("Der Server muss dafür laufen")
	}
	out, err := m.CommandOutput(ctx, id, fmt.Sprintf(cmd, name))
	if err != nil {
		return "", err
	}
	playerCache.Delete(id)
	msg := strings.TrimSpace(strings.Join(out, " "))
	if len(msg) > 200 {
		msg = msg[:200] + "…"
	}
	m.EventKind(id, "info", "", fmt.Sprintf("Spieler %s: %s", name, playerActionLabel[action]))
	return msg, nil
}

var playerActionLabel = map[string]string{"kick": "rausgeworfen", "ban": "gebannt", "unban": "entbannt", "whitelist_add": "auf die Whitelist",
	"whitelist_remove": "von der Whitelist", "op": "zum Admin gemacht", "deop": "Admin entzogen"}

func (m *Manager) editPlayerFile(id string, tpl templates.Template, f playerFile, name string, add bool) (string, error) {
	p := filepath.Join(m.ServerDir(id), filepath.FromSlash(f.Path))
	cur := readPlayerFile(p, f.Format)
	var out []string
	has := false
	for _, x := range cur {
		if x == name {
			has = true
			if !add {
				continue
			}
		}
		out = append(out, x)
	}
	if add && !has {
		out = append(out, name)
	}
	if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
		return "", err
	}
	// keep comment lines the game wrote
	var head []string
	if b, err := os.ReadFile(p); err == nil {
		for _, l := range strings.Split(string(b), "\n") {
			if t := strings.TrimSpace(l); strings.HasPrefix(t, "//") || strings.HasPrefix(t, "#") {
				head = append(head, strings.TrimRight(l, "\r"))
			}
		}
	}
	data := strings.Join(append(head, out...), "\n") + "\n"
	if err := os.WriteFile(p, []byte(data), 0o644); err != nil {
		return "", err
	}
	uid := containerUID
	if tpl.DataUID > 0 {
		uid = tpl.DataUID
	}
	_ = os.Chown(p, uid, uid)
	verb := "eingetragen"
	if !add {
		verb = "entfernt"
	}
	m.EventKind(id, "info", "", fmt.Sprintf("%s: %s %s", f.Label, name, verb))
	return f.Label + ": " + name + " " + verb + ". Das Spiel liest die Liste spätestens beim nächsten Start.", nil
}

// CommandOutput sends a console command and returns what the server
// answered. exec consoles (RCON tools) return their output directly; for
// stdin consoles the answer is read from the log right after the command.
func (m *Manager) CommandOutput(ctx context.Context, id, cmd string) ([]string, error) {
	_, tpl, err := m.get(id)
	if err != nil {
		return nil, err
	}
	if tpl.Console == "exec" {
		out, err := m.Command(ctx, id, cmd)
		if err != nil {
			return nil, err
		}
		return strings.Split(strings.TrimSpace(out), "\n"), nil
	}
	since := time.Now().Unix() - 1
	if _, err := m.Command(ctx, id, cmd); err != nil {
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case <-time.After(commandWait):
	}
	rc, err := m.logs(ctx, id, false, 400, since)
	if err != nil {
		return nil, err
	}
	defer rc.Close()
	var lines []string
	sc := bufio.NewScanner(rc)
	sc.Buffer(make([]byte, 64<<10), 1<<20)
	for sc.Scan() {
		lines = append(lines, ansiRe.ReplaceAllString(sc.Text(), ""))
	}
	// only what came after our command, if the game echoes it
	for i := len(lines) - 1; i >= 0; i-- {
		if strings.TrimSpace(lines[i]) == cmd || strings.HasSuffix(strings.TrimSpace(lines[i]), "> "+cmd) {
			return lines[i+1:], nil
		}
	}
	return lines, nil
}

var commandWait = 1500 * time.Millisecond
