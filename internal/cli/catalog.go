package cli

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"slices"
	"strings"
	"text/tabwriter"
	"time"
	"unicode"

	"github.com/st3fan/masscanr/internal/cloud"
	"github.com/st3fan/masscanr/internal/scan"
)

func listRegions(ctx context.Context, args []string, out, diagnostics io.Writer, catalog cloud.Catalog) error {
	flags := flag.NewFlagSet("masscanr list-regions", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	flags.Usage = func() {
		fmt.Fprintln(diagnostics, "Usage: masscanr list-regions\n\nShow region IDs, names/cities, countries and continents.")
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("list-regions accepts no positional arguments")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if catalog == nil {
		return fmt.Errorf("catalog is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	regions, err := catalog.Regions(ctx)
	if err != nil {
		return err
	}
	slices.SortFunc(regions, func(a, b cloud.Region) int { return strings.Compare(a.ID, b.ID) })
	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tNAME\tCOUNTRY\tCONTINENT")
	for _, r := range regions {
		fmt.Fprintf(table, "%s\t%s\t%s\t%s\n", cell(r.ID), cell(r.Name), cell(r.Country), cell(r.Continent))
	}
	return table.Flush()
}

func listInstanceTypes(ctx context.Context, args []string, out, diagnostics io.Writer, catalog cloud.Catalog) error {
	flags := flag.NewFlagSet("masscanr list-instance-types", flag.ContinueOnError)
	flags.SetOutput(diagnostics)
	region := flags.String("region", scan.DefaultRegion, "Vultr region identifier (Atlanta: atl)")
	flags.Usage = func() {
		fmt.Fprintln(diagnostics, "Usage: masscanr list-instance-types [--region REGION]\n\nShow currently available VM plans and their CPU, memory and storage details.")
		flags.PrintDefaults()
	}
	if err := flags.Parse(args); err != nil {
		if errors.Is(err, flag.ErrHelp) {
			return nil
		}
		return err
	}
	if flags.NArg() != 0 {
		return fmt.Errorf("list-instance-types accepts no positional arguments")
	}
	if *region == "" {
		return fmt.Errorf("region must not be empty")
	}
	if ctx.Err() != nil {
		return ctx.Err()
	}
	if catalog == nil {
		return fmt.Errorf("catalog is unavailable")
	}
	ctx, cancel := context.WithTimeout(ctx, time.Minute)
	defer cancel()
	plans, err := catalog.InstanceTypes(ctx, *region)
	if err != nil {
		return err
	}
	slices.SortFunc(plans, func(a, b cloud.InstanceType) int { return strings.Compare(a.ID, b.ID) })
	table := tabwriter.NewWriter(out, 0, 4, 2, ' ', 0)
	fmt.Fprintln(table, "ID\tFAMILY\tVCPUS\tRAM(MB)\tDISKS\tDISK(GB)\tSTORAGE\tCPU\tVENDOR")
	for _, p := range plans {
		storage := strings.Trim(strings.Join([]string{p.StorageType, p.DiskType}, "/"), "/")
		fmt.Fprintf(table, "%s\t%s\t%d\t%d\t%d\t%d\t%s\t%s\t%s\n", cell(p.ID), cell(p.Family), p.VCPUs, p.MemoryMB, p.DiskCount, p.DiskGB, cell(storage), cell(p.CPUType), cell(p.CPUVendor))
	}
	return table.Flush()
}

func cell(value string) string {
	if value == "" {
		return "-"
	}
	return strings.Map(func(r rune) rune {
		if unicode.IsPrint(r) {
			return r
		}
		return ' '
	}, value)
}
