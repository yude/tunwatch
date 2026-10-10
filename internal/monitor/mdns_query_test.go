package monitor

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"

	"github.com/yude/tunwatch/internal/model"
)

func encodedName(s string) []byte {
	b := []byte{}
	start := 0
	for i := 0; i <= len(s); i++ {
		if i == len(s) || s[i] == '.' {
			b = append(b, byte(i-start))
			b = append(b, s[start:i]...)
			start = i + 1
		}
	}
	return append(b, 0)
}
func TestMDNSReverseAnswer(t *testing.T) {
	for _, ip := range []string{"192.0.2.10", "fe80::1234"} {
		p := make([]byte, 12)
		p[2] = 0x84
		p[7] = 1
		p = append(p, encodedName(reverseName(ip))...)
		host := encodedName("test.local")
		p = append(p, 0, 12, 0, 1, 0, 0, 0, 120, 0, byte(len(host)))
		p = append(p, host...)
		o := model.Observation{IP: ip}
		mdns(p, &o)
		if o.Hostname != "test.local" {
			t.Fatalf("PTR: %+v", o)
		}
		o = model.Observation{IP: "192.0.2.99"}
		mdns(p, &o)
		if o.Hostname != "" {
			t.Fatal("accepted another address")
		}
		p[len(p)-len(host)-3] = 0 // zero TTL
		o = model.Observation{IP: ip}
		mdns(p, &o)
		if o.Hostname != "" {
			t.Fatal("accepted goodbye")
		}
	}
}
func TestMDNSQueryFrame(t *testing.T) {
	mac, _ := net.ParseMAC("00:11:22:33:44:55")
	b := mdnsRequest(mac, netip.MustParseAddr("192.0.2.1"), netip.MustParseAddr("192.0.2.10"))
	if b[22] != 255 || net.IP(b[30:34]).String() != "224.0.0.251" || binary.BigEndian.Uint16(b[34:36]) != 5353 {
		t.Fatal("wrong multicast destination/TTL/port")
	}
	var sum uint32
	for i := 14; i < 34; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(b[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	if sum != 65535 {
		t.Fatal("bad IPv4 checksum")
	}
	q := b[42:]
	name, end, ok := dnsName(q, 12)
	if !ok || name != "10.2.0.192.in-addr.arpa" || binary.BigEndian.Uint16(q[end:end+2]) != 12 || binary.BigEndian.Uint16(q[end+2:end+4]) != 0x8001 {
		t.Fatal("wrong PTR query")
	}
}
func TestNameProbeScopeAndBackoff(t *testing.T) {
	now := time.Now()
	local := []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24")}
	p := nameProbes{}
	o := model.Observation{MAC: "00:11:22:33:44:55", IP: "198.51.100.1", At: now}
	p.observe(o, local, 2)
	o.IP = "192.0.2.10"
	o.VLAN = "20"
	p.observe(o, local, 2)
	if len(p) != 0 {
		t.Fatal("outside interface scope")
	}
	o.VLAN = ""
	p.observe(o, local, 2)
	if _, ok := p.next(now); !ok {
		t.Fatal("no initial probe")
	}
	if _, ok := p.next(now.Add(time.Minute)); ok {
		t.Fatal("probe retry too soon")
	}
	if _, ok := p.next(now.Add(5 * time.Minute)); !ok {
		t.Fatal("no retry")
	}
	o.Hostname = "test.local"
	p.observe(o, local, 2)
	if _, ok := p.next(now.Add(6 * time.Minute)); ok {
		t.Fatal("queried known name")
	}
	p.next(now.Add(11 * time.Minute))
	if len(p) != 0 {
		t.Fatal("stale entry retained")
	}
	o.Hostname = ""
	o.At = now
	p.observe(o, local, 1)
	o.IP = "192.0.2.11"
	p.observe(o, local, 1)
	if len(p) != 1 {
		t.Fatal("probe queue exceeded capacity")
	}
}
func TestDHCPFQDN(t *testing.T) {
	for _, wire := range []bool{false, true} {
		p := make([]byte, 240)
		p[0] = 1
		p[1] = 1
		p[2] = 6
		copy(p[28:34], []byte{0, 17, 34, 51, 68, 85})
		copy(p[236:], []byte{99, 130, 83, 99})
		option := append([]byte{0, 0, 0}, []byte("host.example")...)
		if wire {
			option = append([]byte{4, 0, 0}, encodedName("host.example")...)
		}
		p = append(p, 81, byte(len(option)))
		p = append(p, option...)
		p = append(p, 255)
		o := model.Observation{MAC: "00:11:22:33:44:55"}
		dhcp(p, &o)
		if o.Hostname != "host.example" {
			t.Fatalf("FQDN: %+v", o)
		}
	}
	if _, _, ok := uncompressedName([]byte{0xc0, 0}); ok {
		t.Fatal("accepted compressed DHCP name")
	}
}
