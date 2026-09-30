package remote

import (
	"bytes"
	"context"
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

const (
	emptySHA      = "e3b0c44298fc1c149afbf4c8996fb92427ae41e4649b934ca495991b7852b855"
	multipartFrom = 128 << 20 // ab 128 MB in Teilen hochladen
	partSize      = 64 << 20
)

type S3 struct {
	endpoint  *url.URL
	region    string
	bucket    string
	access    string
	secret    string
	prefix    string
	pathStyle bool
	hc        *http.Client
	now       func() time.Time
}

var regionRe = regexp.MustCompile(`s3[.-]([a-z0-9-]+)\.(?:backblazeb2\.com|wasabisys\.com|amazonaws\.com|your-objectstorage\.com)`)

func NewS3(c Config) (*S3, error) {
	ep := strings.TrimRight(strings.TrimSpace(c.Endpoint), "/")
	if ep == "" {
		ep = "https://s3.amazonaws.com"
	}
	if !strings.Contains(ep, "://") {
		ep = "https://" + ep
	}
	u, err := url.Parse(ep)
	if err != nil || u.Host == "" || (u.Scheme != "https" && u.Scheme != "http") {
		return nil, errors.New("Der Endpunkt muss eine Adresse wie https://s3.eu-central-003.backblazeb2.com sein")
	}
	if strings.TrimSpace(c.Bucket) == "" || strings.TrimSpace(c.AccessKey) == "" || strings.TrimSpace(c.SecretKey) == "" {
		return nil, errors.New("Bucket, Access Key und Secret Key sind Pflicht")
	}
	region := strings.TrimSpace(c.Region)
	if region == "" {
		if m := regionRe.FindStringSubmatch(u.Host); m != nil {
			region = m[1]
		} else {
			region = "us-east-1"
		}
	}
	return &S3{
		endpoint: u, region: region, bucket: strings.TrimSpace(c.Bucket),
		access: strings.TrimSpace(c.AccessKey), secret: strings.TrimSpace(c.SecretKey),
		prefix: strings.Trim(strings.TrimSpace(c.Prefix), "/"), pathStyle: c.PathStyle,
		hc: &http.Client{Timeout: 0}, now: time.Now,
	}, nil
}

func (s *S3) Describe() string {
	return fmt.Sprintf("S3 %s/%s", s.endpoint.Host, s.bucket)
}

func (s *S3) fullKey(key string) string {
	if s.prefix == "" {
		return key
	}
	return s.prefix + "/" + key
}

func (s *S3) objectURL(key string, q url.Values) *url.URL {
	u := *s.endpoint
	p := strings.TrimRight(u.Path, "/")
	if s.pathStyle {
		p += "/" + s.bucket
	} else {
		u.Host = s.bucket + "." + u.Host
	}
	if key != "" {
		p += "/" + key
	}
	if p == "" {
		p = "/"
	}
	u.Path = p
	u.RawPath = uriEncode(p, true) // exakt so senden, wie signiert wird
	u.RawQuery = canonicalQuery(q)
	return &u
}

// ---------- SigV4 ----------

func uriEncode(s string, keepSlash bool) string {
	var b strings.Builder
	for i := 0; i < len(s); i++ {
		c := s[i]
		switch {
		case c >= 'A' && c <= 'Z', c >= 'a' && c <= 'z', c >= '0' && c <= '9', c == '-', c == '_', c == '.', c == '~':
			b.WriteByte(c)
		case c == '/' && keepSlash:
			b.WriteByte(c)
		default:
			fmt.Fprintf(&b, "%%%02X", c)
		}
	}
	return b.String()
}

func canonicalQuery(q url.Values) string {
	if len(q) == 0 {
		return ""
	}
	keys := make([]string, 0, len(q))
	for k := range q {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	var parts []string
	for _, k := range keys {
		vs := append([]string(nil), q[k]...)
		sort.Strings(vs)
		for _, v := range vs {
			parts = append(parts, uriEncode(k, false)+"="+uriEncode(v, false))
		}
	}
	return strings.Join(parts, "&")
}

func hmacSHA(key []byte, data string) []byte {
	h := hmac.New(sha256.New, key)
	h.Write([]byte(data))
	return h.Sum(nil)
}

func sha256hex(b []byte) string {
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:])
}

// sign adds the AWS Signature Version 4 headers to req.
func sign(req *http.Request, payloadHash, access, secret, region string, now time.Time) {
	amzDate := now.UTC().Format("20060102T150405Z")
	day := amzDate[:8]
	req.Header.Set("x-amz-date", amzDate)
	req.Header.Set("x-amz-content-sha256", payloadHash)
	host := req.URL.Host
	if req.Host != "" {
		host = req.Host
	}
	headers := map[string]string{"host": host}
	for k, v := range req.Header {
		lk := strings.ToLower(k)
		if lk == "x-amz-date" || lk == "x-amz-content-sha256" || strings.HasPrefix(lk, "x-amz-") || lk == "range" || lk == "content-md5" {
			headers[lk] = strings.Join(v, ",")
		}
	}
	names := make([]string, 0, len(headers))
	for k := range headers {
		names = append(names, k)
	}
	sort.Strings(names)
	var ch strings.Builder
	for _, n := range names {
		ch.WriteString(n + ":" + strings.TrimSpace(headers[n]) + "\n")
	}
	signed := strings.Join(names, ";")
	canonURI := uriEncode(req.URL.Path, true)
	if canonURI == "" {
		canonURI = "/"
	}
	canon := strings.Join([]string{req.Method, canonURI, canonicalQuery(req.URL.Query()), ch.String(), signed, payloadHash}, "\n")
	scope := day + "/" + region + "/s3/aws4_request"
	sts := "AWS4-HMAC-SHA256\n" + amzDate + "\n" + scope + "\n" + sha256hex([]byte(canon))
	k := hmacSHA([]byte("AWS4"+secret), day)
	k = hmacSHA(k, region)
	k = hmacSHA(k, "s3")
	k = hmacSHA(k, "aws4_request")
	sig := hex.EncodeToString(hmacSHA(k, sts))
	req.Header.Set("Authorization", "AWS4-HMAC-SHA256 Credential="+access+"/"+scope+", SignedHeaders="+signed+", Signature="+sig)
}

type s3Error struct {
	Code    string `xml:"Code"`
	Message string `xml:"Message"`
}

func readS3Err(resp *http.Response) error {
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	var e s3Error
	if xml.Unmarshal(b, &e) == nil && e.Code != "" {
		switch e.Code {
		case "InvalidAccessKeyId", "SignatureDoesNotMatch", "AccessDenied", "InvalidToken":
			return fmt.Errorf("Zugang abgelehnt (%s). Access Key, Secret Key und Region prüfen", e.Code)
		case "NoSuchBucket":
			return errors.New("Den Bucket gibt es nicht. Namen prüfen")
		}
		return fmt.Errorf("%s: %s", e.Code, e.Message)
	}
	return fmt.Errorf("Speicher antwortet mit HTTP %d", resp.StatusCode)
}

// do sends a signed request; body is re-created for each attempt.
func (s *S3) do(ctx context.Context, method string, u *url.URL, body func() (io.Reader, int64, string, error), hdr map[string]string) (*http.Response, error) {
	var last error
	for attempt := 0; attempt < 3; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return nil, ctx.Err()
			case <-time.After(time.Duration(attempt*2) * time.Second):
			}
		}
		var rd io.Reader
		var size int64
		hash := emptySHA
		if body != nil {
			var err error
			rd, size, hash, err = body()
			if err != nil {
				return nil, err
			}
		}
		req, err := http.NewRequestWithContext(ctx, method, u.String(), rd)
		if err != nil {
			return nil, err
		}
		if body != nil {
			req.ContentLength = size
		}
		for k, v := range hdr {
			req.Header.Set(k, v)
		}
		sign(req, hash, s.access, s.secret, s.region, s.now())
		resp, err := s.hc.Do(req)
		if err != nil {
			last = fmt.Errorf("Speicher nicht erreichbar: %w", err)
			continue
		}
		if resp.StatusCode >= 500 {
			last = readS3Err(resp)
			resp.Body.Close()
			continue
		}
		if resp.StatusCode >= 300 {
			defer resp.Body.Close()
			return nil, readS3Err(resp)
		}
		return resp, nil
	}
	return nil, last
}

func fileBody(path string) func() (io.Reader, int64, string, error) {
	return func() (io.Reader, int64, string, error) {
		f, err := os.Open(path)
		if err != nil {
			return nil, 0, "", err
		}
		h := sha256.New()
		n, err := io.Copy(h, f)
		if err != nil {
			f.Close()
			return nil, 0, "", err
		}
		if _, err := f.Seek(0, 0); err != nil {
			f.Close()
			return nil, 0, "", err
		}
		return autoClose{f}, n, hex.EncodeToString(h.Sum(nil)), nil
	}
}

type autoClose struct{ f *os.File }

func (a autoClose) Read(p []byte) (int, error) {
	n, err := a.f.Read(p)
	if err == io.EOF {
		a.f.Close()
	}
	return n, err
}

func bytesBody(b []byte) func() (io.Reader, int64, string, error) {
	return func() (io.Reader, int64, string, error) {
		return bytes.NewReader(b), int64(len(b)), sha256hex(b), nil
	}
}

func (s *S3) Put(ctx context.Context, key, file string) error {
	fi, err := os.Stat(file)
	if err != nil {
		return err
	}
	u := s.objectURL(s.fullKey(key), nil)
	if fi.Size() < multipartFrom {
		resp, err := s.do(ctx, "PUT", u, fileBody(file), nil)
		if err != nil {
			return err
		}
		resp.Body.Close()
		return nil
	}
	return s.multipart(ctx, s.fullKey(key), file)
}

func (s *S3) multipart(ctx context.Context, key, file string) error {
	resp, err := s.do(ctx, "POST", s.objectURL(key, url.Values{"uploads": {""}}), nil, nil)
	if err != nil {
		return err
	}
	var init struct {
		UploadID string `xml:"UploadId"`
	}
	err = xml.NewDecoder(resp.Body).Decode(&init)
	resp.Body.Close()
	if err != nil || init.UploadID == "" {
		return errors.New("Mehrteiliger Upload konnte nicht gestartet werden")
	}
	abort := func() {
		if r, err := s.do(context.Background(), "DELETE", s.objectURL(key, url.Values{"uploadId": {init.UploadID}}), nil, nil); err == nil {
			r.Body.Close()
		}
	}
	f, err := os.Open(file)
	if err != nil {
		abort()
		return err
	}
	defer f.Close()
	type part struct {
		N    int    `xml:"PartNumber"`
		ETag string `xml:"ETag"`
	}
	var parts []part
	buf := make([]byte, partSize)
	for n := 1; ; n++ {
		m, err := io.ReadFull(f, buf)
		if m == 0 {
			break
		}
		if err != nil && err != io.ErrUnexpectedEOF {
			abort()
			return err
		}
		chunk := buf[:m]
		q := url.Values{"partNumber": {strconv.Itoa(n)}, "uploadId": {init.UploadID}}
		resp, err := s.do(ctx, "PUT", s.objectURL(key, q), bytesBody(chunk), nil)
		if err != nil {
			abort()
			return fmt.Errorf("Teil %d: %w", n, err)
		}
		parts = append(parts, part{N: n, ETag: resp.Header.Get("ETag")})
		resp.Body.Close()
		if m < partSize {
			break
		}
	}
	var body bytes.Buffer
	body.WriteString("<CompleteMultipartUpload>")
	for _, p := range parts {
		fmt.Fprintf(&body, "<Part><PartNumber>%d</PartNumber><ETag>%s</ETag></Part>", p.N, xmlEscape(p.ETag))
	}
	body.WriteString("</CompleteMultipartUpload>")
	resp, err = s.do(ctx, "POST", s.objectURL(key, url.Values{"uploadId": {init.UploadID}}), bytesBody(body.Bytes()), map[string]string{"Content-Type": "application/xml"})
	if err != nil {
		abort()
		return err
	}
	defer resp.Body.Close()
	b, _ := io.ReadAll(io.LimitReader(resp.Body, 8192))
	if bytes.Contains(b, []byte("<Error>")) { // S3 kann trotz 200 einen Fehler melden
		abort()
		return errors.New("Mehrteiliger Upload wurde vom Speicher abgelehnt")
	}
	return nil
}

func xmlEscape(s string) string {
	var b strings.Builder
	_ = xml.EscapeText(&b, []byte(s))
	return b.String()
}

func (s *S3) Get(ctx context.Context, key string, w io.Writer) error {
	resp, err := s.do(ctx, "GET", s.objectURL(s.fullKey(key), nil), nil, nil)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	_, err = io.Copy(w, resp.Body)
	return err
}

func (s *S3) Delete(ctx context.Context, key string) error {
	resp, err := s.do(ctx, "DELETE", s.objectURL(s.fullKey(key), nil), nil, nil)
	if err != nil {
		return err
	}
	resp.Body.Close()
	return nil
}

func (s *S3) List(ctx context.Context, prefix string) ([]Object, error) {
	full := s.fullKey(prefix)
	var out []Object
	token := ""
	for page := 0; page < 1000; page++ {
		q := url.Values{"list-type": {"2"}, "prefix": {full}}
		if token != "" {
			q.Set("continuation-token", token)
		}
		resp, err := s.do(ctx, "GET", s.objectURL("", q), nil, nil)
		if err != nil {
			return nil, err
		}
		var res struct {
			Contents []struct {
				Key          string    `xml:"Key"`
				Size         int64     `xml:"Size"`
				LastModified time.Time `xml:"LastModified"`
			} `xml:"Contents"`
			IsTruncated bool   `xml:"IsTruncated"`
			Next        string `xml:"NextContinuationToken"`
		}
		err = xml.NewDecoder(resp.Body).Decode(&res)
		resp.Body.Close()
		if err != nil {
			return nil, fmt.Errorf("Antwort des Speichers unlesbar: %w", err)
		}
		for _, c := range res.Contents {
			k := c.Key
			if s.prefix != "" {
				k = strings.TrimPrefix(k, s.prefix+"/")
			}
			out = append(out, Object{Key: k, Size: c.Size, ModTime: c.LastModified})
		}
		if !res.IsTruncated || res.Next == "" {
			break
		}
		token = res.Next
	}
	return out, nil
}
