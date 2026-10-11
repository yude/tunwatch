package main

import (
	"encoding/binary"
	"net"
	"time"

	"golang.org/x/sys/unix"
)

func htons(v uint16) uint16 { return v<<8 | v>>8 }

func checksum(p []byte) uint16 {
	var sum uint32
	for i := 0; i < len(p); i += 2 {
		sum += uint32(binary.BigEndian.Uint16(p[i:]))
	}
	for sum>>16 != 0 {
		sum = (sum & 0xffff) + (sum >> 16)
	}
	return ^uint16(sum)
}

func main() {
	iface, err := net.InterfaceByName("eth0")
	if err != nil {
		panic(err)
	}
	frame := make([]byte, 14+20+12)
	copy(frame[:6], []byte{1, 0, 0x5e, 0, 0, 18})
	copy(frame[6:12], iface.HardwareAddr)
	binary.BigEndian.PutUint16(frame[12:14], 0x0800)
	ip := frame[14:34]
	ip[0], ip[8], ip[9] = 0x45, 255, 112
	binary.BigEndian.PutUint16(ip[2:4], uint16(len(frame)-14))
	copy(ip[12:16], []byte{192, 0, 2, 3})
	copy(ip[16:20], []byte{224, 0, 0, 18})
	binary.BigEndian.PutUint16(ip[10:12], checksum(ip))
	vrrp := frame[34:]
	copy(vrrp, []byte{0x31, 42, 100, 1, 0, 100, 0, 0, 192, 0, 2, 1})
	binary.BigEndian.PutUint16(vrrp[6:8], checksum(vrrp))
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW, int(htons(unix.ETH_P_IP)))
	if err != nil {
		panic(err)
	}
	defer unix.Close(fd)
	addr := &unix.SockaddrLinklayer{Ifindex: iface.Index, Protocol: htons(unix.ETH_P_IP), Halen: 6, Addr: [8]byte{1, 0, 0x5e, 0, 0, 18}}
	for i := 0; i < 3; i++ {
		if err := unix.Sendto(fd, frame, 0, addr); err != nil {
			panic(err)
		}
		time.Sleep(300 * time.Millisecond)
	}
}
