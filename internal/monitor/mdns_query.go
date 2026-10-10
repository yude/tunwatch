package monitor

import (
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strings"
)

func reverseName(ip string) string {
	a, err := netip.ParseAddr(ip)
	if err != nil {
		return ""
	}
	if a.Is4() {
		b := a.As4()
		return fmt.Sprintf("%d.%d.%d.%d.in-addr.arpa", b[3], b[2], b[1], b[0])
	}
	b := a.As16()
	const hex = "0123456789abcdef"
	var s strings.Builder
	for i := len(b) - 1; i >= 0; i-- {
		s.WriteByte(hex[b[i]&15])
		s.WriteByte('.')
		s.WriteByte(hex[b[i]>>4])
		s.WriteByte('.')
	}
	return s.String() + "ip6.arpa"
}

// IPv4 link-local multicast query with QU set to request unicast responses.
// The existing packet socket receives them without taking over the host mDNS port.
func mdnsRequest(mac net.HardwareAddr, src, target netip.Addr) []byte {
	q := make([]byte, 12)
	q[5] = 1
	for _, label := range strings.Split(reverseName(target.String()), ".") {
		q = append(q, byte(len(label)))
		q = append(q, label...)
	}
	q = append(q, 0, 0, 12, 0x80, 1) // PTR, IN with QU; no service enumeration
	b := make([]byte, 14+20+8+len(q))
	copy(b, []byte{1, 0, 0x5e, 0, 0, 0xfb})
	copy(b[6:], mac)
	binary.BigEndian.PutUint16(b[12:14], 0x800)
	ip := b[14:34]
	ip[0] = 0x45
	ip[8] = 255
	ip[9] = 17
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(b)-14))
	a := src.As4()
	copy(ip[12:16], a[:])
	copy(ip[16:20], []byte{224, 0, 0, 251})
	var sum uint32
	for i := 0; i < 20; i += 2 {
		sum += uint32(binary.BigEndian.Uint16(ip[i : i+2]))
	}
	for sum>>16 != 0 {
		sum = (sum & 65535) + (sum >> 16)
	}
	binary.BigEndian.PutUint16(ip[10:12], ^uint16(sum))
	udp := b[34:]
	binary.BigEndian.PutUint16(udp[:2], 5353)
	binary.BigEndian.PutUint16(udp[2:4], 5353)
	binary.BigEndian.PutUint16(udp[4:6], uint16(len(udp))) // zero UDP checksum is valid for IPv4
	copy(udp[8:], q)
	return b
}
