package monitor

import (
	"bytes"
	"encoding/binary"
	"net"
	"net/netip"

	"github.com/yude/tunwatch/internal/model"
)

var ipv4VRRPMACPrefix = []byte{0, 0, 0x5e, 0, 1}

func isIPv4VRRPMAC(mac []byte) bool {
	return len(mac) == 6 && mac[5] != 0 && bytes.Equal(mac[:5], ipv4VRRPMACPrefix)
}

func isIPv4VRRPMACString(value string) bool {
	mac, err := net.ParseMAC(value)
	return err == nil && isIPv4VRRPMAC(mac)
}

// RFC 3768 (v2) and RFC 9568 (v3) carry the VIPs in the advertisement.
// Some existing v3 senders still use the older RFC 5798 pseudo-header checksum.
func decodeVRRPIPv4(payload []byte, src, dst []byte, ttl int) ([]model.VIP, bool) {
	if ttl != 255 || len(payload) < 12 || len(src) != 4 || len(dst) != 4 || payload[0]&15 != 1 || payload[1] == 0 || payload[3] == 0 {
		return nil, false
	}
	version, count := payload[0]>>4, int(payload[3])
	if version != 2 && version != 3 || count > (len(payload)-8)/4 {
		return nil, false
	}
	length := 8 + 4*count
	if version == 2 {
		if len(payload) != length+8 || !checksumOK(payload) {
			return nil, false
		}
	} else {
		if len(payload) != length || !checksumOK(payload) && !checksumOK(vrrpPseudoHeader(src, dst, len(payload)), payload) {
			return nil, false
		}
	}
	vips := make([]model.VIP, 0, count)
	for i := 0; i < count; i++ {
		ip := netip.AddrFrom4([4]byte(payload[8+i*4 : 12+i*4]))
		if !usableIP(ip) {
			return nil, false
		}
		vips = append(vips, model.VIP{IP: ip.String(), Source: "vrrp_advertisement", VRID: int(payload[1])})
	}
	return vips, true
}

func vrrpPseudoHeader(src, dst []byte, length int) []byte {
	h := make([]byte, 12)
	copy(h[:4], src)
	copy(h[4:8], dst)
	h[9] = 112
	binary.BigEndian.PutUint16(h[10:], uint16(length))
	return h
}

func checksumOK(parts ...[]byte) bool {
	var sum uint32
	for _, part := range parts {
		for i := 0; i+1 < len(part); i += 2 {
			sum += uint32(binary.BigEndian.Uint16(part[i:]))
		}
		if len(part)&1 != 0 {
			sum += uint32(part[len(part)-1]) << 8
		}
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return sum == 0xffff
}
