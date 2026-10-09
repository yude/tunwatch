package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net"
	"net/netip"
	"os"
	"strings"
	"time"
)

type Config struct {
	Listen        string         `json:"listen"`
	Interfaces    []string       `json:"interfaces"`
	Networks      []string       `json:"networks"`
	DataDir       string         `json:"data_dir"`
	StaleAfter    string         `json:"stale_after"`
	OfflineAfter  string         `json:"offline_after"`
	StartupGrace  string         `json:"startup_grace"`
	RetentionDays int            `json:"retention_days"`
	HistoryMaxMB  int            `json:"history_max_mb"`
	MaxDevices    int            `json:"max_devices"`
	ActiveARP     bool           `json:"active_arp"`
	ScanInterval  string         `json:"scan_interval"`
	ProbeRate     int            `json:"probe_rate"`
	ReverseDNS    bool           `json:"reverse_dns"`
	VendorFile    string         `json:"vendor_file"`
	Demo          bool           `json:"demo"`
	Stale         time.Duration  `json:"-"`
	Offline       time.Duration  `json:"-"`
	Grace         time.Duration  `json:"-"`
	Scan          time.Duration  `json:"-"`
	Prefixes      []netip.Prefix `json:"-"`
	Token         string         `json:"-"`
	Webhook       string         `json:"-"`
}

func Load(path string) (Config, error) {
	c := Config{Listen: "127.0.0.1:8080", DataDir: "./data", StaleAfter: "5m", OfflineAfter: "30m", StartupGrace: "30s", RetentionDays: 30, HistoryMaxMB: 128, MaxDevices: 4096, ScanInterval: "2m", ProbeRate: 10}
	f, err := os.Open(path)
	if err != nil {
		return c, err
	}
	defer f.Close()
	d := json.NewDecoder(io.LimitReader(f, 1<<20))
	d.DisallowUnknownFields()
	if err = d.Decode(&c); err != nil {
		return c, err
	}
	var extra any
	if d.Decode(&extra) != io.EOF {
		return c, errors.New("config must contain one JSON object")
	}
	if err = c.Validate(); err != nil {
		return c, err
	}
	c.Token = strings.TrimSpace(os.Getenv("TUNWATCH_API_TOKEN"))
	c.Webhook = strings.TrimSpace(os.Getenv("TUNWATCH_DISCORD_WEBHOOK"))
	if len(c.Token) < 24 {
		return c, errors.New("TUNWATCH_API_TOKEN must contain at least 24 characters")
	}
	return c, nil
}

func (c *Config) Validate() error {
	var err error
	for _, p := range []struct {
		s   string
		dst *time.Duration
	}{{c.StaleAfter, &c.Stale}, {c.OfflineAfter, &c.Offline}, {c.StartupGrace, &c.Grace}, {c.ScanInterval, &c.Scan}} {
		*p.dst, err = time.ParseDuration(p.s)
		if err != nil || *p.dst <= 0 {
			return fmt.Errorf("invalid positive duration %q", p.s)
		}
	}
	if c.Stale < 10*time.Second || c.Offline <= c.Stale || c.Scan < 10*time.Second {
		return errors.New("require stale_after >= 10s, offline_after > stale_after, scan_interval >= 10s")
	}
	if c.RetentionDays < 1 || c.RetentionDays > 3650 || c.HistoryMaxMB < 1 || c.HistoryMaxMB > 10240 || c.MaxDevices < 1 || c.MaxDevices > 100000 || c.ProbeRate < 1 || c.ProbeRate > 100 {
		return errors.New("retention, capacity or probe_rate outside supported bounds")
	}
	if _, _, err = net.SplitHostPort(c.Listen); err != nil {
		return fmt.Errorf("listen: %w", err)
	}
	if c.DataDir == "" {
		return errors.New("data_dir is required")
	}
	if !c.Demo && len(c.Interfaces) == 0 {
		return errors.New("explicit interfaces are required")
	}
	seen := map[string]bool{}
	for _, s := range c.Interfaces {
		if s == "" || seen[s] {
			return errors.New("interfaces must be nonempty and unique")
		}
		seen[s] = true
	}
	c.Prefixes = nil
	for _, s := range c.Networks {
		p, e := netip.ParsePrefix(s)
		if e != nil || p != p.Masked() {
			return fmt.Errorf("invalid canonical network %q", s)
		}
		c.Prefixes = append(c.Prefixes, p)
	}
	if c.ActiveARP && len(c.Prefixes) == 0 {
		return errors.New("active_arp requires explicit networks")
	}
	return nil
}
