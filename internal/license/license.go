// Package license verifies Ed25519-signed license keys offline.
//
// A key looks like  GP1.<payload>.<signature>  (both base64url). The payload
// is JSON: {"e":"pro","n":"Name","u":"2027-09-29","i":"k1a2b3"}.
// Only the seller holds the private key (see cmd/licgen).
package license

import (
	"crypto/ed25519"
	"encoding/base64"
	"encoding/json"
	"errors"
	"strings"
	"time"
)

// PublicKey is the seller's verification key. Replace it with your own
// before shipping releases: go run ./cmd/licgen keygen
var PublicKey = "0YIejRkfJ5brHSIxzSNKHX0esyO7tYzpbJX0MQfXC5Y"

type Edition struct {
	ID         string `json:"id"`
	Name       string `json:"name"`
	MaxServers int    `json:"max_servers"` // -1 = unlimited
	AI         bool   `json:"ai"`
	AutoBackup bool   `json:"auto_backup"` // geplante Backups
}

var Editions = map[string]Edition{
	"free":     {ID: "free", Name: "Testmodus", MaxServers: 1, AI: true, AutoBackup: true},
	"starter":  {ID: "starter", Name: "Starter", MaxServers: 3, AI: false, AutoBackup: false},
	"pro":      {ID: "pro", Name: "Pro", MaxServers: 10, AI: true, AutoBackup: true},
	"ultimate": {ID: "ultimate", Name: "Ultimate", MaxServers: -1, AI: true, AutoBackup: true},
}

type Payload struct {
	Edition      string `json:"e"`
	Name         string `json:"n"`
	UpdatesUntil string `json:"u"`
	ID           string `json:"i"`
}

type Status struct {
	Edition      Edition `json:"edition"`
	Licensee     string  `json:"licensee,omitempty"`
	UpdatesUntil string  `json:"updates_until,omitempty"`
	UpdatesOK    bool    `json:"updates_ok"`
	Valid        bool    `json:"valid"`
	Error        string  `json:"error,omitempty"`
}

var b64 = base64.RawURLEncoding

func Verify(key string) (Payload, error) {
	var p Payload
	parts := strings.Split(strings.TrimSpace(key), ".")
	if len(parts) != 3 || parts[0] != "GP1" {
		return p, errors.New("Das ist kein gültiger Lizenzschlüssel")
	}
	pub, err := b64.DecodeString(PublicKey)
	if err != nil || len(pub) != ed25519.PublicKeySize {
		return p, errors.New("Im Panel ist kein gültiger Prüfschlüssel hinterlegt")
	}
	body, err1 := b64.DecodeString(parts[1])
	sig, err2 := b64.DecodeString(parts[2])
	if err1 != nil || err2 != nil {
		return p, errors.New("Der Lizenzschlüssel ist beschädigt")
	}
	if !ed25519.Verify(ed25519.PublicKey(pub), []byte(parts[1]), sig) {
		return p, errors.New("Der Lizenzschlüssel ist ungültig")
	}
	if err := json.Unmarshal(body, &p); err != nil {
		return p, errors.New("Der Lizenzschlüssel ist beschädigt")
	}
	if _, ok := Editions[p.Edition]; !ok {
		return p, errors.New("Unbekannte Edition im Lizenzschlüssel")
	}
	return p, nil
}

func Check(key string) Status {
	if strings.TrimSpace(key) == "" {
		return Status{Edition: Editions["free"]}
	}
	p, err := Verify(key)
	if err != nil {
		return Status{Edition: Editions["free"], Error: err.Error()}
	}
	st := Status{Edition: Editions[p.Edition], Licensee: p.Name, UpdatesUntil: p.UpdatesUntil, Valid: true}
	if t, err := time.Parse("2006-01-02", p.UpdatesUntil); err == nil {
		st.UpdatesOK = time.Now().Before(t.Add(24 * time.Hour))
	}
	return st
}

// Sign creates a key. Used only by cmd/licgen.
func Sign(priv ed25519.PrivateKey, p Payload) (string, error) {
	j, err := json.Marshal(p)
	if err != nil {
		return "", err
	}
	body := b64.EncodeToString(j)
	sig := ed25519.Sign(priv, []byte(body))
	return "GP1." + body + "." + b64.EncodeToString(sig), nil
}
