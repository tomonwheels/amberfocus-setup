// Package upnp is a minimal, self-contained UPnP AVTransport client:
// SSDP discovery of MediaRenderers plus SetAVTransportURI / Play / Stop.
//
// This is an original, standalone implementation of the standard UPnP/DLNA
// AVTransport protocol (no proprietary amberSUITE code) so that amberFOCUS
// Setup stays cleanly GPL without linking anything proprietary.
package upnp

import (
	"encoding/xml"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/url"
	"strings"
	"time"
)

// Renderer is a discovered UPnP MediaRenderer with an AVTransport service.
type Renderer struct {
	UDN        string `json:"udn"`
	Name       string `json:"name"`
	IP         string `json:"ip"`
	ControlURL string `json:"-"`
	XMLBase    string `json:"-"`
}

// Discover finds MediaRenderers on the local network via SSDP M-SEARCH.
func Discover(timeout time.Duration) ([]Renderer, error) {
	conn, err := net.ListenUDP("udp4", &net.UDPAddr{IP: net.IPv4zero, Port: 0})
	if err != nil {
		return nil, fmt.Errorf("udp socket: %w", err)
	}
	defer conn.Close()

	mcast := &net.UDPAddr{IP: net.ParseIP("239.255.255.250"), Port: 1900}
	targets := []string{
		"urn:schemas-upnp-org:device:MediaRenderer:1",
		"upnp:rootdevice",
		"ssdp:all",
	}
	for _, st := range targets {
		msg := "M-SEARCH * HTTP/1.1\r\n" +
			"HOST: 239.255.255.250:1900\r\n" +
			"MAN: \"ssdp:discover\"\r\n" +
			"MX: 2\r\n" +
			"ST: " + st + "\r\n\r\n"
		for i := 0; i < 2; i++ {
			_, _ = conn.WriteToUDP([]byte(msg), mcast)
			time.Sleep(10 * time.Millisecond)
		}
	}

	_ = conn.SetReadDeadline(time.Now().Add(timeout))
	seen := map[string]bool{}
	byUDN := map[string]Renderer{}
	buf := make([]byte, 65536)
	for {
		n, _, err := conn.ReadFrom(buf)
		if err != nil {
			break
		}
		loc := extractHeader(string(buf[:n]), "LOCATION")
		if loc == "" || seen[loc] {
			continue
		}
		seen[loc] = true
		rs, err := fetchRenderers(loc)
		if err != nil {
			continue
		}
		for _, r := range rs {
			key := r.UDN
			if key == "" {
				key = loc
			}
			if _, ok := byUDN[key]; !ok {
				byUDN[key] = r
			}
		}
	}

	out := make([]Renderer, 0, len(byUDN))
	for _, r := range byUDN {
		out = append(out, r)
	}
	return out, nil
}

func fetchRenderers(location string) ([]Renderer, error) {
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Get(location)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	type service struct {
		ServiceType string `xml:"serviceType"`
		ControlURL  string `xml:"controlURL"`
	}
	type device struct {
		FriendlyName string    `xml:"friendlyName"`
		DeviceType   string    `xml:"deviceType"`
		UDN          string    `xml:"UDN"`
		ServiceList  struct {
			Services []service `xml:"service"`
		} `xml:"serviceList"`
		DeviceList struct {
			Devices []device `xml:"device"`
		} `xml:"deviceList"`
	}
	type root struct {
		XMLName xml.Name `xml:"root"`
		URLBase string   `xml:"URLBase"`
		Device  device   `xml:"device"`
	}

	clean := strings.ReplaceAll(string(raw), `xmlns="urn:schemas-upnp-org:device-1-0"`, "")
	var rt root
	if err := xml.Unmarshal([]byte(clean), &rt); err != nil {
		return nil, fmt.Errorf("device xml: %w", err)
	}
	base := strings.TrimSpace(rt.URLBase)
	if base == "" {
		base = location
	}

	var out []Renderer
	var walk func(d device)
	walk = func(d device) {
		control := ""
		for _, s := range d.ServiceList.Services {
			if strings.Contains(strings.ToLower(s.ServiceType), "avtransport") {
				control = resolveURL(base, location, s.ControlURL)
				break
			}
		}
		if control != "" {
			out = append(out, Renderer{
				UDN:        strings.TrimPrefix(d.UDN, "uuid:"),
				Name:       d.FriendlyName,
				IP:         hostOf(location),
				ControlURL: control,
				XMLBase:    base,
			})
		}
		for _, e := range d.DeviceList.Devices {
			walk(e)
		}
	}
	walk(rt.Device)
	return out, nil
}

// SetAVTransportURI points the renderer at the given stream URL.
func SetAVTransportURI(r *Renderer, streamURL string) error {
	body := fmt.Sprintf(`<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:SetAVTransportURI xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">
      <InstanceID>0</InstanceID>
      <CurrentURI>%s</CurrentURI>
      <CurrentURIMetaData></CurrentURIMetaData>
    </u:SetAVTransportURI>
  </s:Body>
</s:Envelope>`, streamURL)
	return soap(r, "urn:schemas-upnp-org:service:AVTransport:1#SetAVTransportURI", body)
}

// Play starts playback on the renderer.
func Play(r *Renderer) error {
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:Play xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">
      <InstanceID>0</InstanceID>
      <Speed>1</Speed>
    </u:Play>
  </s:Body>
</s:Envelope>`
	return soap(r, "urn:schemas-upnp-org:service:AVTransport:1#Play", body)
}

// Stop stops playback on the renderer.
func Stop(r *Renderer) error {
	body := `<?xml version="1.0"?>
<s:Envelope xmlns:s="http://schemas.xmlsoap.org/soap/envelope/" s:encodingStyle="http://schemas.xmlsoap.org/soap/encoding/">
  <s:Body>
    <u:Stop xmlns:u="urn:schemas-upnp-org:service:AVTransport:1">
      <InstanceID>0</InstanceID>
    </u:Stop>
  </s:Body>
</s:Envelope>`
	return soap(r, "urn:schemas-upnp-org:service:AVTransport:1#Stop", body)
}

func soap(r *Renderer, action, body string) error {
	req, err := http.NewRequest("POST", r.ControlURL, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", `text/xml; charset="utf-8"`)
	req.Header.Set("SOAPAction", fmt.Sprintf("%q", action))
	client := &http.Client{Timeout: 5 * time.Second}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("soap request: %w", err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != 200 {
		data, _ := io.ReadAll(resp.Body)
		return fmt.Errorf("soap %d: %s", resp.StatusCode, string(data))
	}
	return nil
}

func resolveURL(base, location, control string) string {
	if strings.HasPrefix(control, "http://") || strings.HasPrefix(control, "https://") {
		return control
	}
	if base != "" {
		if b, err := url.Parse(base); err == nil {
			if c, err := url.Parse(control); err == nil {
				return b.ResolveReference(c).String()
			}
		}
	}
	if l, err := url.Parse(location); err == nil {
		l.Path = control
		l.RawQuery = ""
		return l.String()
	}
	return control
}

func hostOf(rawURL string) string {
	if u, err := url.Parse(rawURL); err == nil {
		return u.Hostname()
	}
	return ""
}

func extractHeader(response, header string) string {
	needle := strings.ToLower(header) + ":"
	for _, line := range strings.Split(response, "\r\n") {
		if strings.HasPrefix(strings.ToLower(line), needle) {
			return strings.TrimSpace(line[len(header)+1:])
		}
	}
	return ""
}
