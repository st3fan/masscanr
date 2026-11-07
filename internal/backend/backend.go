// Package backend defines the scanner/transport boundary. Implementations own
// native commands and schemas; orchestration consumes common scan concepts.
package backend

import (
	"bufio"
	"bytes"
	"io"

	"github.com/st3fan/masscanr/internal/scan"
)

type Command struct {
	Executable string
	Args       []string
}

func (c Command) Argv() []string { return append([]string{c.Executable}, c.Args...) }

type Probe struct {
	Command   Command
	ExitCodes []int
}
type Preparation struct {
	Packages []string // APK constraints, e.g. masscan~1.3.2.
	Probes   []Probe
	Verify   func(outputs []string, installed string) (scan.Identity, error)
}

type File struct{ Name, Content string } // Private basename in a run-owned directory.
type Plan struct {
	Command    Command
	Files      []File
	NewDecoder func() Decoder // Fresh mutable decoder per shard execution.
}

type Decoder interface {
	Results(io.Reader, func(scan.Finding) error) error
	// Status recognizes native telemetry, leaving other records as diagnostics.
	Status(string) (scan.Progress, bool, error)
}

type Backend interface {
	Name() string
	MaxSeed() uint64
	Validate(scan.Spec, int) error
	Preparation() Preparation
	Build(scan.Spec, scan.Shard, uint64) (Plan, error)
}

// Records accepts CR status updates and LF records, including an EOF fragment.
// Bound every native record so a remote worker cannot exhaust local memory.
func Records(reader io.Reader, visit func(string) error) error {
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 4096), 1<<20)
	s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for s.Scan() {
		if line := s.Text(); line != "" {
			if err := visit(line); err != nil {
				return err
			}
		}
	}
	return s.Err()
}
