Unveränderte Kopie von `golang.org/x/crypto/acme` und `acme/autocert`
(github.com/golang/crypto, Commit 99e4382b, BSD-Lizenz siehe LICENSE).

Einzige Änderungen: crypto.SignMessage (Go 1.25) nachgebildet, Importpfade auf dieses Verzeichnis umgestellt und
`golang.org/x/net/idna` durch eine kleine ASCII-Prüfung (idna.go) ersetzt,
damit das Panel ohne externe Module baut.
