package cli

import (
	"context"
	"fmt"
	"io"
	"os"

	"github.com/st3fan/masscanr/internal/scan"
	"github.com/st3fan/masscanr/internal/tui"
	"golang.org/x/term"
)

func confirmLargeScan(ctx context.Context, cfg scan.Config, work scan.Workload, out io.Writer, plain bool) error {
	terminal, ok := out.(*os.File)
	if !ok || !term.IsTerminal(int(terminal.Fd())) || !term.IsTerminal(int(os.Stdin.Fd())) {
		return fmt.Errorf("%s planned probes (%s TCP ports on %s addresses) exceed the %s-probe confirmation threshold; interactive stdin and stderr terminals are required; scan not started",
			tui.Count(work.Probes), tui.Count(work.Ports), tui.Count(work.Addresses), tui.Count(scan.ConfirmationProbeThreshold))
	}
	return tui.ConfirmScan(ctx, cfg, work, os.Stdin, terminal, plain)
}
