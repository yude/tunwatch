package monitor

import (
	"github.com/yude/tunwatch/internal/model"
	"strings"
	"testing"
	"time"
)

func TestHostsParsingAndAmbiguity(t *testing.T) {
	h, err := parseHosts(strings.NewReader("# comment\n192.0.2.10 server.example alias # x\n2001:db8::1 server.example\n192.0.2.11 other.example\n"), Hosts{Names: map[string]string{}, File: "example.hosts"})
	if err != nil {
		t.Fatal(err)
	}
	got := h.Lookup([]string{"192.0.2.10", "2001:db8::1"})
	if got.Value != "server.example" || got.Source != "hosts_file" || got.Detail != "example.hosts · 192.0.2.10" {
		t.Fatalf("bad hint: %+v", got)
	}
	if h.Lookup([]string{"192.0.2.10", "192.0.2.11"}).Value != "" {
		t.Fatal("ambiguous names selected")
	}
	for _, input := range []string{"invalid host", "192.0.2.1", "192.0.2.1 one\n192.0.2.1 two"} {
		if _, err := parseHosts(strings.NewReader(input), Hosts{Names: map[string]string{}}); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}
func TestHostsEnrichmentDoesNotCreatePresenceOrReplaceObservedName(t *testing.T) {
	c, j, e, now := testEngine()
	h := Hosts{Names: map[string]string{"192.0.2.10": "configured.example"}, File: "example.hosts"}
	e.SetHosts(h)
	if len(e.Devices()) != 0 {
		t.Fatal("hosts created a device")
	}
	o := observation(now)
	o.IP = "192.0.2.10"
	o.Hostname = "wire.local"
	o.Source = "mdns"
	if err := e.Observe(o); err != nil {
		t.Fatal(err)
	}
	d := e.Devices()[0]
	if d.HostsName.Value != "configured.example" || d.Hostname.Value != "wire.local" {
		t.Fatalf("lost provenance: %+v", d)
	}
	e = NewEngine(c, j, Vendors{}, map[string]model.Device{d.ID: d}, now.Add(time.Second))
	e.SetHosts(Hosts{})
	d = e.Devices()[0]
	if d.HostsName.Value != "" || d.Hostname.Value != "wire.local" || d.Status != "unknown" {
		t.Fatalf("stale mapping or false presence: %+v", d)
	}
}
