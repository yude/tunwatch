package config

import (
	"os"
	"path/filepath"
	"testing"
)

func TestStrictConfigAndSecrets(t *testing.T) {
	t.Setenv("TUNWATCH_API_TOKEN", "0123456789abcdef0123456789abcdef")
	path := filepath.Join(t.TempDir(), "config.json")
	for _, tc := range []struct {
		body string
		ok   bool
	}{{`{"interfaces":["eth0"]}`, true}, {`{"interfaces":["eth0"],"typo":1}`, false}, {`{"interfaces":["eth0"],"active_arp":true}`, false}, {`{"interfaces":["eth0"],"offline_after":"1m"}`, false}, {`{"interfaces":["eth0"],"networks":["192.0.2.1/24"]}`, false}, {`{"demo":true}`, true}, {`{"interfaces":[]}`, false}, {`{"demo":true} {}`, false}} {
		os.WriteFile(path, []byte(tc.body), 0600)
		_, err := Load(path)
		if (err == nil) != tc.ok {
			t.Errorf("%s: %v", tc.body, err)
		}
	}
	t.Setenv("TUNWATCH_API_TOKEN", "short")
	os.WriteFile(path, []byte(`{"demo":true}`), 0600)
	if _, err := Load(path); err == nil {
		t.Fatal("short token accepted")
	}
}
