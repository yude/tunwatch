package config

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
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

func TestNodeProfiles(t *testing.T) {
	t.Setenv("TUNWATCH_API_TOKEN", "0123456789abcdef0123456789abcdef")
	base := `{"interfaces":["wrong-fallback"],"networks":["192.0.2.0/24"],"node_profiles":{"sensor-a":{"interfaces":["eno1.30"]},"sensor-b":{"interfaces":["enp3s0.30"],"networks":["198.51.100.0/24"]},"sensor-c":{"interfaces":["bond0.30"],"networks":[]}}}`
	for _, tc := range []struct {
		name, body           string
		interfaces, networks []string
		wantError            string
	}{
		{"sensor-a", base, []string{"eno1.30"}, []string{"192.0.2.0/24"}, ""},
		{"sensor-b", base, []string{"enp3s0.30"}, []string{"198.51.100.0/24"}, ""},
		{"sensor-c", base, []string{"bond0.30"}, []string{}, ""},
		{"missing", base, nil, nil, "refusing to fall back"},
		{"", base, nil, nil, "TUNWATCH_NODE_NAME is required"},
		{"sensor-a", `{"node_profiles":{}}`, nil, nil, "must not be empty"},
		{"sensor-a", `{"node_profiles":{"sensor-a":{"interfaces":[]}}}`, nil, nil, "explicit interfaces"},
		{"sensor-a", `{"node_profiles":{"sensor-a":{"interfaces":["eno1.30","eno1.30"]}}}`, nil, nil, "unique"},
		{"sensor-a", `{"node_profiles":{"sensor-a":{"interfaces":["eno1.30"]},"standby":{"interfaces":["bond0.30"],"networks":["invalid"]}}}`, nil, nil, "standby"},
		{"sensor-a", `{"node_profiles":{"sensor-a":{"interfaces":["eno1.30"],"typo":1}}}`, nil, nil, "unknown field"},
		{"sensor-a", `{"active_arp":true,"networks":["192.0.2.0/24"],"node_profiles":{"sensor-a":{"interfaces":["eno1.30"],"networks":[]}}}`, nil, nil, "active_arp requires"},
		{"sensor-a", `{"interfaces":["eth0"],"networks":[]}`, []string{"eth0"}, []string{}, ""},
	} {
		t.Run(tc.name+tc.wantError, func(t *testing.T) {
			t.Setenv("TUNWATCH_NODE_NAME", tc.name)
			path := filepath.Join(t.TempDir(), "config.json")
			if err := os.WriteFile(path, []byte(tc.body), 0600); err != nil {
				t.Fatal(err)
			}
			c, err := Load(path)
			if tc.wantError != "" {
				if err == nil || !strings.Contains(err.Error(), tc.wantError) {
					t.Fatalf("wanted %q, got %v", tc.wantError, err)
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			if c.NodeName != tc.name || !reflect.DeepEqual(c.Interfaces, tc.interfaces) || !reflect.DeepEqual(c.Networks, tc.networks) || c.NodeProfiles != nil {
				t.Fatalf("incorrect resolved scope: %+v", c)
			}
			if len(c.Prefixes) != len(tc.networks) {
				t.Fatal("resolved networks not validated")
			}
		})
	}
}
