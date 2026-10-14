package discord

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"testing"
	"time"
)

func TestVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	body := []byte(`{"type":1}`)
	ts := strconv.FormatInt(time.Now().Unix(), 10)
	sig := hex.EncodeToString(ed25519.Sign(priv, append([]byte(ts), body...)))
	pk := hex.EncodeToString(pub)
	if !Verify(pk, sig, ts, body) {
		t.Fatal("valid signature rejected")
	}
	if Verify(pk, sig, ts, []byte(`{"type":2}`)) {
		t.Fatal("changed body accepted")
	}
	old := strconv.FormatInt(time.Now().Add(-10*time.Minute).Unix(), 10)
	oldSig := hex.EncodeToString(ed25519.Sign(priv, append([]byte(old), body...)))
	if Verify(pk, oldSig, old, body) {
		t.Fatal("old timestamp accepted")
	}
	if Verify(pk, "zz", ts, body) || Verify("abc", sig, ts, body) {
		t.Fatal("garbage accepted")
	}
}

func TestAllowedAndValidate(t *testing.T) {
	c := Config{Roles: []string{" <@&123456789012345678> "}, Users: []string{"222222222222222222"}}
	if err := c.Validate(); err != nil {
		t.Fatal(err)
	}
	if c.Roles[0] != "123456789012345678" {
		t.Fatalf("role not cleaned: %q", c.Roles[0])
	}
	var in Interaction
	in.Member = &struct {
		Roles []string `json:"roles"`
		User  User     `json:"user"`
	}{Roles: []string{"123456789012345678"}, User: User{ID: "1"}}
	if !c.Allowed(in) {
		t.Fatal("role should be allowed")
	}
	in.Member.Roles = nil
	if c.Allowed(in) {
		t.Fatal("no role, not allowed")
	}
	in.Member = nil
	in.User = &User{ID: "222222222222222222"}
	if !c.Allowed(in) {
		t.Fatal("user should be allowed")
	}
	bad := Config{Roles: []string{"Admins"}}
	if bad.Validate() == nil {
		t.Fatal("role name instead of ID accepted")
	}
}
