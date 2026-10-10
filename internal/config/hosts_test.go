package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestGitHubHostsURL(t *testing.T) {
	for _, raw := range []string{"https://github.com/org/repo/blob/main/path/file.hosts", "https://raw.githubusercontent.com/org/repo/main/path/file.hosts"} {
		got, err := HostsURL(raw)
		if err != nil || got != "https://raw.githubusercontent.com/org/repo/main/path/file.hosts" {
			t.Fatalf("%s: %s %v", raw, got, err)
		}
	}
	for _, raw := range []string{"http://github.com/a/b/blob/main/f", "https://example.com/a/b/main/f", "https://token@github.com/a/b/blob/main/f", "https://github.com/a/b/blob/main/f?token=x", "https://github.com/a/b/blob/main/f#x", "https://github.com/a/b/tree/main/f", "https://raw.githubusercontent.com/a/b/../f"} {
		if _, err := HostsURL(raw); err == nil {
			t.Fatalf("accepted %s", raw)
		}
	}
}

func TestHostsURLConfig(t *testing.T) {
	t.Setenv("TUNWATCH_API_TOKEN", "0123456789abcdef0123456789abcdef")
	path := filepath.Join(t.TempDir(), "config.json")
	for _, tc := range []struct {
		input string
		good  bool
	}{
		{`{"demo":true,"hosts_url":"https://github.com/y2e-org/playbooks/blob/master/svc/coredns/config/tun.y2e.org.hosts"}`, true},
		{`{"demo":true,"hosts_url":"https://github.com/a/b/blob/main/f","hosts_refresh":"30s"}`, false},
		{`{"demo":true,"hosts_url":"https://github.com/a/b/blob/main/f","hosts_file":"/tmp/hosts"}`, false},
	} {
		os.WriteFile(path, []byte(tc.input), 0600)
		c, err := Load(path)
		if (err == nil) != tc.good {
			t.Fatalf("%s: %v", tc.input, err)
		}
		if tc.good && c.HostsRefreshDuration.Minutes() != 5 {
			t.Fatalf("unexpected interval: %s", c.HostsRefreshDuration)
		}
	}
}
