package monitor

import (
	"bufio"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"

	"github.com/yude/tunwatch/internal/model"
)

type Hosts struct {
	Names map[string]string
	File  string
}

// LoadHosts reads a local snapshot. No client addresses are sent to a provider.
func LoadHosts(path string) (Hosts, error) {
	h := Hosts{Names: map[string]string{}, File: filepath.Base(path)}
	if path == "" {
		return h, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return h, err
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil {
		return h, err
	}
	if info.Size() > 4<<20 {
		return h, fmt.Errorf("hosts file exceeds 4 MiB")
	}
	return parseHosts(io.LimitReader(f, (4<<20)+1), h)
}
func parseHosts(r io.Reader, h Hosts) (Hosts, error) {
	scanner := bufio.NewScanner(r)
	line := 0
	for scanner.Scan() {
		line++
		text, _, _ := strings.Cut(scanner.Text(), "#")
		fields := strings.Fields(text)
		if len(fields) == 0 {
			continue
		}
		if len(fields) < 2 {
			return h, fmt.Errorf("hosts line %d: missing hostname", line)
		}
		ip, err := netip.ParseAddr(fields[0])
		if err != nil || ip.Zone() != "" {
			return h, fmt.Errorf("hosts line %d: invalid IP", line)
		}
		name := cleanName(fields[1])
		if name == "" {
			return h, fmt.Errorf("hosts line %d: invalid hostname", line)
		}
		key := ip.Unmap().String()
		if old, ok := h.Names[key]; ok && !strings.EqualFold(old, name) {
			return h, fmt.Errorf("hosts line %d: conflicting names for IP", line)
		}
		h.Names[key] = name
	}
	return h, scanner.Err()
}

// A file-based display name is separate from names observed on the wire.
// A unique gateway name wins when a device has multiple IPv4 addresses.
// Other conflicting assignments across recorded IPs are left unresolved.
func (h Hosts) Lookup(ips []string) model.Hint {
	result := model.Hint{}
	gateway := model.Hint{}
	conflicting, conflictingGateways := false, false
	ipv4 := make(map[string]struct{})
	for _, raw := range ips {
		ip, err := netip.ParseAddr(raw)
		if err != nil {
			continue
		}
		ip = ip.Unmap()
		if ip.Is4() {
			ipv4[ip.String()] = struct{}{}
		}
		name := h.Names[ip.String()]
		if name == "" {
			continue
		}
		hint := model.Hint{Value: name, Source: "hosts_file", Confidence: "configured", Detail: h.File + " · " + ip.String()}
		if result.Value != "" && !strings.EqualFold(result.Value, name) {
			conflicting = true
		}
		if result.Value == "" {
			result = hint
		}
		if ip.Is4() && strings.Contains(strings.ToLower(name), "gw") {
			if gateway.Value != "" && !strings.EqualFold(gateway.Value, name) {
				conflictingGateways = true
			}
			if gateway.Value == "" {
				gateway = hint
			}
		}
	}
	if len(ipv4) >= 2 && gateway.Value != "" {
		if conflictingGateways {
			return model.Hint{}
		}
		return gateway
	}
	if conflicting {
		return model.Hint{}
	}
	return result
}
func (e *Engine) SetHosts(h Hosts) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.hosts = h
	for id, d := range e.devices {
		d.HostsName = h.Lookup(d.IPs)
		e.devices[id] = d
	}
}
