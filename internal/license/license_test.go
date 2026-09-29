package license

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"strings"
	"testing"
)

func TestSignVerify(t *testing.T) {
	pub, priv, _ := ed25519.GenerateKey(rand.Reader)
	old := PublicKey
	PublicKey = base64.RawURLEncoding.EncodeToString(pub)
	defer func() { PublicKey = old }()

	key, err := Sign(priv, Payload{Edition: "pro", Name: "Test", UpdatesUntil: "2099-01-01", ID: "x"})
	if err != nil {
		t.Fatal(err)
	}
	st := Check(key)
	if !st.Valid || st.Edition.ID != "pro" || !st.UpdatesOK {
		t.Fatalf("unerwartet: %+v", st)
	}
	bad := strings.Replace(key, "GP1.e", "GP1.f", 1)
	if Check(bad).Valid {
		t.Fatal("manipulierter Schlüssel wurde akzeptiert")
	}
	if Check("").Edition.ID != "free" {
		t.Fatal("ohne Schlüssel sollte der Testmodus gelten")
	}
}
