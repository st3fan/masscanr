package scan

// ConfirmationProbeThreshold is an initial checkpoint for reviewing scan work.
// It is not a limit on sending rate or a guarantee of network safety.
const ConfirmationProbeThreshold uint64 = 1_000_000

// Workload counts requested address/port pairs, before backend exclusions.
// Sharding distributes these probes; it does not multiply them.
type Workload struct {
	Addresses uint64
	Ports     uint64
	Probes    uint64
}

func (w Workload) RequiresConfirmation() bool { return w.Probes > ConfirmationProbeThreshold }

// Workload requires the normalized, disjoint targets and distinct sorted ports
// used by both backends. No addresses or address/port pairs are enumerated.
func (s Spec) Workload() (Workload, error) {
	if err := s.Validate(); err != nil {
		return Workload{}, err
	}
	w := Workload{Ports: uint64(len(s.Ports))}
	for _, p := range s.Targets {
		w.Addresses += uint64(1) << (32 - p.Bits())
	}
	// Validation bounds the union at 2^32 addresses and the ports at 2^16.
	// Their product is at most 2^48, safely within uint64 on every platform.
	w.Probes = w.Addresses * w.Ports
	return w, nil
}
