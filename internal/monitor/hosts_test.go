package monitor

import (
	"strings"
	"testing"
	"time"

	"github.com/yude/tunwatch/internal/model"
)

func TestHostsParsingAndRepresentative(t *testing.T) {
	h, err := parseHosts(strings.NewReader("# comment\n192.0.2.10 server.example alias # x\n2001:db8::1 server.example\n192.0.2.11 other.example\n"), Hosts{Names: map[string]string{}, File: "example.hosts"})
	if err != nil {
		t.Fatal(err)
	}
	got := h.Lookup([]string{"192.0.2.10", "2001:db8::1"})
	if got.Value != "server.example" || got.Source != "hosts_file" || got.Detail != "example.hosts · 192.0.2.10" {
		t.Fatalf("bad hint: %+v", got)
	}
	if h.Lookup([]string{"192.0.2.11", "192.0.2.10"}).Value != "server.example" {
		t.Fatal("lowest IPv4 name was not selected")
	}
	for _, input := range []string{"invalid host", "192.0.2.1", "192.0.2.1 one\n192.0.2.1 two"} {
		if _, err := parseHosts(strings.NewReader(input), Hosts{Names: map[string]string{}}); err == nil {
			t.Fatalf("accepted %q", input)
		}
	}
}

func TestHostsGatewayRepresentative(t *testing.T) {
	h := Hosts{Names: map[string]string{
		"192.0.2.10":  "printer.example",
		"192.0.2.11":  "EdgeGW.example",
		"192.0.2.12":  "othergw.example",
		"192.0.2.13":  "edgegw.example",
		"192.0.2.14":  "camera.example",
		"192.0.2.2":   "low.example",
		"2001:db8::1": "ipv6gw.example",
	}, File: "tun.hosts"}
	for _, tc := range []struct {
		name string
		ips  []string
		want string
	}{
		{"gateway after other name", []string{"192.0.2.10", "192.0.2.11"}, "EdgeGW.example"},
		{"gateway before other name", []string{"192.0.2.11", "192.0.2.10"}, "EdgeGW.example"},
		{"same gateway on multiple IPs", []string{"192.0.2.11", "192.0.2.13", "192.0.2.10"}, "EdgeGW.example"},
		{"distinct gateway names use lower gateway IP", []string{"192.0.2.12", "192.0.2.11"}, "EdgeGW.example"},
		{"gateway beats lower IPv4", []string{"192.0.2.2", "192.0.2.11"}, "EdgeGW.example"},
		{"IPv6 gateway does not win", []string{"192.0.2.14", "192.0.2.10", "2001:db8::1"}, "printer.example"},
		{"numeric IP order, not lexical or observation order", []string{"192.0.2.10", "192.0.2.2"}, "low.example"},
		{"lowest named IP when lower IP has no mapping", []string{"192.0.2.1", "192.0.2.14", "192.0.2.10"}, "printer.example"},
		{"one IPv4 is insufficient", []string{"192.0.2.10", "2001:db8::1"}, ""},
		{"duplicate IPv4 is insufficient", []string{"192.0.2.10", "192.0.2.10", "2001:db8::1"}, ""},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := h.Lookup(tc.ips)
			if got.Value != tc.want {
				t.Fatalf("Lookup(%v) = %+v, want %q", tc.ips, got, tc.want)
			}
			if tc.want != "" && got.Source != "hosts_file" {
				t.Fatalf("lost source: %+v", got)
			}
		})
	}
	got := h.Lookup([]string{"192.0.2.10", "192.0.2.11"})
	if got.Detail != "tun.hosts · 192.0.2.11" {
		t.Fatalf("wrong representative IP: %+v", got)
	}
	got = h.Lookup([]string{"192.0.2.10", "192.0.2.2"})
	if got.Detail != "tun.hosts · 192.0.2.2" {
		t.Fatalf("wrong lowest-IP source: %+v", got)
	}
}

func TestHostsGatewayRepresentativeAfterSecondObservation(t *testing.T) {
	_, _, e, now := testEngine()
	e.SetHosts(Hosts{Names: map[string]string{
		"192.0.2.10": "device.example",
		"192.0.2.11": "site-gw.example",
	}, File: "tun.hosts"})
	for _, ip := range []string{"192.0.2.10", "192.0.2.11"} {
		o := observation(now)
		o.IP = ip
		if err := e.Observe(o); err != nil {
			t.Fatal(err)
		}
	}
	devices := e.Devices()
	if len(devices) != 1 || devices[0].HostsName.Value != "site-gw.example" {
		t.Fatalf("gateway was not selected after second IP: %+v", devices)
	}
}

func TestHostsLowestIPv4AfterSecondObservation(t *testing.T) {
	_, _, e, now := testEngine()
	e.SetHosts(Hosts{Names: map[string]string{
		"192.0.2.10": "lower.example",
		"192.0.2.20": "higher.example",
	}, File: "tun.hosts"})
	for _, ip := range []string{"192.0.2.20", "192.0.2.10"} {
		o := observation(now)
		o.IP = ip
		if err := e.Observe(o); err != nil {
			t.Fatal(err)
		}
	}
	devices := e.Devices()
	if len(devices) != 1 || devices[0].HostsName.Value != "lower.example" || devices[0].HostsName.Detail != "tun.hosts · 192.0.2.10" {
		t.Fatalf("lowest IPv4 was not selected after second IP: %+v", devices)
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
