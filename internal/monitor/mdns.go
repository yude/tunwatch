package monitor

import (
	"encoding/binary"
	"net/netip"
	"strings"

	"github.com/yude/tunwatch/internal/model"
)

// mdns considers only address records matching the packet's source address.
// Service lists, TXT records and names of other advertised hosts are discarded.
func mdns(p []byte, o *model.Observation) {
	if len(p) < 12 || binary.BigEndian.Uint16(p[2:4])&0x8000 == 0 {
		return
	}
	questions := int(binary.BigEndian.Uint16(p[4:6]))
	records := int(binary.BigEndian.Uint16(p[6:8])) + int(binary.BigEndian.Uint16(p[8:10])) + int(binary.BigEndian.Uint16(p[10:12]))
	if questions > 64 || records > 128 {
		return
	}
	pos := 12
	for i := 0; i < questions; i++ {
		_, next, ok := dnsName(p, pos)
		if !ok || next+4 > len(p) {
			return
		}
		pos = next + 4
	}
	for i := 0; i < records; i++ {
		name, next, ok := dnsName(p, pos)
		if !ok || next+10 > len(p) {
			return
		}
		typ := binary.BigEndian.Uint16(p[next : next+2])
		class := binary.BigEndian.Uint16(p[next+2:next+4]) & 0x7fff
		n := int(binary.BigEndian.Uint16(p[next+8 : next+10]))
		pos = next + 10
		if pos+n > len(p) {
			return
		}
		var addr netip.Addr
		if typ == 1 && n == 4 {
			addr = netip.AddrFrom4([4]byte(p[pos : pos+4]))
		}
		if typ == 28 && n == 16 {
			addr = netip.AddrFrom16([16]byte(p[pos : pos+16]))
		}
		if class == 1 && addr.IsValid() && addr.String() == o.IP && strings.HasSuffix(strings.ToLower(name), ".local") {
			if name = cleanName(name); name != "" {
				o.Hostname = name
				o.Source = "mdns"
				return
			}
		}
		pos += n
	}
}
func dnsName(p []byte, offset int) (string, int, bool) {
	var labels []string
	next := -1
	size := 0
	for hops := 0; hops < 64; hops++ {
		if offset < 0 || offset >= len(p) {
			return "", 0, false
		}
		n := int(p[offset])
		if n&0xc0 == 0xc0 {
			if offset+1 >= len(p) {
				return "", 0, false
			}
			if next < 0 {
				next = offset + 2
			}
			offset = (n&0x3f)<<8 | int(p[offset+1])
			continue
		}
		if n&0xc0 != 0 {
			return "", 0, false
		}
		offset++
		if n == 0 {
			if next < 0 {
				next = offset
			}
			return strings.Join(labels, "."), next, true
		}
		if n > 63 || offset+n > len(p) || size+n+1 > 254 {
			return "", 0, false
		}
		labels = append(labels, string(p[offset:offset+n]))
		size += n + 1
		offset += n
	}
	return "", 0, false
}
