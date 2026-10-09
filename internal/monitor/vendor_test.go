package monitor

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/yude/tunwatch/internal/model"
)

func TestVendorLongestPrefixAndRestartEnrichment(t *testing.T) {
	path := filepath.Join(t.TempDir(), "vendors.csv")
	os.WriteFile(path, []byte("Registry,Assignment,Organization Name,Organization Address\nMA-L,001122,Example L,\nMA-M,0011223,Example M,\nMA-S,001122334,Example S,\n"), 0600)
	v, err := LoadVendors(path)
	if err != nil {
		t.Fatal(err)
	}
	if v.Lookup("00:11:22:33:44:55") != "Example S" || v.Lookup("00:11:22:35:44:55") != "Example M" || v.Lookup("00:11:22:45:44:55") != "Example L" {
		t.Fatal("wrong longest prefix")
	}
	c, j, e, now := testEngine()
	e.Observe(observation(now))
	d := e.Devices()[0]
	e = NewEngine(c, j, v, map[string]model.Device{d.ID: d}, now.Add(time.Second))
	if e.Devices()[0].Vendor != "Example S" {
		t.Fatal("restored device did not get newly configured vendor data")
	}
	o := observation(now.Add(2 * time.Second))
	o.MAC = "02:11:22:33:44:55"
	v["021122"] = "Must not be attributed"
	e.Observe(o)
	for _, d := range e.Devices() {
		if d.MAC == o.MAC && d.Vendor != "" {
			t.Fatal("local MAC assigned a vendor")
		}
	}
}
