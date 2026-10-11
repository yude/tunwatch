package monitor

import (
	"errors"
	"net/netip"
	"testing"
	"time"

	"github.com/yude/tunwatch/internal/config"
	"github.com/yude/tunwatch/internal/model"
)

type memoryJournal struct {
	events []model.Event
	fail   bool
}

func (j *memoryJournal) Append(e model.Event) error {
	if j.fail {
		return errors.New("disk full")
	}
	j.events = append(j.events, e)
	return nil
}
func (j *memoryJournal) Save([]model.Device, time.Time) error { return nil }
func testEngine() (config.Config, *memoryJournal, *Engine, time.Time) {
	now := time.Date(2026, 1, 1, 0, 0, 0, 0, time.UTC)
	c := config.Config{MaxDevices: 100, RetentionDays: 30, Stale: 10 * time.Second, Offline: 30 * time.Second, Grace: time.Second}
	j := &memoryJournal{}
	return c, j, NewEngine(c, j, Vendors{}, nil, now), now
}
func observation(at time.Time) model.Observation {
	return model.Observation{Interface: "eth0", MAC: "00:11:22:33:44:55", IP: "192.0.2.10", Source: "arp", At: at}
}
func TestPresenceAndNotificationDebounce(t *testing.T) {
	_, j, e, now := testEngine()
	var notifications []model.Event
	e.Notify = func(v model.Event) { notifications = append(notifications, v) }
	if err := e.Observe(observation(now)); err != nil {
		t.Fatal(err)
	}
	if len(notifications) != 0 {
		t.Fatal("startup notification storm")
	}
	if err := e.Tick(now.Add(11 * time.Second)); err != nil {
		t.Fatal(err)
	}
	if e.Devices()[0].Status != "stale" {
		t.Fatal("expected stale")
	}
	e.Observe(observation(now.Add(12 * time.Second)))
	if len(notifications) != 0 {
		t.Fatal("stale recovery should not notify reconnect")
	}
	e.Tick(now.Add(43 * time.Second))
	if e.Devices()[0].Status != "offline" || len(notifications) != 1 {
		t.Fatal("offline inference missing")
	}
	e.Observe(observation(now.Add(44 * time.Second)))
	if len(notifications) != 2 || notifications[1].Type != "online" {
		t.Fatal("reconnect missing")
	}
	if len(j.events) != 5 {
		t.Fatalf("unexpected events: %d", len(j.events))
	}
}
func TestRestartDoesNotInventDisconnects(t *testing.T) {
	c, j, e, now := testEngine()
	e.Observe(observation(now))
	restored := map[string]model.Device{}
	for _, d := range e.Devices() {
		restored[d.ID] = d
	}
	e = NewEngine(c, j, Vendors{}, restored, now.Add(time.Hour))
	e.Tick(now.Add(2 * time.Hour))
	if e.Devices()[0].Status != "unknown" || len(j.events) != 1 {
		t.Fatal("restart inferred a disconnect without observation")
	}
}
func TestDurabilityBeforeStateChange(t *testing.T) {
	_, j, e, now := testEngine()
	j.fail = true
	if err := e.Observe(observation(now)); err == nil {
		t.Fatal("swallowed storage error")
	}
	if len(e.Devices()) != 0 {
		t.Fatal("unpersisted transition visible")
	}
}
func TestIsolationAndCapacity(t *testing.T) {
	c, j, _, now := testEngine()
	c.MaxDevices = 2
	e := NewEngine(c, j, Vendors{}, nil, now)
	o := observation(now)
	e.Observe(o)
	o.VLAN = "10"
	e.Observe(o)
	o.Interface = "eth1"
	e.Observe(o)
	count, rejected := e.Stats()
	if count != 2 || rejected != 1 {
		t.Fatal("interface/VLAN isolation or capacity failed")
	}
	copy := e.Devices()
	copy[0].IPs[0] = "mutated"
	if e.Devices()[0].IPs[0] == "mutated" {
		t.Fatal("snapshot aliases live state")
	}
}
func TestVIPDetectionKeepsPhysicalRouterAndHidesVirtualIdentity(t *testing.T) {
	c, j, e, now := testEngine()
	virtual := observation(now.Add(2 * time.Second))
	virtual.MAC = "00:00:5e:00:01:2a"
	virtual.IP = "192.0.2.1"
	virtual.VIPs = []model.VIP{{IP: virtual.IP, Source: "vrrp_mac", VRID: 42}}
	var notifications []model.Event
	e.Notify = func(event model.Event) { notifications = append(notifications, event) }
	if err := e.Observe(virtual); err != nil {
		t.Fatal(err)
	}
	router := observation(now.Add(3 * time.Second))
	router.VIPs = []model.VIP{{IP: virtual.IP, Source: "vrrp_advertisement", VRID: 42}}
	if err := e.Observe(router); err != nil {
		t.Fatal(err)
	}
	devices := e.Devices()
	if len(devices) != 2 || len(e.VIPs()) != 1 || e.VIPs()[0].Source != "vrrp_advertisement" {
		t.Fatalf("VIP evidence lost: devices=%+v vips=%+v", devices, e.VIPs())
	}
	for _, d := range devices {
		if d.MAC == virtual.MAC && (!d.VIPOnly || len(d.VIPs) != 1) {
			t.Fatalf("virtual identity not marked: %+v", d)
		}
		if d.MAC == router.MAC && (d.VIPOnly || len(d.VIPs) != 1 || d.VIPs[0].IP != virtual.IP) {
			t.Fatalf("physical router incorrectly hidden: %+v", d)
		}
	}
	if len(notifications) != 1 || notifications[0].Device.MAC != router.MAC {
		t.Fatalf("virtual identity notification leaked: %+v", notifications)
	}
	restored := map[string]model.Device{}
	for _, d := range devices {
		restored[d.ID] = d
	}
	e = NewEngine(c, j, Vendors{}, restored, now.Add(time.Hour))
	if len(e.VIPs()) != 1 || len(e.Devices()) != 2 {
		t.Fatal("VIP evidence not restored")
	}
	if err := e.Tick(now.Add(2 * time.Hour)); err != nil {
		t.Fatal(err)
	}
	if len(e.VIPs()) != 0 {
		t.Fatal("stale VIP evidence did not expire")
	}
}

func TestVIPEvidenceIsScopedAndRolledBackOnStorageFailure(t *testing.T) {
	_, j, e, now := testEngine()
	j.fail = true
	o := observation(now)
	o.VIPs = []model.VIP{{IP: "192.0.2.1", Source: "vrrp_advertisement", VRID: 42}}
	if err := e.Observe(o); err == nil || len(e.VIPs()) != 0 {
		t.Fatalf("failed observation retained VIP: %+v", e.VIPs())
	}
	j.fail = false
	if err := e.Observe(o); err != nil {
		t.Fatal(err)
	}
	other := observation(now.Add(time.Second))
	other.VLAN = "20"
	other.IP = "192.0.2.1"
	if err := e.Observe(other); err != nil {
		t.Fatal(err)
	}
	for _, d := range e.Devices() {
		if d.VLAN == "20" && (d.VIPOnly || len(d.VIPs) != 0) {
			t.Fatalf("VIP leaked across VLANs: %+v", d)
		}
	}
}
func TestScanStaysOnLinkAndBounded(t *testing.T) {
	local := []netip.Prefix{netip.MustParsePrefix("192.0.2.10/24")}
	p, err := scanTargets([]netip.Prefix{netip.MustParsePrefix("192.0.2.0/29")}, local)
	if err != nil || len(p) != 6 {
		t.Fatalf("%d %v", len(p), err)
	}
	p, err = scanTargets([]netip.Prefix{netip.MustParsePrefix("198.51.100.0/24")}, local)
	if err != nil || len(p) != 0 {
		t.Fatal("off-link probing")
	}
	if _, err = scanTargets([]netip.Prefix{netip.MustParsePrefix("192.0.0.0/16")}, local); err == nil {
		t.Fatal("unbounded scan")
	}
}

func TestScanExcludesAllLocalAddressesAndDuplicateNetworks(t *testing.T) {
	local := []netip.Prefix{netip.MustParsePrefix("192.0.2.1/24"), netip.MustParsePrefix("192.0.2.2/24")}
	networks := []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24"), netip.MustParsePrefix("192.0.2.0/25")}
	probes, err := scanTargets(networks, local)
	if err != nil {
		t.Fatal(err)
	}
	if len(probes) != 252 {
		t.Fatalf("expected 252 distinct targets, got %d", len(probes))
	}
	for _, p := range probes {
		if p.target == local[0].Addr() || p.target == local[1].Addr() {
			t.Fatal("probing own address")
		}
	}
}
