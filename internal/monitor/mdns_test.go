package monitor

import (
	"encoding/binary"
	"testing"

	"github.com/yude/tunwatch/internal/model"
)

func TestMDNSOnlyMatchingAddress(t *testing.T) {
	p := make([]byte, 12)
	p[2] = 0x84
	p[7] = 1
	p = append(p, 4, 't', 'e', 's', 't', 5, 'l', 'o', 'c', 'a', 'l', 0)
	p = append(p, 0, 1, 0x80, 1, 0, 0, 0, 120, 0, 4, 192, 0, 2, 10)
	o := model.Observation{IP: "192.0.2.10"}
	mdns(p, &o)
	if o.Hostname != "test.local" || o.Source != "mdns" {
		t.Fatalf("mDNS: %+v", o)
	}
	o = model.Observation{IP: "192.0.2.20"}
	mdns(p, &o)
	if o.Hostname != "" {
		t.Fatal("attributed another host's record")
	}
	binary.BigEndian.PutUint16(p[6:8], 65535)
	mdns(p, &o)
}
func TestDNSCompressionLoop(t *testing.T) {
	if _, _, ok := dnsName([]byte{0xc0, 0}, 0); ok {
		t.Fatal("accepted compression cycle")
	}
}
func FuzzMDNS(f *testing.F) {
	f.Add([]byte{0xc0, 0})
	f.Fuzz(func(t *testing.T, b []byte) { o := model.Observation{IP: "192.0.2.1"}; mdns(b, &o); dnsName(b, 0) })
}
