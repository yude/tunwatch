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
func TestVRRPAdvertisementAndVirtualARP(t *testing.T) {
	frame := make([]byte, 14+20+12)
	copy(frame[6:12], []byte{0, 17, 34, 51, 68, 85})
	binary.BigEndian.PutUint16(frame[12:14], 0x800)
	ip := frame[14:]
	ip[0], ip[8], ip[9] = 0x45, 255, 112
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(ip)))
	copy(ip[12:16], []byte{192, 0, 2, 10})
	copy(ip[16:20], []byte{224, 0, 0, 18})
	vrrp := ip[20:]
	copy(vrrp, []byte{0x31, 42, 100, 1, 0, 100, 0, 0, 192, 0, 2, 1})
	binary.BigEndian.PutUint16(vrrp[6:8], testChecksum(vrrp))
	o, ok := Decode(frame, "eth0", time.Now())
	if !ok || o.IP != "192.0.2.10" || o.Source != "vrrp" || len(o.VIPs) != 1 || o.VIPs[0].IP != "192.0.2.1" || o.VIPs[0].VRID != 42 {
		t.Fatalf("VRRP advertisement not decoded: %+v, %v", o, ok)
	}
	ip[8] = 64
	if _, ok := Decode(frame, "eth0", time.Now()); ok {
		t.Fatal("accepted off-link VRRP")
	}
	ip[8] = 255
	vrrp[11]++
	if _, ok := Decode(frame, "eth0", time.Now()); ok {
		t.Fatal("accepted corrupt VRRP checksum")
	}

	arpFrame := arp()
	copy(arpFrame[6:12], []byte{0, 17, 34, 51, 68, 85})
	copy(arpFrame[22:28], []byte{0, 0, 0x5e, 0, 1, 42})
	o, ok = Decode(arpFrame, "eth0", time.Now())
	if !ok || o.MAC != "00:00:5e:00:01:2a" || len(o.VIPs) != 1 || o.VIPs[0].Source != "vrrp_mac" {
		t.Fatalf("virtual ARP not decoded: %+v, %v", o, ok)
	}
}

func TestVRRPv2AndLegacyV3Checksums(t *testing.T) {
	src, dst := []byte{192, 0, 2, 10}, []byte{224, 0, 0, 18}
	v2 := []byte{0x21, 7, 100, 1, 0, 1, 0, 0, 192, 0, 2, 1, 0, 0, 0, 0, 0, 0, 0, 0}
	binary.BigEndian.PutUint16(v2[6:8], testChecksum(v2))
	if vips, ok := decodeVRRPIPv4(v2, src, dst, 255); !ok || len(vips) != 1 || vips[0].IP != "192.0.2.1" {
		t.Fatalf("valid v2 advertisement rejected: %+v, %v", vips, ok)
	}
	v3 := []byte{0x31, 7, 100, 1, 0, 100, 0, 0, 192, 0, 2, 1}
	withPseudo := append(vrrpPseudoHeader(src, dst, len(v3)), v3...)
	binary.BigEndian.PutUint16(v3[6:8], testChecksum(withPseudo))
	if vips, ok := decodeVRRPIPv4(v3, src, dst, 255); !ok || len(vips) != 1 {
		t.Fatalf("legacy v3 checksum rejected: %+v, %v", vips, ok)
	}
}

func testChecksum(p []byte) uint16 {
	var sum uint32
	for i := 0; i < len(p); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i:]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}
func FuzzDecode(f *testing.F) {
	f.Add(arp())
	f.Add([]byte{})
	f.Fuzz(func(t *testing.T, b []byte) { Decode(b, "test0", time.Unix(0, 0)) })
}
