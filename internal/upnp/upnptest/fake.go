package upnptest

import (
	"encoding/xml"
	"fmt"
	"github.com/derface/Gameserver-controlPanel/internal/upnp"
	"io"
	"net/http"
	"net/http/httptest"
	"regexp"
	"strings"
	"sync"
)

// Package upnptest provides a fake UPnP router for tests.
// Router is a tiny IGD: GET Desc() for the description URL.
type Router struct {
	mu       sync.Mutex
	Maps     map[string]upnp.Mapping // "UDP/15636"
	Deny     bool                    // answer 606 like a Fritzbox without permission
	srv      *httptest.Server
	Lease0No bool   // refuse lease 0
	WAN      string // external address, default 203.0.113.7
}

// New starts a fake router.
func New() *Router {
	f := &Router{Maps: map[string]upnp.Mapping{}}
	mux := http.NewServeMux()
	mux.HandleFunc("/desc.xml", func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<?xml version="1.0"?><root xmlns="urn:schemas-upnp-org:device-1-0"><device>
<friendlyName>FRITZ!Box Test</friendlyName><deviceList><device><deviceList><device>
<serviceList><service><serviceType>urn:schemas-upnp-org:service:WANIPConnection:1</serviceType>
<controlURL>/igdupnp/control/WANIPConn1</controlURL></service></serviceList>
</device></deviceList></device></deviceList></device></root>`)
	})
	field := func(body, name string) string {
		m := regexp.MustCompile(`<` + name + `>([^<]*)</` + name + `>`).FindStringSubmatch(body)
		if m == nil {
			return ""
		}
		return m[1]
	}
	fault := func(w http.ResponseWriter, code int) {
		w.WriteHeader(500)
		fmt.Fprintf(w, `<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><s:Fault><detail><UPnPError xmlns="urn:schemas-upnp-org:control-1-0"><errorCode>%d</errorCode><errorDescription>x</errorDescription></UPnPError></detail></s:Fault></s:Body></s:Envelope>`, code)
	}
	ok := func(w http.ResponseWriter, action string, kv ...string) {
		var b strings.Builder
		for i := 0; i < len(kv); i += 2 {
			b.WriteString("<" + kv[i] + ">")
			_ = xml.EscapeText(&b, []byte(kv[i+1]))
			b.WriteString("</" + kv[i] + ">")
		}
		fmt.Fprintf(w, `<?xml version="1.0"?><s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/"><s:Body><u:%sResponse xmlns:u="urn:schemas-upnp-org:service:WANIPConnection:1">%s</u:%sResponse></s:Body></s:Envelope>`, action, b.String(), action)
	}
	mux.HandleFunc("/igdupnp/control/WANIPConn1", func(w http.ResponseWriter, r *http.Request) {
		raw, _ := io.ReadAll(r.Body)
		body := string(raw)
		action := strings.Trim(r.Header.Get("SOAPAction"), `"`)
		action = action[strings.Index(action, "#")+1:]
		key := field(body, "NewProtocol") + "/" + field(body, "NewExternalPort")
		f.mu.Lock()
		defer f.mu.Unlock()
		switch action {
		case "GetExternalIPAddress":
			wan := f.WAN // handler already holds f.mu
			if wan == "" {
				wan = "203.0.113.7"
			}
			ok(w, action, "NewExternalIPAddress", wan)
		case "GetSpecificPortMappingEntry":
			m, found := f.Maps[key]
			if !found {
				fault(w, 714)
				return
			}
			ok(w, action, "NewInternalPort", fmt.Sprint(m.InternalPort), "NewInternalClient", m.Client, "NewEnabled", "1", "NewPortMappingDescription", m.Description)
		case "AddPortMapping":
			if f.Deny {
				fault(w, 606)
				return
			}
			if f.Lease0No && field(body, "NewLeaseDuration") == "0" {
				fault(w, 402)
				return
			}
			if m, found := f.Maps[key]; found && m.Client != field(body, "NewInternalClient") {
				fault(w, 718)
				return
			}
			var p int
			fmt.Sscan(field(body, "NewInternalPort"), &p)
			f.Maps[key] = upnp.Mapping{Client: field(body, "NewInternalClient"), InternalPort: p, Description: field(body, "NewPortMappingDescription"), Enabled: true}
			ok(w, action)
		case "DeletePortMapping":
			if _, found := f.Maps[key]; !found {
				fault(w, 714)
				return
			}
			delete(f.Maps, key)
			ok(w, action)
		default:
			fault(w, 401)
		}
	})
	f.srv = httptest.NewServer(mux)
	return f
}

// Desc is the URL of the device description.
func (f *Router) Desc() string { return f.srv.URL + "/desc.xml" }

// Close stops the router.
func (f *Router) Close() { f.srv.Close() }

// Get returns a mapping by "UDP/15636".
func (f *Router) Get(key string) (upnp.Mapping, bool) {
	f.mu.Lock()
	defer f.mu.Unlock()
	m, ok := f.Maps[key]
	return m, ok
}

// Set adds a mapping, e.g. a manual one of another device.
func (f *Router) Set(key string, m upnp.Mapping) {
	f.mu.Lock()
	f.Maps[key] = m
	f.mu.Unlock()
}

// Count returns the number of mappings.
func (f *Router) Count() int {
	f.mu.Lock()
	defer f.mu.Unlock()
	return len(f.Maps)
}
