// licgen creates the seller key pair and signs license keys.
//
//	go run ./cmd/licgen keygen                   -> writes licgen-private.key, prints the public key
//	go run ./cmd/licgen sign -edition pro -name "Max Muster" -months 12
//
// Keep licgen-private.key secret and never commit it.
package main

import (
	"crypto/ed25519"
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"flag"
	"fmt"
	"os"
	"time"

	"github.com/derface/Gameserver-controlPanel/internal/license"
)

func main() {
	if len(os.Args) < 2 {
		fmt.Println("Nutzung: licgen keygen | licgen sign -edition pro -name \"Name\" -months 12 [-key licgen-private.key]")
		os.Exit(2)
	}
	switch os.Args[1] {
	case "keygen":
		pub, priv, err := ed25519.GenerateKey(rand.Reader)
		check(err)
		if _, err := os.Stat("licgen-private.key"); err == nil {
			fmt.Println("licgen-private.key existiert schon. Nicht überschrieben.")
			os.Exit(1)
		}
		check(os.WriteFile("licgen-private.key", []byte(base64.RawURLEncoding.EncodeToString(priv)), 0o600))
		fmt.Println("Privater Schlüssel: licgen-private.key (geheim halten, nicht ins Repo)")
		fmt.Println("Öffentlicher Schlüssel für internal/license/license.go:")
		fmt.Println(base64.RawURLEncoding.EncodeToString(pub))
	case "sign":
		fs := flag.NewFlagSet("sign", flag.ExitOnError)
		ed := fs.String("edition", "pro", "starter | pro | ultimate")
		name := fs.String("name", "", "Name des Käufers")
		months := fs.Int("months", 12, "Monate mit Updates")
		keyf := fs.String("key", "licgen-private.key", "Datei mit privatem Schlüssel")
		check(fs.Parse(os.Args[2:]))
		raw, err := os.ReadFile(*keyf)
		check(err)
		priv, err := base64.RawURLEncoding.DecodeString(string(raw))
		check(err)
		id := make([]byte, 4)
		_, _ = rand.Read(id)
		key, err := license.Sign(ed25519.PrivateKey(priv), license.Payload{
			Edition: *ed, Name: *name, ID: hex.EncodeToString(id),
			UpdatesUntil: time.Now().AddDate(0, *months, 0).Format("2006-01-02"),
		})
		check(err)
		fmt.Println(key)
	default:
		fmt.Println("Unbekannter Befehl:", os.Args[1])
		os.Exit(2)
	}
}

func check(err error) {
	if err != nil {
		fmt.Fprintln(os.Stderr, "Fehler:", err)
		os.Exit(1)
	}
}
