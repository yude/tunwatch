package monitor

import (
	"bytes"
	"encoding/binary"
	"fmt"
	"net"
	"net/netip"
	"strings"
	"time"

	"github.com/yude/tunwatch/internal/model"
)

func validMAC(b []byte) bool { return len(b) == 6 && b[0]&1 == 0 && !bytes.Equal(b, make([]byte, 6)) }
func usableIP(a netip.Addr) bool {
	return a.IsValid() && !a.IsUnspecified() && !a.IsMulticast() && !a.IsLoopback() && a.String() != "255.255.255.255"
}
func cleanName(s string) string {
	s = strings.TrimSuffix(strings.TrimSpace(s), ".")
	if len(s) == 0 || len(s) > 253 {
		return ""
	}
	for _, c := range s {
		if !(c >= 'a' && c <= 'z' || c >= 'A' && c <= 'Z' || c >= '0' && c <= '9' || c == '.' || c == '-' || c == '_') {
			return ""
		}
	}
	return s
}

// Decode keeps only source metadata. Destination addresses are never evidence of presence.
// Tagged frames retain their VLAN stack; monitor VLAN interfaces for offloaded VLANs.
func Decode(frame []byte, iface string, now time.Time) (model.Observation, bool) {
	o := model.Observation{Interface: iface, At: now}
	if len(frame) < 14 || !validMAC(frame[6:12]) {
		return o, false
	}
	o.MAC = net.HardwareAddr(frame[6:12]).String()
	typ := binary.BigEndian.Uint16(frame[12:14])
	p := frame[14:]
	for depth := 0; typ == 0x8100 || typ == 0x88a8; depth++ {
		if depth >= 2 || len(p) < 4 {
			return o, false
		}
		if o.VLAN != "" {
			o.VLAN += "."
		}
		o.VLAN += fmt.Sprint(binary.BigEndian.Uint16(p[:2]) & 0xfff)
		typ = binary.BigEndian.Uint16(p[2:4])
		p = p[4:]
	}
	switch typ {
	case 0x0806:
		if len(p) < 28 || binary.BigEndian.Uint16(p[:2]) != 1 || binary.BigEndian.Uint16(p[2:4]) != 0x800 || p[4] != 6 || p[5] != 4 {
			return o, false
		}
		op := binary.BigEndian.Uint16(p[6:8])
		if op != 1 && op != 2 || !bytes.Equal(p[8:14], frame[6:12]) {
			return o, false
		}
		a := netip.AddrFrom4([4]byte(p[14:18]))
		if !usableIP(a) {
			return o, false
		}
		o.IP = a.String()
		o.Source = "arp"
	case 0x0800:
		if len(p) < 20 || p[0]>>4 != 4 {
			return o, false
		}
		ihl := int(p[0]&15) * 4
		total := int(binary.BigEndian.Uint16(p[2:4]))
		if ihl < 20 || len(p) < ihl || total < ihl {
			return o, false
		}
		if total < len(p) {
			p = p[:total]
		}
		o.TTL = int(p[8])
		o.Source = "ipv4"
		a := netip.AddrFrom4([4]byte(p[12:16]))
		if usableIP(a) {
			o.IP = a.String()
		}
		if p[9] == 17 && binary.BigEndian.Uint16(p[6:8])&0x3fff == 0 && len(p) >= ihl+8 {
			u := p[ihl:]
			decodeMDNSUDP(u, &o)
			if binary.BigEndian.Uint16(u[:2]) == 68 && binary.BigEndian.Uint16(u[2:4]) == 67 {
				n := int(binary.BigEndian.Uint16(u[4:6]))
				if n <= len(u) && n >= 248 {
					dhcp(u[8:n], &o)
				}
			}
		}
		if o.IP == "" && o.Source != "dhcp" {
			return o, false
		}
	case 0x86dd:
		if len(p) < 40 || p[0]>>4 != 6 {
			return o, false
		}
		a := netip.AddrFrom16([16]byte(p[8:24]))
		if !usableIP(a) {
			return o, false
		}
		o.IP = a.String()
		o.Source = "ipv6"
		o.TTL = int(p[7])
		if p[6] == 17 && len(p) >= 48 {
			decodeMDNSUDP(p[40:], &o)
		}
		if p[6] == 58 && len(p) >= 64 && (p[40] == 135 || p[40] == 136) {
			if p[7] != 255 || p[41] != 0 {
				return o, false
			}
			o.Source = "ndp"
		}
	default:
		return o, false
	}
	return o, true
}
func dhcp(p []byte, o *model.Observation) {
	if len(p) < 240 || p[0] != 1 || p[1] != 1 || p[2] != 6 || !bytes.Equal(p[236:240], []byte{99, 130, 83, 99}) {
		return
	}
	mac, _ := net.ParseMAC(o.MAC)
	if !bytes.Equal(mac, p[28:34]) {
		return
	} // Do not attribute relayed requests to a router.
	o.Source = "dhcp"
	for i := 240; i < len(p); {
		code := p[i]
		i++
		if code == 255 {
			break
		}
		if code == 0 {
			continue
		}
		if i >= len(p) {
			break
		}
		n := int(p[i])
		i++
		if i+n > len(p) {
			break
		}
		switch code {
		case 12:
			o.Hostname = cleanName(string(p[i : i+n]))
		case 81:
			// RFC 4702: flags, two rcodes, then an uncompressed DNS or ASCII name.
			if n > 3 && o.Hostname == "" {
				value := p[i : i+n]
				if value[0]&4 != 0 {
					if name, end, ok := uncompressedName(value[3:]); ok && end == n-3 {
						o.Hostname = cleanName(name)
					}
				} else {
					o.Hostname = cleanName(string(value[3:]))
				}
			}
		case 60:
			if n <= 128 {
				o.VendorClass = string(p[i : i+n])
			}
		}
		i += n
	}
}

func decodeMDNSUDP(u []byte, o *model.Observation) {
	if o.TTL != 255 || len(u) < 8 || binary.BigEndian.Uint16(u[:2]) != 5353 || binary.BigEndian.Uint16(u[2:4]) != 5353 {
		return
	}
	n := int(binary.BigEndian.Uint16(u[4:6]))
	if n >= 20 && n <= len(u) {
		mdns(u[8:n], o)
	}
}

// DHCP option 81 prohibits DNS compression pointers.
func uncompressedName(b []byte) (string, int, bool) {
	for i := 0; i < len(b); {
		n := int(b[i])
		if n > 63 || i+1+n > len(b) {
			return "", 0, false
		}
		if n == 0 {
			return dnsName(b, 0)
		}
		i += n + 1
	}
	return "", 0, false
}
