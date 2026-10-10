package monitor

import (
	"bufio"
	"fmt"
	"github.com/yude/tunwatch/internal/model"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strings"
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
// Conflicting assignments across recorded IPs are left unresolved.
func (h Hosts) Lookup(ips []string) model.Hint {
	result := model.Hint{}
	for _, raw := range ips {
		ip, err := netip.ParseAddr(raw)
		if err != nil {
			continue
		}
		name := h.Names[ip.Unmap().String()]
		if name == "" {
			continue
		}
		if result.Value != "" && !strings.EqualFold(result.Value, name) {
			return model.Hint{}
		}
		if result.Value == "" {
			result = model.Hint{Value: name, Source: "hosts_file", Confidence: "configured", Detail: h.File + " · " + ip.Unmap().String()}
		}
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
