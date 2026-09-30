package remote

import (
	"fmt"
	"net/http"
	"net/url"
	"os"
	"testing"
	"time"
)

// TestSigDump prints signatures for cross-checking with botocore (only with SIGDUMP=1).
func TestSigDump(t *testing.T) {
	if os.Getenv("SIGDUMP") == "" {
		t.Skip()
	}
	s, _ := NewS3(Config{Type: "s3", Endpoint: "https://s3.eu-central-003.backblazeb2.com", Bucket: "mein-bucket", AccessKey: "AKIDEXAMPLE", SecretKey: "wJalrXUtnFEMI/K7MDENG+bPxRfiCYEXAMPLEKEY", Prefix: "panel", PathStyle: true})
	now := time.Date(2026, 9, 30, 18, 0, 0, 0, time.UTC)
	cases := []struct {
		m    string
		u    *url.URL
		hash string
	}{
		{"GET", s.objectURL("", url.Values{"list-type": {"2"}, "prefix": {"panel/ab12_Mein Server/"}}), emptySHA},
		{"PUT", s.objectURL(s.fullKey("ab12_Wikinger-Runde/2026-09-30_19-35-37_manuell.tar.gz"), nil), "d3751d33f9cd5049c4af2b462735457e4d3baf130bcbb87f389e349fbaeb20b9"},
		{"PUT", s.objectURL("panel/ümlaut ordner/a+b=c.tar.gz", nil), emptySHA},
		{"POST", s.objectURL("panel/x.tar.gz", url.Values{"uploads": {""}}), emptySHA},
		{"PUT", s.objectURL("panel/x.tar.gz", url.Values{"partNumber": {"2"}, "uploadId": {"2~abc/def+ghi="}}), emptySHA},
	}
	for _, c := range cases {
		req, _ := http.NewRequest(c.m, c.u.String(), nil)
		sign(req, c.hash, s.access, s.secret, s.region, now)
		fmt.Printf("%s\t%s\t%s\t%s\n", c.m, c.u.String(), c.hash, req.Header.Get("Authorization"))
	}
}
