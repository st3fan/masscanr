package cli

import (
	"io"

	"github.com/st3fan/masscanr/internal/results"
	"github.com/st3fan/masscanr/internal/scanfile"
)

// The retired command surface remains buildable until the final cutover.
type finding = results.Finding

var exportColumns = results.Columns

func exportPaths(output string) [2]string { return results.Paths(output) }

func exportResults(source, output string) error { return results.Export(source, output) }

func writeExports(in io.Reader, csvOut, jsonlOut io.Writer) error {
	return results.Write(in, csvOut, jsonlOut)
}

func exportFinding(f scanfile.Finding) finding { return results.FromScan(f) }

func exportRow(row finding) []string { return results.Row(row) }
