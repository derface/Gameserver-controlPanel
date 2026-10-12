// Package upnp opens ports in the home router with UPnP IGD (Internet
// Gateway Device), the same way game consoles and Steam do it. Only the
// standard library is used: SSDP to find the router, a short XML device
// description, and SOAP calls to WANIPConnection / WANPPPConnection.
package upnp

import (
	"bytes"
	"context"
	"encoding/xml"
	"errors"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

// Client talks to one router.
type Client struct {
	Location    string // URL of the device description
	ControlURL  string
	ServiceType string
	LocalIP     string // our address as the router sees it
	Name        string // friendly name of the router, e.g. "FRITZ!Box 6690 Cable"
	http        *http.Client
}

var searchTargets = []string{
	"urn:schemas-upnp-org:device:InternetGatewayDevice:2",
	"urn:schemas-upnp-org:device:InternetGatewayDevice:1",
	"urn:schemas-upnp-org:service:WANIPConnection:2",
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

var serviceTypes = []string{
	"urn:schemas-upnp-org:service:WANIPConnection:2",
	"urn:schemas-upnp-org:service:WANIPConnection:1",
	"urn:schemas-upnp-org:service:WANPPPConnection:1",
}

// ErrNotFound means no router answered the search.
var ErrNotFound = errors.New("Kein Router mit UPnP gefunden. In der Fritzbox unter Heimnetz → Netzwerk → Netzwerkeinstellungen „Statusinformationen über UPnP übertragen“ einschalten.")

// Discover searches the local network for an Internet gateway.
func Discover(ctx context.Context, wait time.Duration) (*Client, error) {
	conn, err := net.ListenPacket("udp4", ":0")
	if err != nil {
		return nil, err
	}
	defer conn.Close()
	dst := &net.UDPAddr{IP: net.IPv4(239, 255, 255, 250), Port: 1900}
	for round := 0; round < 2; round++ {
		for _, st := range searchTargets {
			msg := "M-SEARCH * HTTP/1.1\r\nHOST: 239.255.255.250:1900\r\nMAN: \"ssdp:discover\"\r\nMX: 2\r\nST: " + st + "\r\n\r\n"
			_, _ = conn.WriteTo([]byte(msg), dst)
		}
	}
	deadline := time.Now().Add(wait)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	_ = conn.SetReadDeadline(deadline)
	seen := map[string]bool{}
	var lastErr error
	buf := make([]byte, 4096)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		loc := header(string(buf[:n]), "location")
		if loc == "" || seen[loc] {
			continue
		}
		seen[loc] = true
		c, err := New(ctx, loc)
		if err == nil {
			return c, nil
		}
		lastErr = err
	}
	if lastErr != nil {
		return nil, lastErr
	}
	return nil, ErrNotFound
}

func header(resp, name string) string {
	for _, line := range strings.Split(resp, "\r\n") {
		k, v, ok := strings.Cut(line, ":")
		if ok && strings.EqualFold(strings.TrimSpace(k), name) {
			return strings.TrimSpace(v)
		}
	}
	return ""
}

type xmlDevice struct {
	FriendlyName string       `xml:"friendlyName"`
	Services     []xmlService `xml:"serviceList>service"`
	Devices      []xmlDevice  `xml:"deviceList>device"`
}

type xmlService struct {
	ServiceType string `xml:"serviceType"`
	ControlURL  string `xml:"controlURL"`
}

type xmlRoot struct {
	URLBase string    `xml:"URLBase"`
	Device  xmlDevice `xml:"device"`
}

// New reads the device description at location and picks the WAN
// connection service.
func New(ctx context.Context, location string) (*Client, error) {
	hc := &http.Client{Timeout: 8 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", location, nil)
	if err != nil {
		return nil, err
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Router-Beschreibung nicht lesbar: %v", err)
	}
	defer resp.Body.Close()
	var root xmlRoot
	if err := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20)).Decode(&root); err != nil {
		return nil, fmt.Errorf("Router-Beschreibung nicht lesbar: %v", err)
	}
	base, err := url.Parse(location)
	if err != nil {
		return nil, err
	}
	if root.URLBase != "" {
		if b, err := url.Parse(root.URLBase); err == nil {
			base = b
		}
	}
	var found *xmlService
	var walk func(d xmlDevice)
	walk = func(d xmlDevice) {
		for i := range d.Services {
			s := d.Services[i]
			for _, want := range serviceTypes {
				if strings.TrimSpace(s.ServiceType) == want && (found == nil || rank(want) < rank(found.ServiceType)) {
					found = &s
				}
			}
		}
		for _, sub := range d.Devices {
			walk(sub)
		}
	}
	walk(root.Device)
	if found == nil {
		return nil, errors.New("Der Router kann keine Portfreigaben per UPnP (kein WANIPConnection-Dienst)")
	}
	ctl, err := base.Parse(strings.TrimSpace(found.ControlURL))
	if err != nil {
		return nil, err
	}
	c := &Client{Location: location, ControlURL: ctl.String(), ServiceType: strings.TrimSpace(found.ServiceType),
		Name: cleanName(root.Device.FriendlyName), http: hc}
	// our own address on the way to the router
	host := ctl.Host
	if !strings.Contains(host, ":") {
		host += ":80"
	}
	if conn, err := net.Dial("udp4", host); err == nil {
		c.LocalIP = conn.LocalAddr().(*net.UDPAddr).IP.String()
		conn.Close()
	}
	return c, nil
}

func rank(st string) int {
	for i, s := range serviceTypes {
		if s == st {
			return i
		}
	}
	return len(serviceTypes)
}

// Error is a UPnP error answer of the router.
type Error struct {
	Code int
	Desc string
}

func (e *Error) Error() string {
	if msg, ok := codeText[e.Code]; ok {
		return msg
	}
	return fmt.Sprintf("Router meldet Fehler %d (%s)", e.Code, e.Desc)
}

var codeText = map[int]string{
	606: "Der Router erlaubt diesem Rechner keine Portfreigaben. In der Fritzbox beim Gerät „Selbstständige Portfreigaben erlauben“ anhaken.",
	714: "Diese Freigabe gibt es im Router nicht.",
	718: "Der Port ist im Router schon an ein anderes Gerät freigegeben.",
	724: "Der Router verlangt gleichen internen und externen Port.",
	725: "Der Router erlaubt nur dauerhafte Freigaben.",
}

// Code returns the UPnP error code of err, or 0.
func Code(err error) int {
	var e *Error
	if errors.As(err, &e) {
		return e.Code
	}
	return 0
}

type kv struct{ k, v string }

func (c *Client) call(ctx context.Context, action string, args []kv) (map[string]string, error) {
	var b bytes.Buffer
	b.WriteString(`<?xml version="1.0"?>` +
		`<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/"><s:Body>`)
	fmt.Fprintf(&b, `<u:%s xmlns:u="%s">`, action, c.ServiceType)
	for _, a := range args {
		fmt.Fprintf(&b, "<%s>", a.k)
		_ = xml.EscapeText(&b, []byte(a.v))
		fmt.Fprintf(&b, "</%s>", a.k)
	}
	fmt.Fprintf(&b, `</u:%s></s:Body></s:Envelope>`, action)
	req, err := http.NewRequestWithContext(ctx, "POST", c.ControlURL, &b)
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", `"`+c.ServiceType+"#"+action+`"`)
	hc := c.http
	if hc == nil {
		hc = &http.Client{Timeout: 8 * time.Second}
	}
	resp, err := hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("Router antwortet nicht: %v", err)
	}
	defer resp.Body.Close()
	out := map[string]string{}
	dec := xml.NewDecoder(io.LimitReader(resp.Body, 1<<20))
	var cur string
	for {
		tok, err := dec.Token()
		if err != nil {
			break
		}
		switch t := tok.(type) {
		case xml.StartElement:
			cur = t.Name.Local
		case xml.CharData:
			if cur != "" {
				out[cur] += string(t)
			}
		case xml.EndElement:
			cur = ""
		}
	}
	if resp.StatusCode != 200 {
		code, _ := strconv.Atoi(strings.TrimSpace(out["errorCode"]))
		if code == 0 {
			return nil, fmt.Errorf("Router meldet HTTP %d bei %s", resp.StatusCode, action)
		}
		return nil, &Error{Code: code, Desc: strings.TrimSpace(out["errorDescription"])}
	}
	return out, nil
}

// ExternalIP returns the public address of the router.
func (c *Client) ExternalIP(ctx context.Context) (string, error) {
	r, err := c.call(ctx, "GetExternalIPAddress", nil)
	if err != nil {
		return "", err
	}
	return strings.TrimSpace(r["NewExternalIPAddress"]), nil
}

// Mapping is one port forwarding.
type Mapping struct {
	Client       string
	InternalPort int
	Description  string
	Enabled      bool
}

// Get returns the forwarding of an external port, or an *Error with code 714.
func (c *Client) Get(ctx context.Context, port int, proto string) (Mapping, error) {
	r, err := c.call(ctx, "GetSpecificPortMappingEntry", []kv{
		{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(port)}, {"NewProtocol", strings.ToUpper(proto)},
	})
	if err != nil {
		return Mapping{}, err
	}
	ip, _ := strconv.Atoi(strings.TrimSpace(r["NewInternalPort"]))
	return Mapping{Client: strings.TrimSpace(r["NewInternalClient"]), InternalPort: ip,
		Description: r["NewPortMappingDescription"], Enabled: strings.TrimSpace(r["NewEnabled"]) == "1"}, nil
}

// Add forwards external port to port on this machine. lease 0 = permanent.
func (c *Client) Add(ctx context.Context, port int, proto, desc string, lease int) error {
	_, err := c.call(ctx, "AddPortMapping", []kv{
		{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(port)}, {"NewProtocol", strings.ToUpper(proto)},
		{"NewInternalPort", strconv.Itoa(port)}, {"NewInternalClient", c.LocalIP}, {"NewEnabled", "1"},
		{"NewPortMappingDescription", desc}, {"NewLeaseDuration", strconv.Itoa(lease)},
	})
	return err
}

// Delete removes a forwarding.
func (c *Client) Delete(ctx context.Context, port int, proto string) error {
	_, err := c.call(ctx, "DeletePortMapping", []kv{
		{"NewRemoteHost", ""}, {"NewExternalPort", strconv.Itoa(port)}, {"NewProtocol", strings.ToUpper(proto)},
	})
	return err
}

// cleanName turns "InternetGatewayDeviceV2 - FRITZ!Box 6690 Cable" into
// "FRITZ!Box 6690 Cable".
func cleanName(n string) string {
	n = strings.TrimSpace(n)
	if i := strings.Index(n, " - "); i > 0 && strings.HasPrefix(strings.ToLower(n), "internetgateway") {
		n = strings.TrimSpace(n[i+3:])
	}
	return n
}
