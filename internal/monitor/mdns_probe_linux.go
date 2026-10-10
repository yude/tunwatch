package monitor

import (
	"net/netip"
	"time"

	"github.com/yude/tunwatch/internal/model"
	"golang.org/x/sys/unix"
)

type nameProbe struct {
	source, target netip.Addr
	seen, next     time.Time
	named          bool
}
type nameProbes map[string]nameProbe

func (p nameProbes) observe(o model.Observation, local []netip.Prefix, limit int) {
	// Tagged trunks need per-VLAN addresses; never send untagged probes onto them.
	if o.VLAN != "" {
		return
	}
	target, err := netip.ParseAddr(o.IP)
	if err != nil || !target.Is4() {
		return
	}
	key := o.MAC + "/" + o.IP
	v, exists := p[key]
	if !exists {
		if len(p) >= limit {
			return
		}
		for _, prefix := range local {
			if prefix.Addr().Is4() && prefix.Contains(target) {
				v.source = prefix.Addr()
				break
			}
		}
		if !v.source.IsValid() {
			return
		}
		v.target, v.next = target, o.At
	}
	v.seen = o.At
	v.named = v.named || o.Hostname != ""
	p[key] = v
}
func (p nameProbes) next(now time.Time) (nameProbe, bool) {
	var chosen string
	var oldest time.Time
	for key, v := range p {
		if now.Sub(v.seen) > 10*time.Minute {
			delete(p, key)
			continue
		}
		if !v.named && !v.next.After(now) && (chosen == "" || v.next.Before(oldest)) {
			chosen, oldest = key, v.next
		}
	}
	if chosen == "" {
		return nameProbe{}, false
	}
	v := p[chosen]
	v.next = now.Add(5 * time.Minute)
	p[chosen] = v
	return v, true
}
func (x *Capture) queryName(v nameProbe) error {
	b := mdnsRequest(x.iface.HardwareAddr, v.source, v.target)
	return unix.Sendto(x.fd, b, 0, &unix.SockaddrLinklayer{Ifindex: x.iface.Index, Protocol: htons(unix.ETH_P_IP), Halen: 6, Addr: [8]byte{1, 0, 0x5e, 0, 0, 0xfb}})
}
