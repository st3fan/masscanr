package scanfile

import (
	"bufio"
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"slices"
	"sort"
	"strconv"
	"strings"
	"unicode/utf8"
)

type State struct {
	Header    *Header
	Seq       uint64
	Found     uint64
	Workers   map[string]Worker
	Backends  map[int]Backend
	Progress  map[int]Progress
	Counts    map[int]uint64
	Finished  *Final
	Truncated bool
	ports     []uint16
}

func workerKey(w Worker) string {
	if w.Shard > 0 {
		return strconv.Itoa(w.Shard)
	}
	return "instance:" + w.InstanceID
}

func (s *State) Outcomes() []Outcome {
	var result []Outcome
	if s.Header != nil {
		for i := 1; i <= s.Header.Config.Shards; i++ {
			result = append(result, Outcome{Shard: i, Phase: s.Workers[strconv.Itoa(i)].Phase, Found: s.Counts[i]})
		}
	}
	return result
}

func (s *State) Undeleted() []string {
	var ids []string
	for _, w := range s.Workers {
		if w.InstanceID != "" && w.Cleanup != "complete" {
			ids = append(ids, w.InstanceID)
		}
	}
	sort.Strings(ids)
	return slices.Compact(ids)
}

func (s *State) Complete() bool {
	return s.Finished != nil && s.Finished.ScanOutcome == "complete" && !s.Truncated
}

func (s *State) Apply(r Record) error {
	if s.Finished != nil {
		return fmt.Errorf("record after finished")
	}
	if r.Seq != s.Seq+1 || r.At.IsZero() {
		return fmt.Errorf("invalid sequence or missing observation time")
	}
	payloads := 0
	for _, present := range []bool{r.Scan != nil, r.Backend != nil, r.Worker != nil, r.Progress != nil, r.Finding != nil, r.Finished != nil} {
		if present {
			payloads++
		}
	}
	if payloads != 1 {
		return fmt.Errorf("record must have exactly one payload")
	}
	if r.Type == "scan" && r.Scan != nil {
		h := r.Scan
		if s.Header != nil || r.Seq != 1 || h.Format != Format || h.Schema != Schema || h.RunID == "" || h.CreatedAt.IsZero() || h.ControllerVersion == "" || h.Label != "masscanr" || h.OwnershipTag != "masscanr-run-"+h.RunID {
			return fmt.Errorf("invalid or unsupported scan header")
		}
		if err := h.Config.Validate(); err != nil {
			return err
		}
		s.Header = h
		s.Workers = make(map[string]Worker)
		s.Backends = make(map[int]Backend)
		s.Progress = make(map[int]Progress)
		s.Counts = make(map[int]uint64)
		for i := 1; i <= h.Config.Shards; i++ {
			s.Workers[strconv.Itoa(i)] = Worker{Shard: i, Phase: "pending", Cleanup: "pending"}
		}
		s.ports, _ = parsePorts(h.Config.Ports)
		s.Seq = r.Seq
		return nil
	}
	if s.Header == nil {
		return fmt.Errorf("first record must be scan")
	}
	validShard := func(i int) bool { return i > 0 && i <= s.Header.Config.Shards }
	switch {
	case r.Type == "backend" && r.Backend != nil:
		b := *r.Backend
		if !validShard(b.Shard) || b.Name != s.Header.Config.Backend || b.Version == "" || b.Build == "" {
			return fmt.Errorf("invalid backend identity")
		}
		if _, ok := s.Backends[b.Shard]; ok {
			return fmt.Errorf("duplicate backend identity")
		}
		if s.Workers[strconv.Itoa(b.Shard)].Phase != "preparing" {
			return fmt.Errorf("backend identity outside preparation")
		}
		s.Backends[b.Shard] = b
	case r.Type == "worker" && r.Worker != nil:
		w := *r.Worker
		if !validShard(w.Shard) && (w.Shard != 0 || w.InstanceID == "") {
			return fmt.Errorf("invalid worker identity")
		}
		key := workerKey(w)
		prior, known := s.Workers[key]
		if !known {
			if len(s.Workers) >= 2*s.Header.Config.Shards {
				return fmt.Errorf("too many reconciled workers")
			}
			prior = Worker{Shard: 0, InstanceID: w.InstanceID, Phase: "pending", Cleanup: "pending"}
		}
		if prior.InstanceID != "" && w.InstanceID != prior.InstanceID {
			return fmt.Errorf("worker identity changed")
		}
		for k, other := range s.Workers {
			if k != key && w.InstanceID != "" && other.InstanceID == w.InstanceID {
				return fmt.Errorf("instance assigned to multiple shards")
			}
		}
		if !transition(prior, w) {
			return fmt.Errorf("invalid worker transition %s/%s to %s/%s", prior.Phase, prior.Cleanup, w.Phase, w.Cleanup)
		}
		if w.Phase == "ready" {
			if _, ok := s.Backends[w.Shard]; !ok {
				return fmt.Errorf("ready worker has no backend identity")
			}
		}
		if w.Phase == "scanning" {
			var identity Backend
			for i := 1; i <= s.Header.Config.Shards; i++ {
				b, ok := s.Backends[i]
				if !ok || s.Workers[strconv.Itoa(i)].Phase == "preparing" {
					return fmt.Errorf("scan before all workers prepared")
				}
				if i == 1 {
					identity = b
				}
				if b.Name != identity.Name || b.Version != identity.Version || b.Build != identity.Build {
					return fmt.Errorf("incompatible worker builds")
				}
			}
		}
		s.Workers[key] = w
	case r.Type == "progress" && r.Progress != nil:
		p := *r.Progress
		if !validShard(p.Shard) {
			return fmt.Errorf("invalid progress shard")
		}
		w := s.Workers[strconv.Itoa(p.Shard)]
		if w.Phase != "scanning" && w.Phase != "waiting" {
			return fmt.Errorf("progress outside scanning")
		}
		for i, v := range []*float64{p.Percent, p.Rate, p.ETASeconds} {
			if v != nil && (math.IsNaN(*v) || math.IsInf(*v, 0) || *v < 0 || (i == 0 && *v > 100)) {
				return fmt.Errorf("invalid progress measurement")
			}
		}
		s.Progress[p.Shard] = p
	case r.Type == "finding" && r.Finding != nil:
		f := *r.Finding
		if !validShard(f.Shard) || !f.IP.Is4() || f.Protocol != "tcp" || f.State != "open" || !slices.Contains(s.ports, f.Port) {
			return fmt.Errorf("invalid finding")
		}
		w := s.Workers[strconv.Itoa(f.Shard)]
		if w.Phase != "scanning" && w.Phase != "waiting" {
			return fmt.Errorf("finding outside scanning")
		}
		inScope := false
		for _, p := range s.Header.Config.Targets {
			inScope = inScope || p.Contains(f.IP)
		}
		if !inScope {
			return fmt.Errorf("out-of-scope finding")
		}
		s.Found++
		s.Counts[f.Shard]++
	case r.Type == "finished" && r.Finished != nil:
		f := r.Finished
		if f.ScanOutcome != "complete" && f.ScanOutcome != "failed" && f.ScanOutcome != "cancelled" {
			return fmt.Errorf("invalid scan outcome")
		}
		if f.CleanupOutcome != "complete" && f.CleanupOutcome != "incomplete" {
			return fmt.Errorf("invalid cleanup outcome")
		}
		if f.Found != s.Found || !slices.Equal(f.Shards, s.Outcomes()) || !slices.Equal(f.Undeleted, s.Undeleted()) {
			return fmt.Errorf("summary disagrees with recorded results/state")
		}
		if (f.CleanupOutcome == "complete") != (len(f.Undeleted) == 0 && !f.ReconciliationUnresolved) {
			return fmt.Errorf("inconsistent cleanup summary")
		}
		if f.ScanOutcome == "complete" {
			for _, o := range f.Shards {
				if o.Phase != "completed" {
					return fmt.Errorf("complete run has unfinished shards")
				}
			}
		}
		s.Finished = f
	default:
		return fmt.Errorf("unknown record type or mismatched payload %q", r.Type)
	}
	s.Seq = r.Seq
	return nil
}

func transition(a, b Worker) bool {
	if b.Cleanup != "pending" && b.Cleanup != "deleting" && b.Cleanup != "incomplete" && b.Cleanup != "complete" {
		return false
	}
	if a.Cleanup == "complete" {
		return false
	}
	if b.Cleanup != a.Cleanup {
		if b.Phase != a.Phase || b.InstanceID == "" {
			return false
		}
		return b.Cleanup == "deleting" && (a.Cleanup == "pending" || a.Cleanup == "incomplete") || (b.Cleanup == "complete" || b.Cleanup == "incomplete") && a.Cleanup == "deleting"
	}
	if a.Phase == b.Phase {
		return a.Phase != "completed" && a.Phase != "failed" && a.Phase != "cancelled"
	}
	if b.Cleanup != "pending" {
		return false
	}
	if b.Phase == "failed" || b.Phase == "cancelled" {
		return a.Phase != "completed" && a.Phase != "failed" && a.Phase != "cancelled"
	}
	if b.Phase != "provisioning" && b.InstanceID == "" {
		return false
	}
	return a.Phase == "pending" && b.Phase == "provisioning" || a.Phase == "provisioning" && b.Phase == "booting" || a.Phase == "booting" && b.Phase == "preparing" || a.Phase == "preparing" && b.Phase == "ready" || a.Phase == "ready" && b.Phase == "scanning" || (a.Phase == "scanning" || a.Phase == "waiting") && (b.Phase == "waiting" || b.Phase == "scanning" || b.Phase == "collecting") || a.Phase == "collecting" && b.Phase == "completed"
}

// Replay reads a stable file or snapshot with bounded per-record memory. An
// unterminated tail is partial, even when its bytes happen to be valid JSON.
// Finding callbacks are provisional until Replay returns successfully.
func Replay(in io.Reader, finding func(Finding) error) (State, error) {
	var s State
	reader := bufio.NewReaderSize(in, 64<<10)
	for {
		var line []byte
		for {
			part, err := reader.ReadSlice('\n')
			if len(line)+len(part) > MaxRecordBytes {
				return s, fmt.Errorf("record %d exceeds %d bytes", s.Seq+1, MaxRecordBytes)
			}
			line = append(line, part...)
			if err == bufio.ErrBufferFull {
				continue
			}
			if err != nil {
				if errors.Is(err, io.EOF) {
					if len(line) > 0 {
						s.Truncated = true
					}
					if s.Header == nil {
						return s, fmt.Errorf("missing complete scan header")
					}
					return s, nil
				}
				return s, err
			}
			break
		}
		if !utf8.Valid(line) {
			return s, fmt.Errorf("record %d: invalid UTF-8", s.Seq+1)
		}
		if err := uniqueKeys(line); err != nil {
			return s, fmt.Errorf("record %d: %w", s.Seq+1, err)
		}
		if err := requiredFields(line); err != nil {
			return s, fmt.Errorf("record %d: %w", s.Seq+1, err)
		}
		var r Record
		if err := json.Unmarshal(line, &r); err != nil {
			return s, fmt.Errorf("record %d: invalid JSON: %w", s.Seq+1, err)
		}
		if err := s.Apply(r); err != nil {
			return s, fmt.Errorf("record %d: %w", s.Seq+1, err)
		}
		if r.Finding != nil && finding != nil {
			if err := finding(*r.Finding); err != nil {
				return s, err
			}
		}
	}
}

// Reject duplicate keys recursively, including unknown extension objects. Go's
// default JSON decoder otherwise accepts ambiguous, last-key-wins documents.
func uniqueKeys(line []byte) error {
	d := json.NewDecoder(bytes.NewReader(line))
	d.UseNumber()
	var value func(int) error
	value = func(depth int) error {
		if depth > 64 {
			return fmt.Errorf("JSON nesting exceeds 64")
		}
		t, err := d.Token()
		if err != nil {
			return err
		}
		delim, ok := t.(json.Delim)
		if !ok {
			return nil
		}
		switch delim {
		case '{':
			keys := make(map[string]bool)
			for d.More() {
				t, err = d.Token()
				if err != nil {
					return err
				}
				k, ok := t.(string)
				k = strings.ToLower(k)
				if !ok || keys[k] {
					return fmt.Errorf("duplicate or invalid JSON key")
				}
				keys[k] = true
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		case '[':
			for d.More() {
				if err = value(depth + 1); err != nil {
					return err
				}
			}
		default:
			return fmt.Errorf("unexpected JSON delimiter")
		}
		_, err = d.Token()
		return err
	}
	if err := value(0); err != nil {
		return err
	}
	if _, err := d.Token(); err != io.EOF {
		return fmt.Errorf("multiple JSON values or trailing data")
	}
	return nil
}

// Missing JSON numbers/bools must not silently become valid measured zeroes.
func requiredFields(line []byte) error {
	object := func(data []byte, keys ...string) (map[string]json.RawMessage, error) {
		var m map[string]json.RawMessage
		if err := json.Unmarshal(data, &m); err != nil {
			return nil, err
		}
		for _, k := range keys {
			if v, ok := m[k]; !ok || bytes.Equal(bytes.TrimSpace(v), []byte("null")) {
				return nil, fmt.Errorf("missing or null field %s", k)
			}
		}
		return m, nil
	}
	m, err := object(line, "type", "seq", "at")
	if err != nil {
		return err
	}
	var kind string
	if err = json.Unmarshal(m["type"], &kind); err != nil {
		return err
	}
	spec := map[string][]string{
		"scan":     {"format", "schema", "run_id", "created_at", "controller_version", "label", "ownership_tag", "config"},
		"backend":  {"shard", "name", "version", "build"},
		"worker":   {"shard", "phase", "cleanup"},
		"progress": {"shard", "waiting"},
		"finding":  {"shard", "ip", "port", "protocol", "state", "timestamp"},
		"finished": {"scan_outcome", "cleanup_outcome", "found", "shards", "reconciliation_unresolved"},
	}
	keys, ok := spec[kind]
	if !ok {
		return fmt.Errorf("unknown record type %q", kind)
	}
	payload, err := object(m[kind], keys...)
	if err != nil {
		return err
	}
	if kind == "scan" {
		_, err = object(payload["config"], "targets", "ports", "backend", "seed", "shards", "rate_per_worker", "provider", "region", "instance_type", "timeouts", "output", "exports")
	}
	if kind == "progress" {
		for _, k := range []string{"percent", "rate", "eta_seconds"} {
			if _, ok := payload[k]; !ok {
				return fmt.Errorf("missing nullable progress field %s", k)
			}
		}
	}
	if kind == "finished" {
		if _, ok := payload["undeleted"]; !ok {
			return fmt.Errorf("missing undeleted field")
		}
		var shards []json.RawMessage
		if err = json.Unmarshal(payload["shards"], &shards); err != nil {
			return err
		}
		for _, shard := range shards {
			if _, err = object(shard, "shard", "phase", "found"); err != nil {
				return err
			}
		}
	}
	return err
}
