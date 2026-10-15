// Package discord is a small Discord bot without a gateway connection:
// slash commands arrive as HTTP interactions (signed with Ed25519), the
// status message is posted and edited over the REST API with the bot token.
package discord

import (
	"bytes"
	"context"
	"crypto/ed25519"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// Config is stored in the panel settings.
type Config struct {
	AppID     string `json:"app_id,omitempty"`
	PublicKey string `json:"public_key,omitempty"`
	Token     string `json:"token,omitempty"`
	GuildID   string `json:"guild_id,omitempty"` // commands appear at once in this server; empty = global
	// Who may use /start, /stop and /restart. /status is open to everyone.
	Roles []string `json:"roles,omitempty"`
	Users []string `json:"users,omitempty"`
	// Servers the bot may show and control; empty = none.
	Servers []string `json:"servers,omitempty"`
	// Status message that the panel keeps up to date.
	Channel   string `json:"channel,omitempty"`
	MessageID string `json:"message_id,omitempty"`
	// Whitelist: members with this role may put their player name on the
	// whitelist of these servers with /whitelist.
	WLRole    string   `json:"wl_role,omitempty"`
	WLServers []string `json:"wl_servers,omitempty"`
	// Registered is a fingerprint of the last command registration.
	Registered string `json:"registered,omitempty"`
}

var snowflake = regexp.MustCompile(`^[0-9]{15,22}$`)

// Validate checks the IDs and the key format.
func (c *Config) Validate() error {
	c.AppID, c.PublicKey, c.Token = strings.TrimSpace(c.AppID), strings.ToLower(strings.TrimSpace(c.PublicKey)), strings.TrimSpace(c.Token)
	c.GuildID, c.Channel = strings.TrimSpace(c.GuildID), strings.TrimSpace(c.Channel)
	if c.AppID != "" && !snowflake.MatchString(c.AppID) {
		return errors.New("Die Application ID besteht nur aus Ziffern")
	}
	if c.PublicKey != "" {
		if b, err := hex.DecodeString(c.PublicKey); err != nil || len(b) != ed25519.PublicKeySize {
			return errors.New("Der Public Key ist 64 Zeichen lang (0-9, a-f)")
		}
	}
	c.WLRole = strings.Trim(strings.TrimSpace(c.WLRole), "<@&>")
	if c.WLRole != "" && c.GuildID == "" {
		return errors.New("Für die Whitelist bitte auch die Server-ID eintragen")
	}
	for _, s := range []string{c.GuildID, c.Channel, c.WLRole} {
		if s != "" && !snowflake.MatchString(s) {
			return errors.New("Server-ID und Kanal-ID bestehen nur aus Ziffern (Rechtsklick → ID kopieren)")
		}
	}
	clean := func(in []string) ([]string, error) {
		out := []string{}
		for _, s := range in {
			s = strings.Trim(strings.TrimSpace(s), "<@&!>")
			if s == "" {
				continue
			}
			if !snowflake.MatchString(s) {
				return nil, fmt.Errorf("„%s“ ist keine Discord-ID", s)
			}
			out = append(out, s)
		}
		return out, nil
	}
	var err error
	if c.Roles, err = clean(c.Roles); err != nil {
		return err
	}
	if c.Users, err = clean(c.Users); err != nil {
		return err
	}
	if len(c.Token) > 200 {
		return errors.New("Der Bot-Token ist zu lang")
	}
	return nil
}

// Ready says whether slash commands can work.
func (c Config) Ready() bool { return c.AppID != "" && c.PublicKey != "" && c.Token != "" }

// Verify checks the signature Discord puts on every interaction.
func Verify(publicKey, sig, ts string, body []byte) bool {
	pk, err := hex.DecodeString(publicKey)
	if err != nil || len(pk) != ed25519.PublicKeySize {
		return false
	}
	s, err := hex.DecodeString(sig)
	if err != nil || len(s) != ed25519.SignatureSize {
		return false
	}
	// replays older than 5 minutes are rejected
	if n, err := strconv.ParseInt(ts, 10, 64); err != nil || time.Since(time.Unix(n, 0)).Abs() > 5*time.Minute {
		return false
	}
	return ed25519.Verify(pk, append([]byte(ts), body...), s)
}

// Interaction is the part of an incoming interaction the panel uses.
type Interaction struct {
	Type   int    `json:"type"` // 1 ping, 2 command, 4 autocomplete
	Token  string `json:"token"`
	AppID  string `json:"application_id"`
	Member *struct {
		Roles []string `json:"roles"`
		User  User     `json:"user"`
	} `json:"member"`
	User *User `json:"user"` // in DMs
	Data struct {
		Name    string   `json:"name"`
		Options []Option `json:"options"`
	} `json:"data"`
}

type User struct {
	ID       string `json:"id"`
	Username string `json:"username"`
}

type Option struct {
	Name    string `json:"name"`
	Value   any    `json:"value"`
	Focused bool   `json:"focused"`
}

// Who returns the user ID, name and roles of the caller.
func (in Interaction) Who() (id, name string, roles []string) {
	if in.Member != nil {
		return in.Member.User.ID, in.Member.User.Username, in.Member.Roles
	}
	if in.User != nil {
		return in.User.ID, in.User.Username, nil
	}
	return "", "", nil
}

// Opt returns a string option.
func (in Interaction) Opt(name string) (string, bool) {
	for _, o := range in.Data.Options {
		if o.Name == name {
			s, _ := o.Value.(string)
			return s, o.Focused
		}
	}
	return "", false
}

// Allowed says whether the caller may control servers.
func (c Config) Allowed(in Interaction) bool {
	id, _, roles := in.Who()
	for _, u := range c.Users {
		if u == id {
			return true
		}
	}
	for _, r := range c.Roles {
		for _, have := range roles {
			if r == have {
				return true
			}
		}
	}
	return false
}

// Embed is a Discord message embed.
type Embed struct {
	Title       string  `json:"title,omitempty"`
	Description string  `json:"description,omitempty"`
	Color       int     `json:"color,omitempty"`
	Fields      []Field `json:"fields,omitempty"`
	Footer      *Footer `json:"footer,omitempty"`
	Timestamp   string  `json:"timestamp,omitempty"`
}

type Field struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}

type Footer struct {
	Text string `json:"text"`
}

// Choice is an autocomplete answer.
type Choice struct {
	Name  string `json:"name"`
	Value string `json:"value"`
}

const apiBase = "https://discord.com/api/v10"

var client = &http.Client{Timeout: 20 * time.Second}

// ErrUnknownMessage means the status message was deleted in Discord.
var ErrUnknownMessage = errors.New("Nachricht gibt es nicht mehr")

func call(ctx context.Context, token, method, path string, body any) ([]byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, apiBase+path, rd)
	if err != nil {
		return nil, err
	}
	if token != "" {
		req.Header.Set("Authorization", "Bot "+token)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("User-Agent", "DiscordBot (https://github.com/derface/Gameserver-controlPanel, 1)")
	res, err := client.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Discord nicht erreichbar: %v", err)
	}
	defer res.Body.Close()
	out, _ := io.ReadAll(io.LimitReader(res.Body, 1<<20))
	if res.StatusCode >= 300 {
		var e struct {
			Message string `json:"message"`
			Code    int    `json:"code"`
		}
		_ = json.Unmarshal(out, &e)
		switch {
		case e.Code == 10008 || (res.StatusCode == 404 && strings.Contains(path, "/messages/")):
			return nil, ErrUnknownMessage
		case res.StatusCode == 401:
			return nil, errors.New("Discord lehnt den Bot-Token ab")
		case e.Code == 50001 || e.Code == 50013:
			return nil, errors.New("Dem Bot fehlen Rechte in diesem Kanal (Kanal ansehen, Nachrichten senden, Links einbetten)")
		case e.Code == 10003:
			return nil, errors.New("Diesen Kanal kennt der Bot nicht. Ist er auf deinem Discord-Server eingeladen?")
		}
		if e.Message == "" {
			e.Message = res.Status
		}
		return nil, fmt.Errorf("Discord: %s", e.Message)
	}
	return out, nil
}

// Commands are the slash commands the panel registers.
func Commands() []map[string]any {
	srv := func(req bool) []map[string]any {
		return []map[string]any{{"type": 3, "name": "server", "description": "Welcher Server", "required": req, "autocomplete": true}}
	}
	return []map[string]any{
		{"name": "status", "description": "Zeigt, welche Gameserver laufen und wer spielt", "options": srv(false)},
		{"name": "start", "description": "Startet einen Gameserver", "options": srv(true)},
		{"name": "stop", "description": "Stoppt einen Gameserver", "options": srv(true)},
		{"name": "restart", "description": "Startet einen Gameserver neu", "options": srv(true)},
		{"name": "whitelist", "description": "Trägt deinen Spielernamen auf die Whitelist der Gameserver ein", "dm_permission": false,
			"options": []map[string]any{{"type": 3, "name": "spielername", "description": "Dein Name im Spiel", "required": true, "min_length": 2, "max_length": 32}}},
	}
}

// Register replaces the application's commands (guild or global).
func Register(ctx context.Context, c Config) error {
	path := "/applications/" + c.AppID + "/commands"
	if c.GuildID != "" {
		path = "/applications/" + c.AppID + "/guilds/" + c.GuildID + "/commands"
	}
	_, err := call(ctx, c.Token, "PUT", path, Commands())
	return err
}

// Fingerprint changes when a new registration is needed.
func Fingerprint(c Config) string {
	b, _ := json.Marshal(Commands())
	h := sha256.Sum256(append(b, c.AppID+"|"+c.GuildID+"|"+c.Token...))
	return hex.EncodeToString(h[:8])
}

// PostStatus sends a new message and returns its ID.
func PostStatus(ctx context.Context, c Config, e Embed) (string, error) {
	out, err := call(ctx, c.Token, "POST", "/channels/"+c.Channel+"/messages", map[string]any{"embeds": []Embed{e}, "allowed_mentions": map[string]any{"parse": []string{}}})
	if err != nil {
		return "", err
	}
	var m struct {
		ID string `json:"id"`
	}
	_ = json.Unmarshal(out, &m)
	return m.ID, nil
}

// EditStatus replaces the embed of the status message.
func EditStatus(ctx context.Context, c Config, e Embed) error {
	_, err := call(ctx, c.Token, "PATCH", "/channels/"+c.Channel+"/messages/"+c.MessageID, map[string]any{"embeds": []Embed{e}})
	return err
}

// FollowUp edits the first answer to an interaction (no bot token needed).
func FollowUp(ctx context.Context, appID, token, text string) error {
	_, err := call(ctx, "", "PATCH", "/webhooks/"+appID+"/"+token+"/messages/@original", map[string]any{"content": text, "allowed_mentions": map[string]any{"parse": []string{}}})
	return err
}

// FollowUpEmbed fills a deferred answer with an embed.
func FollowUpEmbed(ctx context.Context, appID, token string, e Embed) error {
	_, err := call(ctx, "", "PATCH", "/webhooks/"+appID+"/"+token+"/messages/@original", map[string]any{"embeds": []Embed{e}, "allowed_mentions": map[string]any{"parse": []string{}}})
	return err
}

// MemberRoles returns the roles of a member of the guild; ok=false when the
// user is not (or no longer) on the server.
func MemberRoles(ctx context.Context, c Config, user string) (roles []string, ok bool, err error) {
	out, err := call(ctx, c.Token, "GET", "/guilds/"+c.GuildID+"/members/"+user, nil)
	if err != nil {
		if strings.Contains(err.Error(), "Unknown Member") || strings.Contains(err.Error(), "Unknown User") {
			return nil, false, nil
		}
		return nil, false, err
	}
	var m struct {
		Roles []string `json:"roles"`
	}
	if err := json.Unmarshal(out, &m); err != nil {
		return nil, false, err
	}
	return m.Roles, true, nil
}
