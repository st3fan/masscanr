package scan

import (
	"net/netip"
	"testing"
	"time"
)

func TestIntentHasNoPlacement(t *testing.T) {
	c := Defaults()
	c.Targets = []netip.Prefix{netip.MustParsePrefix("192.0.2.0/24")}
	s := c.Intent()
	if err := s.Validate(); err != nil {
		t.Fatal(err)
	}
	if s.Wait != 10*time.Second {
		t.Fatal(s.Wait)
	}
	for _, bad := range []Spec{
		{Ports: s.Ports, Rate: s.Rate},
		{Targets: s.Targets, Ports: []uint16{80, 80}, Rate: s.Rate},
		{Targets: []netip.Prefix{s.Targets[0], netip.MustParsePrefix("192.0.2.1/32")}, Ports: s.Ports, Rate: s.Rate},
		{Targets: s.Targets, Ports: s.Ports, Rate: s.Rate, Wait: time.Millisecond},
	} {
		if err := bad.Validate(); err == nil {
			t.Fatal("accepted invalid intent", bad)
		}
	}
	if err := (Shard{Index: 0, Total: 2}).Validate(); err == nil {
		t.Fatal("accepted zero-based common shard")
	}
}
