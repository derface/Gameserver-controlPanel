package api

import "testing"

func TestCleanLine(t *testing.T) {
	cases := map[string]string{
		"\x1b[39m[mc-image-helper] INFO":       "[mc-image-helper] INFO",
		"\x1b[0mSuccess! App fully installed.": "Success! App fully installed.",
		"Loading Steam API...\x1b[0mOK":        "Loading Steam API...OK",
		"50%\r100%":                            "100%",
		"plain\r":                              "plain",
	}
	for in, want := range cases {
		if got := cleanLine(in); got != want {
			t.Errorf("cleanLine(%q) = %q, erwartet %q", in, got, want)
		}
	}
}
