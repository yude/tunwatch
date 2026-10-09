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
