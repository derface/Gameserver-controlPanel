// Package mods searches Modrinth for Minecraft mods and plugins. The
// itzg/minecraft-server image downloads the chosen projects itself on start
// (MODRINTH_PROJECTS), so the panel only manages the list.
package mods

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"
)

// BaseURL can be changed in tests.
var BaseURL = "https://api.modrinth.com/v2"

var UserAgent = "derface/gamepanel (self-hosted game server panel)"

var client = &http.Client{Timeout: 15 * time.Second}

// Project is what the panel shows for one mod or plugin.
type Project struct {
	ID          string   `json:"id"`
	Slug        string   `json:"slug"`
	Title       string   `json:"title"`
	Description string   `json:"description"`
	Icon        string   `json:"icon,omitempty"`
	Downloads   int      `json:"downloads"`
	Author      string   `json:"author,omitempty"`
	Categories  []string `json:"categories,omitempty"`
	ServerSide  string   `json:"server_side,omitempty"`
	ClientSide  string   `json:"client_side,omitempty"`
}

// Target describes what fits a server: mods for a loader or plugins.
type Target struct {
	ProjectType string // mod | plugin
	Loader      string // fabric, forge, neoforge, quilt, paper, purpur, spigot, bukkit
	GameVersion string // "" = any
}

// TargetFor maps the itzg server TYPE to a Modrinth search target.
func TargetFor(serverType, version string) (Target, bool) {
	v := strings.TrimSpace(version)
	if strings.EqualFold(v, "LATEST") || strings.EqualFold(v, "SNAPSHOT") {
		v = ""
	}
	switch strings.ToUpper(serverType) {
	case "FABRIC":
		return Target{"mod", "fabric", v}, true
	case "QUILT":
		return Target{"mod", "quilt", v}, true
	case "FORGE":
		return Target{"mod", "forge", v}, true
	case "NEOFORGE":
		return Target{"mod", "neoforge", v}, true
	case "PAPER", "PURPUR", "SPIGOT", "BUKKIT", "FOLIA":
		return Target{"plugin", strings.ToLower(serverType), v}, true
	}
	return Target{}, false
}

func get(ctx context.Context, path string, q url.Values, out any) error {
	u := BaseURL + path
	if len(q) > 0 {
		u += "?" + q.Encode()
	}
	req, err := http.NewRequestWithContext(ctx, "GET", u, nil)
	if err != nil {
		return err
	}
	req.Header.Set("User-Agent", UserAgent)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Modrinth nicht erreichbar: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode == http.StatusTooManyRequests {
		return errors.New("Modrinth: zu viele Anfragen, bitte kurz warten")
	}
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("Modrinth antwortet mit %s", resp.Status)
	}
	return json.NewDecoder(http.MaxBytesReader(nil, resp.Body, 4<<20)).Decode(out)
}

// Search finds projects that run on the server side for the target.
func Search(ctx context.Context, t Target, query string, offset int) ([]Project, int, error) {
	facets := [][]string{
		{"project_type:" + t.ProjectType},
		{"categories:" + t.Loader},
		{"server_side:required", "server_side:optional"},
	}
	if t.Loader == "purpur" || t.Loader == "folia" {
		facets[1] = append(facets[1], "categories:paper") // Paper plugins run there too
	}
	if t.Loader == "paper" {
		facets[1] = append(facets[1], "categories:spigot", "categories:bukkit")
	}
	if t.GameVersion != "" {
		facets = append(facets, []string{"versions:" + t.GameVersion})
	}
	fb, _ := json.Marshal(facets)
	q := url.Values{"facets": {string(fb)}, "limit": {"20"}, "offset": {fmt.Sprint(max(0, offset))}}
	if s := strings.TrimSpace(query); s != "" {
		q.Set("query", s)
	} else {
		q.Set("index", "downloads")
	}
	var res struct {
		Hits []struct {
			ProjectID   string   `json:"project_id"`
			Slug        string   `json:"slug"`
			Title       string   `json:"title"`
			Description string   `json:"description"`
			IconURL     string   `json:"icon_url"`
			Downloads   int      `json:"downloads"`
			Author      string   `json:"author"`
			Categories  []string `json:"display_categories"`
			ServerSide  string   `json:"server_side"`
		} `json:"hits"`
		Total int `json:"total_hits"`
	}
	if err := get(ctx, "/search", q, &res); err != nil {
		return nil, 0, err
	}
	out := make([]Project, 0, len(res.Hits))
	for _, h := range res.Hits {
		out = append(out, Project{ID: h.ProjectID, Slug: h.Slug, Title: h.Title, Description: h.Description, Icon: safeIcon(h.IconURL),
			Downloads: h.Downloads, Author: h.Author, Categories: h.Categories, ServerSide: h.ServerSide})
	}
	return out, res.Total, nil
}

// Lookup returns details for the given slugs or ids (unknown ones are left out).
func Lookup(ctx context.Context, slugs []string) ([]Project, error) {
	if len(slugs) == 0 {
		return []Project{}, nil
	}
	ids, _ := json.Marshal(slugs)
	var res []struct {
		ID          string `json:"id"`
		Slug        string `json:"slug"`
		Title       string `json:"title"`
		Description string `json:"description"`
		IconURL     string `json:"icon_url"`
		Downloads   int    `json:"downloads"`
		ServerSide  string `json:"server_side"`
		ClientSide  string `json:"client_side"`
	}
	if err := get(ctx, "/projects", url.Values{"ids": {string(ids)}}, &res); err != nil {
		return nil, err
	}
	out := make([]Project, 0, len(res))
	for _, p := range res {
		out = append(out, Project{ID: p.ID, Slug: p.Slug, Title: p.Title, Description: p.Description, Icon: safeIcon(p.IconURL), Downloads: p.Downloads, ServerSide: p.ServerSide, ClientSide: p.ClientSide})
	}
	return out, nil
}

// only Modrinth's CDN, so the page's image policy can stay strict
func safeIcon(u string) string {
	if strings.HasPrefix(u, "https://cdn.modrinth.com/") {
		return u
	}
	return ""
}

// ParseList turns the stored list ("create,ferrite-core") into slugs.
func ParseList(s string) []string {
	out := []string{} // never nil: the browser expects a list
	seen := map[string]bool{}
	for _, p := range strings.FieldsFunc(s, func(r rune) bool { return r == ',' || r == ' ' || r == '\n' }) {
		p = strings.TrimSpace(p)
		if p != "" && !seen[strings.ToLower(p)] {
			seen[strings.ToLower(p)] = true
			out = append(out, p)
		}
	}
	return out
}

var slugRe = regexp.MustCompile(`^[A-Za-z0-9_.-]{2,64}$`)

// ValidSlug reports whether s looks like a Modrinth slug or id.
func ValidSlug(s string) bool { return slugRe.MatchString(s) }
