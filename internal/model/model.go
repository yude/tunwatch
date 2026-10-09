package model

import "time"

type Hint struct {
	Value      string `json:"value"`
	Source     string `json:"source"`
	Confidence string `json:"confidence"`
}
type Device struct {
	ID        string    `json:"id"`
	Interface string    `json:"interface"`
	VLAN      string    `json:"vlan,omitempty"`
	MAC       string    `json:"mac"`
	IPs       []string  `json:"ips"`
	Hostname  Hint      `json:"hostname"`
	Vendor    string    `json:"vendor,omitempty"`
	OS        Hint      `json:"os"`
	LocalMAC  bool      `json:"local_mac"`
	Status    string    `json:"status"`
	FirstSeen time.Time `json:"first_seen"`
	LastSeen  time.Time `json:"last_seen"`
	Sources   []string  `json:"sources"`
}
type Observation struct {
	Interface, VLAN, MAC, IP, Source, Hostname, VendorClass string
	TTL                                                     int
	At                                                      time.Time
}

func (o Observation) ID() string { return o.Interface + "/" + o.VLAN + "/" + o.MAC }

type Event struct {
	ID     string    `json:"id"`
	Time   time.Time `json:"time"`
	Type   string    `json:"type"`
	Reason string    `json:"reason"`
	Device Device    `json:"device"`
}
type Snapshot struct {
	Time    time.Time `json:"time"`
	Devices []Device  `json:"devices"`
}
