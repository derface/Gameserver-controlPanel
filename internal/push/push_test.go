package push

import (
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"io"
	"math/big"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"
)

// RFC 8291, Appendix A
func TestEncryptRFC8291(t *testing.T) {
	plain, _ := decode("V2hlbiBJIGdyb3cgdXAsIEkgd2FudCB0byBiZSBhIHdhdGVybWVsb24")
	asPriv, _ := decode("yfWPiYE-n46HLnH0KqZOF1fJJU3MYrct3AELtAQ-oRw")
	salt, _ := decode("DGv6ra1nlYgDCS1FRnbzlw")
	as, err := ecdh.P256().NewPrivateKey(asPriv)
	if err != nil {
		t.Fatal(err)
	}
	s := Subscription{P256dh: "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4", Auth: "BTBZMqHH6r4Tts7J_aSIgg"}
	body, err := encrypt(plain, s, as, salt)
	if err != nil {
		t.Fatal(err)
	}
	// the appendix lists header and ciphertext separately
	header, _ := decode("DGv6ra1nlYgDCS1FRnbzlwAAEABBBP4z9KsN6nGRTbVYI_c7VJSPQTBtkgcy27mlmlMoZIIgDll6e3vCYLocInmYWAmS6TlzAC8wEqKK6PBru3jl7A8")
	ct, _ := decode("8pfeW0KbunFT06SuDKoJH9Ql87S1QUrdirN6GcG7sFz1y1sqLgVi1VhjVkHsUoEsbI_0LpXMuGvnzQ")
	if want := append(header, ct...); string(body) != string(want) {
		t.Fatalf("\n got %x\nwant %x", body, want)
	}
}

func TestVAPID(t *testing.T) {
	k, err := NewKeys()
	if err != nil {
		t.Fatal(err)
	}
	h, err := vapid(k, "https://fcm.googleapis.com/fcm/send/abc", "https://example.org", time.Unix(1000, 0))
	if err != nil || !strings.HasPrefix(h, "vapid t=") || !strings.HasSuffix(h, ", k="+k.Public) {
		t.Fatal(h, err)
	}
	jwt := strings.TrimSuffix(strings.TrimPrefix(h, "vapid t="), ", k="+k.Public)
	parts := strings.Split(jwt, ".")
	var claims map[string]any
	cb, _ := b64.DecodeString(parts[1])
	_ = json.Unmarshal(cb, &claims)
	if claims["aud"] != "https://fcm.googleapis.com" || claims["sub"] != "https://example.org" {
		t.Fatal(claims)
	}
	pub, _ := b64.DecodeString(k.Public)
	x, y := elliptic.Unmarshal(elliptic.P256(), pub)
	sig, _ := b64.DecodeString(parts[2])
	sum := sha256.Sum256([]byte(parts[0] + "." + parts[1]))
	if !ecdsa.Verify(&ecdsa.PublicKey{Curve: elliptic.P256(), X: x, Y: y}, sum[:], new(big.Int).SetBytes(sig[:32]), new(big.Int).SetBytes(sig[32:])) {
		t.Fatal("Signatur ungültig")
	}
}

func TestCheck(t *testing.T) {
	ok := Subscription{Endpoint: "https://fcm.googleapis.com/fcm/send/x", P256dh: "BCVxsr7N_eNgVRqvHtD0zTZsEc6-VV-JvLexhqUzORcxaOzi6-AYWXvTBHm4bjyPjs7Vd8pZGH6SRpkNtoIAiw4", Auth: "BTBZMqHH6r4Tts7J_aSIgg"}
	if err := ok.Check(); err != nil {
		t.Fatal(err)
	}
	for _, e := range []string{"https://web.push.apple.com/abc", "https://updates.push.services.mozilla.com/wpush/v2/x", "https://wns2-am3p.notify.windows.com/w/?token=x"} {
		s := ok
		s.Endpoint = e
		if err := s.Check(); err != nil {
			t.Error(e, err)
		}
	}
	for _, e := range []string{"http://fcm.googleapis.com/x", "https://evil.example/x", "https://192.168.1.1/x", "https://fcm.googleapis.com.evil.de/x"} {
		s := ok
		s.Endpoint = e
		if s.Check() == nil {
			t.Error("angenommen:", e)
		}
	}
}

// decrypt is the browser side, for the round trip test.
func decrypt(t *testing.T, body []byte, ua *ecdh.PrivateKey, authSecret []byte) []byte {
	t.Helper()
	salt, idlen := body[:16], int(body[20])
	asPub := body[21 : 21+idlen]
	as, err := ecdh.P256().NewPublicKey(asPub)
	if err != nil {
		t.Fatal(err)
	}
	shared, _ := ua.ECDH(as)
	keyInfo := append(append([]byte("WebPush: info\x00"), ua.PublicKey().Bytes()...), asPub...)
	ikm := hkdf(authSecret, shared, keyInfo, 32)
	cek := hkdf(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)
	block, _ := aes.NewCipher(cek)
	gcm, _ := cipher.NewGCM(block)
	pt, err := gcm.Open(nil, nonce, body[21+idlen:], nil)
	if err != nil {
		t.Fatal(err)
	}
	return pt[:len(pt)-1] // delimiter
}

func TestSendRoundTrip(t *testing.T) {
	ua, _ := ecdh.P256().GenerateKey(rand.Reader)
	authSecret := make([]byte, 16)
	_, _ = rand.Read(authSecret)
	var got []byte
	var hdr http.Header
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got, _ = io.ReadAll(r.Body)
		hdr = r.Header
		if r.URL.Path == "/gone" {
			w.WriteHeader(410)
			return
		}
		w.WriteHeader(201)
	}))
	defer srv.Close()
	allowedHosts = append(allowedHosts, "127.0.0.1")
	old := client
	client = srv.Client()
	defer func() { client = old }()
	k, _ := NewKeys()
	sub := Subscription{Endpoint: srv.URL + "/sub", P256dh: b64.EncodeToString(ua.PublicKey().Bytes()), Auth: b64.EncodeToString(authSecret)}
	if err := Send(context.Background(), k, sub, []byte(`{"title":"Valheim","body":"Bjorn ist online"}`), "normal", "https://example.org"); err != nil {
		t.Fatal(err)
	}
	if pt := decrypt(t, got, ua, authSecret); string(pt) != `{"title":"Valheim","body":"Bjorn ist online"}` {
		t.Fatal(string(pt))
	}
	if hdr.Get("Content-Encoding") != "aes128gcm" || !strings.HasPrefix(hdr.Get("Authorization"), "vapid t=") || hdr.Get("TTL") == "" {
		t.Fatal(hdr)
	}
	sub.Endpoint = srv.URL + "/gone"
	if err := Send(context.Background(), k, sub, []byte("x"), "normal", "https://example.org"); err != ErrGone {
		t.Fatal(err)
	}
}
