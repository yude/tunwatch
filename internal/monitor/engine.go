package monitor

import (
	"fmt"
	"net"
	"slices"
	"sort"
	"strings"
	"sync"
	"time"

	"github.com/yude/tunwatch/internal/config"
	"github.com/yude/tunwatch/internal/model"
)

type Journal interface {
	Append(model.Event) error
	Save([]model.Device, time.Time) error
}
type Engine struct {
	mu       sync.RWMutex
	c        config.Config
	devices  map[string]model.Device
	observed map[string]bool
	updates  map[string]time.Time
	journal  Journal
	hosts    Hosts
	vendors  Vendors
	started  time.Time
	sequence int64
	Notify   func(model.Event)
	Rejected uint64
}

func NewEngine(c config.Config, j Journal, v Vendors, restored map[string]model.Device, now time.Time) *Engine {
	if restored == nil {
		restored = map[string]model.Device{}
	}
	for id, d := range restored {
		if d.LastSeen.Before(now.Add(-time.Duration(c.RetentionDays) * 24 * time.Hour)) {
			delete(restored, id)
			continue
		}
		d.Status = "unknown"
		if vendor := v.Lookup(d.MAC); !d.LocalMAC && vendor != "" {
			d.Vendor = vendor
		}
		restored[id] = d
	}
	return &Engine{c: c, journal: j, vendors: v, devices: restored, observed: map[string]bool{}, updates: map[string]time.Time{}, started: now, sequence: now.UnixNano()}
}
func clone(d model.Device) model.Device {
	d.IPs = slices.Clone(d.IPs)
	d.Sources = slices.Clone(d.Sources)
	return d
}
func (e *Engine) Observe(o model.Observation) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	id := o.ID()
	d, exists := e.devices[id]
	if !exists && len(e.devices) >= e.c.MaxDevices {
		e.Rejected++
		return nil
	}
	d = clone(d)
	old := d
	if !exists {
		mac, err := net.ParseMAC(o.MAC)
		if err != nil || !validMAC(mac) {
			return nil
		}
		d = model.Device{ID: id, Interface: o.Interface, VLAN: o.VLAN, MAC: o.MAC, FirstSeen: o.At, IPs: []string{}, Sources: []string{}, LocalMAC: mac[0]&2 != 0}
		if !d.LocalMAC {
			d.Vendor = e.vendors.Lookup(o.MAC)
		}
	}
	if o.At.Before(d.LastSeen) {
		return nil
	}
	d.LastSeen = o.At
	d.Status = "online"
	changed := false
	if o.IP != "" && !slices.Contains(d.IPs, o.IP) {
		if len(d.IPs) >= 32 {
			d.IPs = d.IPs[1:]
		}
		d.IPs = append(d.IPs, o.IP)
		changed = true
	}
	if o.Source != "" && !slices.Contains(d.Sources, o.Source) {
		d.Sources = append(d.Sources, o.Source)
		changed = true
	}
	if name := cleanName(o.Hostname); name != "" && (d.Hostname.Value == "" || o.Source == "dhcp" || d.Hostname.Source == "reverse_dns") {
		h := model.Hint{Value: name, Source: o.Source, Confidence: "medium"}
		if h != d.Hostname {
			d.Hostname = h
			changed = true
		}
	}
	if h := osHint(o); h.Value != "" && (d.OS.Value == "" || d.OS.Confidence == "low" && h.Confidence == "medium") {
		if h != d.OS {
			d.OS = h
			changed = true
		}
	}
	if h := e.hosts.Lookup(d.IPs); h != d.HostsName {
		d.HostsName = h
		changed = true
	}
	typ, reason := "", ""
	if !exists || old.Status != "online" {
		typ = "online"
		reason = "source traffic observed"
	} else if changed && o.At.Sub(e.updates[id]) >= 30*time.Second {
		typ = "updated"
		reason = "device metadata changed"
	}
	if typ != "" {
		if err := e.event(d, typ, reason, o.At, !exists || old.Status == "offline"); err != nil {
			return err
		}
		e.updates[id] = o.At
	}
	e.devices[id] = d
	e.observed[id] = true
	return nil
}
func (e *Engine) event(d model.Device, typ, reason string, now time.Time, notify bool) error {
	e.sequence = max(e.sequence+1, now.UnixNano())
	event := model.Event{ID: fmt.Sprintf("%020d", e.sequence), Time: now, Type: typ, Reason: reason, Device: clone(d)}
	if err := e.journal.Append(event); err != nil {
		return err
	}
	if notify && e.Notify != nil && (typ == "online" || typ == "offline") && now.Sub(e.started) >= e.c.Grace {
		e.Notify(event)
	}
	return nil
}
func (e *Engine) Tick(now time.Time) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	for id, d := range e.devices {
		if d.LastSeen.Before(now.Add(-time.Duration(e.c.RetentionDays) * 24 * time.Hour)) {
			delete(e.devices, id)
			delete(e.observed, id)
			delete(e.updates, id)
			continue
		}
		if !e.observed[id] {
			continue
		}
		status := d.Status
		age := now.Sub(d.LastSeen)
		if age >= e.c.Offline {
			status = "offline"
		} else if age >= e.c.Stale {
			status = "stale"
		}
		if status == d.Status {
			continue
		}
		d.Status = status
		reason := "no recent source traffic; presence unconfirmed"
		if status == "offline" {
			reason = "silence timeout; offline is inferred, not confirmed"
		}
		if err := e.event(d, status, reason, now, status == "offline"); err != nil {
			return err
		}
		e.devices[id] = d
	}
	return nil
}
func (e *Engine) Devices() []model.Device {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.devicesLocked()
}
func (e *Engine) devicesLocked() []model.Device {
	out := make([]model.Device, 0, len(e.devices))
	for _, d := range e.devices {
		out = append(out, clone(d))
	}
	sort.Slice(out, func(i, j int) bool {
		if out[i].Status == out[j].Status {
			return out[i].LastSeen.After(out[j].LastSeen)
		}
		rank := map[string]int{"online": 0, "stale": 1, "unknown": 2, "offline": 3}
		return rank[out[i].Status] < rank[out[j].Status]
	})
	return out
}
func (e *Engine) Save(now time.Time) error {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return e.journal.Save(e.devicesLocked(), now)
}
func (e *Engine) Stats() (int, uint64) {
	e.mu.RLock()
	defer e.mu.RUnlock()
	return len(e.devices), e.Rejected
}
func (e *Engine) SetHostname(id, name string) error {
	name = cleanName(strings.TrimSuffix(name, "."))
	if name == "" {
		return nil
	}
	e.mu.Lock()
	defer e.mu.Unlock()
	d, ok := e.devices[id]
	if !ok || d.Hostname.Value != "" {
		return nil
	}
	d.Hostname = model.Hint{Value: name, Source: "reverse_dns", Confidence: "low"}
	if err := e.event(d, "updated", "reverse DNS name observed", time.Now().UTC(), false); err != nil {
		return err
	}
	e.devices[id] = d
	return nil
}
