package monitor

import (
	"slices"
	"time"

	"github.com/yude/tunwatch/internal/model"
)

func vipKey(iface, vlan, ip string) string { return iface + "\x00" + vlan + "\x00" + ip }

func addVIP(d *model.Device, vip model.VIP) bool {
	for i, old := range d.VIPs {
		if old.IP != vip.IP {
			continue
		}
		if old.Source == "vrrp_mac" && vip.Source == "vrrp_advertisement" || old.Source == vip.Source && old.VRID != vip.VRID {
			d.VIPs[i] = vip
			return true
		}
		return false
	}
	d.VIPs = append(d.VIPs, vip)
	return true
}

func isVIPOnly(d model.Device) bool {
	if isIPv4VRRPMACString(d.MAC) {
		return true
	}
	if len(d.IPs) == 0 {
		return false
	}
	for _, ip := range d.IPs {
		if !slices.ContainsFunc(d.VIPs, func(vip model.VIP) bool { return vip.IP == ip }) {
			return false
		}
	}
	return true
}

func (e *Engine) enrichVIPs(d *model.Device) bool {
	changed := false
	for _, ip := range d.IPs {
		if vip, ok := e.vips[vipKey(d.Interface, d.VLAN, ip)]; ok && addVIP(d, vip.VIP) {
			changed = true
		}
	}
	if only := isVIPOnly(*d); d.VIPOnly != only {
		d.VIPOnly = only
		changed = true
	}
	return changed
}

// Called while the engine is locked, or before it is exposed by NewEngine.
func (e *Engine) expireVIPs(now time.Time) {
	for key, seen := range e.vipSeen {
		if now.Sub(seen) < max(time.Hour, e.c.Offline) {
			continue
		}
		delete(e.vipSeen, key)
		delete(e.vips, key)
		for id, d := range e.devices {
			if !slices.ContainsFunc(d.VIPs, func(vip model.VIP) bool { return vipKey(d.Interface, d.VLAN, vip.IP) == key }) {
				continue
			}
			d = clone(d)
			d.VIPs = slices.DeleteFunc(d.VIPs, func(vip model.VIP) bool { return vipKey(d.Interface, d.VLAN, vip.IP) == key })
			d.VIPOnly = isVIPOnly(d)
			e.devices[id] = d
		}
	}
}

func (e *Engine) VIPs() []model.ScopedVIP {
	e.mu.RLock()
	defer e.mu.RUnlock()
	out := make([]model.ScopedVIP, 0, len(e.vips))
	for _, vip := range e.vips {
		out = append(out, vip)
	}
	slices.SortFunc(out, func(a, b model.ScopedVIP) int {
		if a.Interface != b.Interface {
			return compareString(a.Interface, b.Interface)
		}
		if a.VLAN != b.VLAN {
			return compareString(a.VLAN, b.VLAN)
		}
		return compareString(a.IP, b.IP)
	})
	return out
}

func compareString(a, b string) int {
	if a < b {
		return -1
	}
	if a > b {
		return 1
	}
	return 0
}
