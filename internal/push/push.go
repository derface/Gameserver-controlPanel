// Package push sends Web Push messages (RFC 8030) to browsers and installed
// web apps: payload encryption per RFC 8291 (aes128gcm, RFC 8188) and VAPID
// authentication per RFC 8292, with the standard library only.
package push

import (
	"bytes"
	"context"
	"crypto/aes"
	"crypto/cipher"
	"crypto/ecdh"
	"crypto/ecdsa"
	"crypto/elliptic"
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"encoding/base64"
	"encoding/binary"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math/big"
	"net/http"
	"net/url"
	"strings"
	"time"
)

var b64 = base64.RawURLEncoding

// decode accepts base64url with or without padding (browsers differ).
func decode(s string) ([]byte, error) {
	s = strings.TrimRight(strings.TrimSpace(s), "=")
	s = strings.NewReplacer("+", "-", "/", "_").Replace(s)
	return b64.DecodeString(s)
}

// Keys is the VAPID key pair of this panel.
type Keys struct {
	Private string `json:"private"` // base64url, 32 bytes
	Public  string `json:"public"`  // base64url, 65 bytes uncompressed point
}

// NewKeys creates a VAPID key pair.
func NewKeys() (Keys, error) {
	k, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return Keys{}, err
	}
	return Keys{Private: b64.EncodeToString(k.Bytes()), Public: b64.EncodeToString(k.PublicKey().Bytes())}, nil
}

// Subscription is what the browser's PushManager hands out.
type Subscription struct {
	Endpoint string `json:"endpoint"`
	P256dh   string `json:"p256dh"`
	Auth     string `json:"auth"`
}

// allowedHosts are the push services of Chrome/Android, Firefox, Safari/iOS
// and Edge. Other endpoints are refused, so the panel cannot be made to
// send requests anywhere else.
var allowedHosts = []string{"fcm.googleapis.com", "android.googleapis.com", "updates.push.services.mozilla.com",
	".push.services.mozilla.com", ".push.apple.com", ".notify.windows.com"}

// Check validates a subscription.
func (s Subscription) Check() error {
	u, err := url.Parse(s.Endpoint)
	if err != nil || u.Scheme != "https" || u.Host == "" || len(s.Endpoint) > 1000 {
		return errors.New("ungültige Push-Adresse")
	}
	host, ok := strings.ToLower(u.Hostname()), false
	for _, h := range allowedHosts {
		if host == h || (strings.HasPrefix(h, ".") && strings.HasSuffix(host, h)) {
			ok = true
		}
	}
	if !ok {
		return errors.New("unbekannter Push-Dienst: " + host)
	}
	if p, err := decode(s.P256dh); err != nil || len(p) != 65 {
		return errors.New("ungültiger Push-Schlüssel")
	}
	if a, err := decode(s.Auth); err != nil || len(a) != 16 {
		return errors.New("ungültiges Push-Geheimnis")
	}
	return nil
}

func hkdf(salt, ikm, info []byte, n int) []byte {
	m := hmac.New(sha256.New, salt)
	m.Write(ikm)
	prk := m.Sum(nil)
	m = hmac.New(sha256.New, prk)
	m.Write(info)
	m.Write([]byte{1})
	return m.Sum(nil)[:n]
}

// encrypt builds the aes128gcm body for one record. asKey and salt are
// random in normal use; tests pass fixed ones.
func encrypt(plain []byte, s Subscription, asKey *ecdh.PrivateKey, salt []byte) ([]byte, error) {
	uaPub, err := decode(s.P256dh)
	if err != nil {
		return nil, err
	}
	authSecret, err := decode(s.Auth)
	if err != nil {
		return nil, err
	}
	ua, err := ecdh.P256().NewPublicKey(uaPub)
	if err != nil {
		return nil, err
	}
	shared, err := asKey.ECDH(ua)
	if err != nil {
		return nil, err
	}
	asPub := asKey.PublicKey().Bytes()
	keyInfo := append(append([]byte("WebPush: info\x00"), uaPub...), asPub...)
	ikm := hkdf(authSecret, shared, keyInfo, 32)
	cek := hkdf(salt, ikm, []byte("Content-Encoding: aes128gcm\x00"), 16)
	nonce := hkdf(salt, ikm, []byte("Content-Encoding: nonce\x00"), 12)
	block, err := aes.NewCipher(cek)
	if err != nil {
		return nil, err
	}
	gcm, err := cipher.NewGCM(block)
	if err != nil {
		return nil, err
	}
	ct := gcm.Seal(nil, nonce, append(append([]byte{}, plain...), 2), nil)
	var body bytes.Buffer
	body.Write(salt)
	_ = binary.Write(&body, binary.BigEndian, uint32(4096))
	body.WriteByte(byte(len(asPub)))
	body.Write(asPub)
	body.Write(ct)
	return body.Bytes(), nil
}

// vapid returns the Authorization header for the push service of endpoint.
func vapid(k Keys, endpoint, subject string, now time.Time) (string, error) {
	priv, err := decode(k.Private)
	if err != nil || len(priv) != 32 {
		return "", errors.New("VAPID-Schlüssel ungültig")
	}
	u, err := url.Parse(endpoint)
	if err != nil {
		return "", err
	}
	header := b64.EncodeToString([]byte(`{"typ":"JWT","alg":"ES256"}`))
	claims, _ := json.Marshal(map[string]any{"aud": u.Scheme + "://" + u.Host, "exp": now.Add(12 * time.Hour).Unix(), "sub": subject})
	signing := header + "." + b64.EncodeToString(claims)
	key := new(ecdsa.PrivateKey)
	key.Curve = elliptic.P256()
	key.D = new(big.Int).SetBytes(priv)
	key.PublicKey.X, key.PublicKey.Y = key.Curve.ScalarBaseMult(priv)
	h := sha256.Sum256([]byte(signing))
	r, s, err := ecdsa.Sign(rand.Reader, key, h[:])
	if err != nil {
		return "", err
	}
	sig := make([]byte, 64)
	r.FillBytes(sig[:32])
	s.FillBytes(sig[32:])
	return "vapid t=" + signing + "." + b64.EncodeToString(sig) + ", k=" + k.Public, nil
}

// ErrGone means the subscription no longer exists and should be deleted.
var ErrGone = errors.New("Push-Abo abgelaufen")

var client = &http.Client{Timeout: 15 * time.Second}

// Send delivers one message. urgency: "high" for problems, else "normal".
func Send(ctx context.Context, k Keys, s Subscription, payload []byte, urgency, subject string) error {
	if err := s.Check(); err != nil {
		return err
	}
	as, err := ecdh.P256().GenerateKey(rand.Reader)
	if err != nil {
		return err
	}
	salt := make([]byte, 16)
	if _, err := rand.Read(salt); err != nil {
		return err
	}
	body, err := encrypt(payload, s, as, salt)
	if err != nil {
		return err
	}
	auth, err := vapid(k, s.Endpoint, subject, time.Now())
	if err != nil {
		return err
	}
	req, err := http.NewRequestWithContext(ctx, "POST", s.Endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Encoding", "aes128gcm")
	req.Header.Set("Content-Type", "application/octet-stream")
	req.Header.Set("TTL", "86400")
	req.Header.Set("Urgency", urgency)
	req.Header.Set("Authorization", auth)
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("Push-Dienst nicht erreichbar (%v)", err)
	}
	defer resp.Body.Close()
	msg, _ := io.ReadAll(io.LimitReader(resp.Body, 300))
	switch {
	case resp.StatusCode == 404 || resp.StatusCode == 410:
		return ErrGone
	case resp.StatusCode >= 300:
		return fmt.Errorf("Push-Dienst: HTTP %d %s", resp.StatusCode, strings.TrimSpace(string(msg)))
	}
	return nil
}
