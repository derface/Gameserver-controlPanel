package auth

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha1"
	"crypto/sha256"
	"encoding/base32"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"net/url"
	"strings"
	"time"
)

// TOTP after RFC 6238 with the settings every authenticator app understands:
// SHA-1, 6 digits, 30 seconds.
const (
	totpStep   = 30
	totpDigits = 6
	totpSkew   = 1 // accept one step before and after (clock drift)
)

var b32 = base32.StdEncoding.WithPadding(base32.NoPadding)

var now = time.Now // replaced in tests

func newTOTPSecret() string {
	b := make([]byte, 20)
	_, _ = rand.Read(b)
	return b32.EncodeToString(b)
}

func totpCode(secret string, step int64) (string, error) {
	key, err := b32.DecodeString(strings.ToUpper(secret))
	if err != nil {
		return "", err
	}
	var msg [8]byte
	binary.BigEndian.PutUint64(msg[:], uint64(step))
	m := hmac.New(sha1.New, key)
	m.Write(msg[:])
	sum := m.Sum(nil)
	off := sum[len(sum)-1] & 0x0f
	v := binary.BigEndian.Uint32(sum[off:off+4]) & 0x7fffffff
	return fmt.Sprintf("%06d", v%1_000_000), nil
}

// checkTOTP returns the matching time step, or 0. Steps at or before last are
// refused, so a code that was already used (or an older one) does not work again.
func checkTOTP(secret, code string, last int64) int64 {
	code = strings.ReplaceAll(strings.TrimSpace(code), " ", "")
	if len(code) != totpDigits {
		return 0
	}
	cur := now().Unix() / totpStep
	for d := int64(-totpSkew); d <= totpSkew; d++ {
		st := cur + d
		if st <= last {
			continue
		}
		if c, err := totpCode(secret, st); err == nil && hmac.Equal([]byte(c), []byte(code)) {
			return st
		}
	}
	return 0
}

func otpauthURL(issuer, user, secret string) string {
	label := url.PathEscape(issuer + ":" + user)
	q := url.Values{"secret": {secret}, "issuer": {issuer}, "algorithm": {"SHA1"}, "digits": {"6"}, "period": {"30"}}
	return "otpauth://totp/" + label + "?" + q.Encode()
}

// Recovery codes look like "k3f9-x2m7-q8wd" and are stored as sha256.
const recoveryAlphabet = "abcdefghjkmnpqrstuvwxyz23456789"

func newRecoveryCodes(n int) (plain, hashed []string) {
	for i := 0; i < n; i++ {
		b := make([]byte, 12)
		_, _ = rand.Read(b)
		var sb strings.Builder
		for j, x := range b {
			if j > 0 && j%4 == 0 {
				sb.WriteByte('-')
			}
			sb.WriteByte(recoveryAlphabet[int(x)%len(recoveryAlphabet)])
		}
		plain = append(plain, sb.String())
		hashed = append(hashed, hashRecovery(sb.String()))
	}
	return
}

func hashRecovery(code string) string {
	c := strings.ToLower(strings.NewReplacer("-", "", " ", "").Replace(strings.TrimSpace(code)))
	s := sha256.Sum256([]byte("gp-recovery:" + c))
	return hex.EncodeToString(s[:])
}
