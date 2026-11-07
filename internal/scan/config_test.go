package scan

import (
	"reflect"
	"testing"
)

func TestPorts(t *testing.T) {
	ports, err := ParsePorts("22,80,443,8000-8002,80,0,65535")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(ports, []uint16{0, 22, 80, 443, 8000, 8001, 8002, 65535}) {
		t.Fatal(ports)
	}
	all, err := ParsePorts("0-65535")
	if err != nil || len(all) != 65536 {
		t.Fatal(len(all), err)
	}
	if got := (Config{Ports: all}).PortSpec(); got != "0-65535" {
		t.Fatal(got)
	}
	for _, bad := range []string{"", "80,", "-1", "65536", "90-80", "80-", "1-2-3", "U:53", "http", "80;reboot"} {
		if _, err := ParsePorts(bad); err == nil {
			t.Errorf("accepted %q", bad)
		}
	}
}

func TestTargets(t *testing.T) {
	got, err := ParseTargets([]string{"198.51.100.4/24", "192.0.2.1/25", "192.0.2.0/24", "192.0.2.128/25"})
	if err != nil {
		t.Fatal(err)
	}
	if len(got) != 2 || got[0].String() != "192.0.2.0/24" || got[1].String() != "198.51.100.0/24" {
		t.Fatal(got)
	}
	for _, values := range [][]string{nil, {"::/0"}, {"192.0.2.1"}, {"192.0.2.0/33"}} {
		if _, err := ParseTargets(values); err == nil {
			t.Errorf("accepted %v", values)
		}
	}
}

func TestDefaults(t *testing.T) {
	c := Defaults()
	c.Targets, _ = ParseTargets([]string{"192.0.2.0/24"})
	if len(c.Ports) != 100 || c.Shards != 2 || c.Rate != 500000 || c.Region != "atl" || c.InstanceType != "vc2-1c-1gb" {
		t.Fatal(c)
	}
	c.Shards = 0
	if err := c.Validate(); err == nil {
		t.Fatal("accepted zero workers")
	}
}
