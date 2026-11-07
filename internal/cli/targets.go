package cli

import (
	"fmt"
	"io"
	"strings"

	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/tui"
)

// Every invocation shows the exact frozen scope before any scan side effects.
func describeTargets(out io.Writer, cfg scan.Config, work scan.Workload) error {
	var b strings.Builder
	b.WriteString("Requested targets (first match in command-line order):\n")
	for _, t := range cfg.RequestedTargets {
		fmt.Fprintf(&b, "  %s", t.Target)
		if t.Resolution != nil {
			fmt.Fprintf(&b, " | RIPE snapshot %s | fetched %s", t.Resolution.SnapshotAt.UTC().Format("2006-01-02T15:04:05Z"), t.Resolution.FetchedAt.UTC().Format("2006-01-02T15:04:05Z"))
		}
		b.WriteByte('\n')
		for _, p := range t.Prefixes {
			fmt.Fprintf(&b, "    %s\n", p)
		}
	}
	fmt.Fprintf(&b, "Effective IPv4 scope: %v\n%s addresses x %s TCP ports = %s planned probes\n", cfg.Targets, tui.Count(work.Addresses), tui.Count(work.Ports), tui.Count(work.Probes))
	_, err := io.WriteString(out, b.String())
	return err
}

func targetNames(targets []scan.Target) string {
	names := make([]string, len(targets))
	for i, t := range targets {
		names[i] = t.Target
	}
	return strings.Join(names, ", ")
}
