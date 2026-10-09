package monitor

import (
	"encoding/binary"
	"net"
	"net/netip"
	"testing"
	"time"
)

func arp() []byte {
	mac, _ := net.ParseMAC("00:11:22:33:44:55")
	return arpRequest(mac, netip.MustParseAddr("192.0.2.10"), netip.MustParseAddr("192.0.2.1"))
}
func TestARPOnlySourceIsPresent(t *testing.T) {
	o, ok := Decode(arp(), "eth0", time.Now())
	if !ok || o.IP != "192.0.2.10" || o.MAC != "00:11:22:33:44:55" {
		t.Fatalf("bad ARP: %+v", o)
	}
	b := arp()
	b[22] = 2
	if _, ok = Decode(b, "eth0", time.Now()); ok {
		t.Fatal("accepted mismatched sender")
	}
	b = arp()
	for i := 28; i < 32; i++ {
		b[i] = 0
	}
	if _, ok = Decode(b, "eth0", time.Now()); ok {
		t.Fatal("DAD probe claimed an IP")
	}
}
func TestVLANIsolation(t *testing.T) {
	a := arp()
	b := append([]byte{}, a[:12]...)
	b = append(b, 0x81, 0, 0, 10)
	b = append(b, a[12:]...)
	o, ok := Decode(b, "eth0", time.Now())
	if !ok || o.VLAN != "10" {
		t.Fatalf("VLAN: %+v", o)
	}
}
func TestDHCPHostnameAndSpoofedRelay(t *testing.T) {
	b := make([]byte, 14+20+8+256)
	copy(b[6:12], []byte{0, 17, 34, 51, 68, 85})
	binary.BigEndian.PutUint16(b[12:14], 0x800)
	p := b[14:]
	p[0] = 0x45
	p[8] = 64
	p[9] = 17
	binary.BigEndian.PutUint16(p[2:4], uint16(len(p)))
	u := p[20:]
	binary.BigEndian.PutUint16(u[:2], 68)
	binary.BigEndian.PutUint16(u[2:4], 67)
	binary.BigEndian.PutUint16(u[4:6], uint16(len(u)))
	d := u[8:]
	d[0] = 1
	d[1] = 1
	d[2] = 6
	copy(d[28:34], b[6:12])
	copy(d[236:240], []byte{99, 130, 83, 99})
	copy(d[240:], []byte{12, 4, 't', 'e', 's', 't', 60, 4, 'M', 'S', 'F', 'T', 255})
	o, ok := Decode(b, "eth0", time.Now())
	if !ok || o.Hostname != "test" || osHint(o).Value != "Windows" {
		t.Fatalf("DHCP: %+v", o)
	}
	d[28] = 2
	if _, ok = Decode(b, "eth0", time.Now()); ok {
		t.Fatal("accepted relay as client")
	}
}
func TestNDPSenderNotTarget(t *testing.T) {
	b := make([]byte, 14+64)
	copy(b[6:12], []byte{0, 17, 34, 51, 68, 85})
	binary.BigEndian.PutUint16(b[12:14], 0x86dd)
	p := b[14:]
	p[0] = 0x60
	p[6] = 58
	p[7] = 255
	s := netip.MustParseAddr("fe80::1234").As16()
	copy(p[8:24], s[:])
	p[40] = 135
	o, ok := Decode(b, "eth0", time.Now())
	if !ok || o.Source != "ndp" || o.IP != "fe80::1234" {
		t.Fatalf("NDP: %+v", o)
	}
	p[7] = 64
	if _, ok = Decode(b, "eth0", time.Now()); ok {
		t.Fatal("accepted invalid NDP hop limit")
	}
}
func FuzzDecode(f *testing.F) {
	f.Add(arp())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b, "test0", time.Unix(0, 0)) })
}
