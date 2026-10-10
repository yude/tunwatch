// Test fixture: only answer the isolated integration client's reverse query.
package main

import (
	"bytes"
	"encoding/binary"
	"log"
	"net"
	"syscall"
)

func main() {
	iface, err := net.InterfaceByName("eth0")
	if err != nil {
		log.Fatal(err)
	}
	group := &net.UDPAddr{IP: net.IPv4(224, 0, 0, 251), Port: 5353}
	conn, err := net.ListenMulticastUDP("udp4", iface, group)
	if err != nil {
		log.Fatal(err)
	}
	defer conn.Close()
	raw, err := conn.SyscallConn()
	if err != nil {
		log.Fatal(err)
	}
	var sockErr error
	if err = raw.Control(func(fd uintptr) {
		sockErr = syscall.SetsockoptInt(int(fd), syscall.IPPROTO_IP, syscall.IP_TTL, 255)
	}); err != nil {
		log.Fatal(err)
	}
	if sockErr != nil {
		log.Fatal(sockErr)
	}
	name := []byte{1, '3', 1, '2', 1, '0', 3, '1', '9', '2', 7, 'i', 'n', '-', 'a', 'd', 'd', 'r', 4, 'a', 'r', 'p', 'a', 0}
	host := append([]byte{18}, []byte("integration-client")...)
	host = append(host, 5, 'l', 'o', 'c', 'a', 'l', 0)
	buf := make([]byte, 1500)
	for {
		n, sender, err := conn.ReadFromUDP(buf)
		if err != nil {
			log.Fatal(err)
		}
		if n < 12+len(name)+4 || buf[2]&0x80 != 0 || !bytes.Equal(buf[12:12+len(name)], name) {
			continue
		}
		response := make([]byte, 12)
		response[2] = 0x84
		response[7] = 1
		response = append(response, name...)
		response = append(response, 0, 12, 0, 1, 0, 0, 0, 120, 0, 0)
		binary.BigEndian.PutUint16(response[len(response)-2:], uint16(len(host)))
		response = append(response, host...)
		if _, err := conn.WriteToUDP(response, sender); err != nil {
			log.Fatal(err)
		}
	}
}
