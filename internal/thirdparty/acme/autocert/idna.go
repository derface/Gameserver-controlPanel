package autocert

import (
	"errors"
	"strings"
)

// toASCII replaces idna.Lookup.ToASCII: the panel only accepts plain ASCII
// host names (internationalised names must be entered in punycode).
func toASCII(s string) (string, error) {
	for _, r := range s {
		if r >= 0x80 {
			return "", errors.New("autocert: Domain bitte in Punycode (xn--…) angeben")
		}
	}
	return strings.ToLower(s), nil
}
