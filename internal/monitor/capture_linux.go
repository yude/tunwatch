//go:build linux

package monitor

import (
	"context"
	"encoding/binary"
	"errors"
	"fmt"
	"net"
	"net/netip"
	"sort"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/yude/tunwatch/internal/config"
	"github.com/yude/tunwatch/internal/model"
	"golang.org/x/sys/unix"
)

type Capture struct {
	iface            net.Interface
	fd               int
	prefixes         []netip.Prefix
	targets          []probe
	Received         atomic.Uint64
	Dropped          atomic.Uint64
	KernelDropped    atomic.Uint64
	allowUnaddressed bool
	addressKey       string
}
type probe struct{ source, target netip.Addr }

func htons(v uint16) uint16 {
	var b [2]byte
	binary.BigEndian.PutUint16(b[:], v)
	return binary.NativeEndian.Uint16(b[:])
}

func OpenCapture(name string, c config.Config) (*Capture, error) {
	iface, err := net.InterfaceByName(name)
	if err != nil {
		return nil, err
	}
	if len(iface.HardwareAddr) != 6 || iface.Flags&net.FlagLoopback != 0 || iface.Flags&net.FlagUp == 0 {
		return nil, fmt.Errorf("%s must be an up Ethernet interface", name)
	}
	addrs, err := iface.Addrs()
	if err != nil {
		return nil, err
	}
	var local []netip.Prefix
	for _, a := range addrs {
		if p, e := netip.ParsePrefix(a.String()); e == nil {
			local = append(local, p)
		}
	}
	x := &Capture{iface: *iface, fd: -1, prefixes: c.Prefixes, allowUnaddressed: len(c.Prefixes) == 0, addressKey: addressKey(addrs)}
	if len(x.prefixes) == 0 {
		for _, p := range local {
			x.prefixes = append(x.prefixes, p.Masked())
		}
	}
	if len(x.prefixes) == 0 {
		return nil, fmt.Errorf("%s needs connected addresses or explicit networks", name)
	}
	if c.ActiveARP {
		x.targets, err = scanTargets(c.Prefixes, local)
		if err != nil {
			return nil, err
		}
		if time.Duration(len(x.targets))*time.Second/time.Duration(c.ProbeRate) > c.Scan {
			return nil, errors.New("scan_interval is too short for networks and probe_rate")
		}
	}
	fd, err := unix.Socket(unix.AF_PACKET, unix.SOCK_RAW|unix.SOCK_CLOEXEC|unix.SOCK_NONBLOCK, int(htons(unix.ETH_P_ALL)))
	if err != nil {
		return nil, fmt.Errorf("capture %s (requires CAP_NET_RAW): %w", name, err)
	}
	x.fd = fd
	if err = unix.Bind(fd, &unix.SockaddrLinklayer{Protocol: htons(unix.ETH_P_ALL), Ifindex: iface.Index}); err != nil {
		x.Close()
		return nil, err
	}
	// Kernel truncates general IP traffic to headers. DHCP/mDNS/VLAN use a bounded snaplen.
	filter := []unix.SockFilter{
		{Code: 0x28, K: 12},
		{Code: 0x15, Jt: 16, K: 0x0806}, // ARP -> 64
		{Code: 0x15, Jt: 9, K: 0x86dd},  // IPv6 branch
		{Code: 0x15, Jt: 15, K: 0x8100}, // VLAN -> 1152
		{Code: 0x15, Jt: 14, K: 0x88a8},
		{Code: 0x15, Jf: 14, K: 0x0800},     // other -> drop
		{Code: 0x30, K: 23},                 // IPv4 protocol
		{Code: 0x15, Jf: 8, K: 17},          // non-UDP -> 74
		{Code: 0xb1, K: 14},                 // X = IHL
		{Code: 0x48, K: 14},                 // UDP source port
		{Code: 0x15, Jt: 8, K: 68},          // DHCP
		{Code: 0x15, Jt: 7, Jf: 4, K: 5353}, // mDNS, otherwise -> 74
		{Code: 0x30, K: 20},                 // IPv6 next header
		{Code: 0x15, Jf: 3, K: 17},
		{Code: 0x28, K: 54}, // IPv6 UDP source port
		{Code: 0x15, Jt: 3, Jf: 1, K: 5353},
		{Code: 0x06, K: 74},
		{Code: 0x06, K: 80},
		{Code: 0x06, K: 64},
		{Code: 0x06, K: 1152},
		{Code: 0x06, K: 0},
	}
	if err = unix.SetsockoptSockFprog(fd, unix.SOL_SOCKET, unix.SO_ATTACH_FILTER, &unix.SockFprog{Len: uint16(len(filter)), Filter: &filter[0]}); err != nil {
		x.Close()
		return nil, err
	}
	if err = unix.SetsockoptInt(fd, unix.SOL_PACKET, unix.PACKET_AUXDATA, 1); err != nil {
		x.Close()
		return nil, err
	}
	if err = unix.SetsockoptPacketMreq(fd, unix.SOL_PACKET, unix.PACKET_ADD_MEMBERSHIP, &unix.PacketMreq{Ifindex: int32(iface.Index), Type: unix.PACKET_MR_ALLMULTI}); err != nil {
		x.Close()
		return nil, err
	}
	if err = unix.SetsockoptInt(fd, unix.SOL_SOCKET, unix.SO_LOCK_FILTER, 1); err != nil {
		x.Close()
		return nil, err
	}
	return x, nil
}
func (x *Capture) Close() {
	if x.fd >= 0 {
		unix.Close(x.fd)
		x.fd = -1
	}
}
func (x *Capture) InScope(o model.Observation) bool {
	if o.MAC == x.iface.HardwareAddr.String() {
		return false
	}
	if o.IP == "" {
		return x.allowUnaddressed && o.Source == "dhcp"
	}
	a, err := netip.ParseAddr(o.IP)
	if err != nil {
		return false
	}
	for _, p := range x.prefixes {
		if p.Contains(a) {
			return true
		}
	}
	return false
}
func (x *Capture) Run(ctx context.Context, c config.Config, out chan<- model.Observation) error {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	var wg sync.WaitGroup
	scanErr := make(chan error, 1)
	if c.ActiveARP {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if err := x.scan(ctx, c); err != nil {
				scanErr <- err
			}
		}()
	}
	defer func() { cancel(); wg.Wait() }()
	buf := make([]byte, 1152)
	control := make([]byte, 128)
	lastCheck := time.Time{}
	for ctx.Err() == nil {
		select {
		case err := <-scanErr:
			return err
		default:
		}
		if time.Since(lastCheck) > time.Second {
			iface, err := net.InterfaceByName(x.iface.Name)
			if err != nil || iface.Flags&net.FlagUp == 0 || iface.Index != x.iface.Index {
				return fmt.Errorf("monitor interface %s unavailable; stopping to avoid false offline events", x.iface.Name)
			}
			currentAddrs, addrErr := iface.Addrs()
			if addrErr != nil || addressKey(currentAddrs) != x.addressKey || iface.HardwareAddr.String() != x.iface.HardwareAddr.String() {
				return errors.New("interface addresses changed; restart to revalidate monitoring scope")
			}
			lastCheck = time.Now()
			stats, e := unix.GetsockoptTpacketStats(x.fd, unix.SOL_PACKET, unix.PACKET_STATISTICS)
			if e != nil {
				return e
			}
			x.KernelDropped.Add(uint64(stats.Drops))
		}
		fds := []unix.PollFd{{Fd: int32(x.fd), Events: unix.POLLIN}}
		_, err := unix.Poll(fds, 1000)
		if err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if fds[0].Revents&(unix.POLLERR|unix.POLLHUP|unix.POLLNVAL) != 0 {
			return errors.New("packet socket unavailable")
		}
		if fds[0].Revents&unix.POLLIN == 0 {
			continue
		}
		n, on, _, from, err := unix.Recvmsg(x.fd, buf, control, 0)
		if err == unix.EAGAIN || err == unix.EINTR {
			continue
		}
		if err != nil {
			return err
		}
		if ll, ok := from.(*unix.SockaddrLinklayer); ok && ll.Pkttype == unix.PACKET_OUTGOING {
			continue
		}
		o, ok := Decode(buf[:n], x.iface.Name, time.Now().UTC())
		if !ok || !x.InScope(o) {
			continue
		}
		if msgs, e := unix.ParseSocketControlMessage(control[:on]); e == nil {
			for _, m := range msgs {
				if m.Header.Level == unix.SOL_PACKET && m.Header.Type == unix.PACKET_AUXDATA && len(m.Data) >= 20 && binary.NativeEndian.Uint32(m.Data[:4])&unix.TP_STATUS_VLAN_VALID != 0 {
					tag := fmt.Sprint(binary.NativeEndian.Uint16(m.Data[16:18]) & 0xfff)
					if o.VLAN != "" {
						tag += "." + o.VLAN
					}
					o.VLAN = tag
				}
			}
		}
		x.Received.Add(1)
		select {
		case out <- o:
		default:
			x.Dropped.Add(1)
		}
	}
	return nil
}
func scanTargets(networks, local []netip.Prefix) ([]probe, error) {
	var out []probe
	seen := map[netip.Addr]bool{}
	for _, p := range networks {
		if !p.Addr().Is4() {
			continue
		}
		if p.Bits() < 20 {
			return nil, errors.New("active ARP networks must be /20 or smaller")
		}
		var source netip.Addr
		for _, l := range local {
			if l.Addr().Is4() && l.Bits() <= p.Bits() && l.Masked().Contains(p.Addr()) {
				source = l.Addr()
				break
			}
		}
		if !source.IsValid() {
			continue
		}
		for a := p.Addr(); a.IsValid() && p.Contains(a); a = a.Next() {
			if a == source || seen[a] {
				continue
			}
			if p.Bits() < 31 && (a == p.Addr() || !p.Contains(a.Next())) {
				continue
			}
			seen[a] = true
			out = append(out, probe{source, a})
			if len(out) > 4096 {
				return nil, errors.New("active ARP limit is 4096 targets per interface")
			}
		}
	}
	return out, nil
}
func (x *Capture) scan(ctx context.Context, c config.Config) error {
	if len(x.targets) == 0 {
		<-ctx.Done()
		return nil
	}
	ticker := time.NewTicker(time.Second / time.Duration(c.ProbeRate))
	defer ticker.Stop()
	for {
		start := time.Now()
		for _, p := range x.targets {
			select {
			case <-ctx.Done():
				return nil
			case <-ticker.C:
			}
			frame := arpRequest(x.iface.HardwareAddr, p.source, p.target)
			if err := unix.Sendto(x.fd, frame, 0, &unix.SockaddrLinklayer{Ifindex: x.iface.Index, Protocol: htons(unix.ETH_P_ARP), Halen: 6, Addr: [8]byte{255, 255, 255, 255, 255, 255}}); err != nil {
				return fmt.Errorf("ARP probe failed: %w", err)
			}
		}
		wait := time.NewTimer(max(time.Millisecond, c.Scan-time.Since(start)))
		select {
		case <-ctx.Done():
			wait.Stop()
			return nil
		case <-wait.C:
		}
	}
}
func arpRequest(mac net.HardwareAddr, src, dst netip.Addr) []byte {
	b := make([]byte, 42)
	copy(b[:6], []byte{255, 255, 255, 255, 255, 255})
	copy(b[6:12], mac)
	binary.BigEndian.PutUint16(b[12:14], 0x806)
	copy(b[14:22], []byte{0, 1, 8, 0, 6, 4, 0, 1})
	copy(b[22:28], mac)
	s, d := src.As4(), dst.As4()
	copy(b[28:32], s[:])
	copy(b[38:42], d[:])
	return b
}
func (x *Capture) Name() string     { return x.iface.Name }
func (x *Capture) TargetCount() int { return len(x.targets) }

func addressKey(addrs []net.Addr) string {
	names := make([]string, 0, len(addrs))
	for _, a := range addrs {
		names = append(names, a.String())
	}
	sort.Strings(names)
	return strings.Join(names, ",")
}
