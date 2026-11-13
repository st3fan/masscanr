package cli

import (
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"github.com/st3fan/masscanr/internal/scanfile"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type finding struct {
	Target    string `json:"target,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	IP        string `json:"ip"`
	Port      uint16 `json:"port"`
	Addr      string `json:"addr"`
	Protocol  string `json:"protocol"`
	State     string `json:"state"`
	Timestamp uint64 `json:"timestamp"`
}

func exportPaths(output string) [2]string {
	base := strings.TrimSuffix(output, ".scan")
	return [2]string{base + ".csv", base + ".jsonl"}
}

// Stage both exports beside their destinations. Publish with exclusive hard links
// so an existing file (including a concurrent export) is never overwritten.
func exportResults(source, output string) (err error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	paths := exportPaths(output)
	var staged [2]*os.File
	var published []string
	defer func() {
		for _, file := range staged {
			if file != nil {
				file.Close()
				err = errors.Join(err, os.Remove(file.Name()))
			}
		}
		if err != nil {
			for _, path := range published {
				err = errors.Join(err, os.Remove(path))
			}
		}
	}()
	for i, path := range paths {
		staged[i], err = os.CreateTemp(filepath.Dir(path), ".masscanr-export-*")
		if err != nil {
			return err
		}
	}
	if err = writeExports(in, staged[0], staged[1]); err != nil {
		return err
	}
	for _, file := range staged {
		if err = errors.Join(file.Sync(), file.Close()); err != nil {
			return err
		}
	}
	for i, path := range paths {
		if err = os.Link(staged[i].Name(), path); err != nil {
			return fmt.Errorf("create %s: %w", path, err)
		}
		published = append(published, path)
	}
	return nil
}

func writeExports(in io.Reader, csvOut, jsonlOut io.Writer) error {
	cw := csv.NewWriter(csvOut)
	if err := cw.Write(exportColumns); err != nil {
		return err
	}
	jw := json.NewEncoder(jsonlOut)
	state, err := scanfile.Replay(in, func(f scanfile.Finding) error {
		row := exportFinding(f)
		if err := cw.Write(exportRow(row)); err != nil {
			return err
		}
		return jw.Encode(row)
	})
	if err != nil {
		return err
	}
	if !state.Complete() {
		return fmt.Errorf("scan completion unconfirmed; results are incomplete")
	}
	cw.Flush()
	return cw.Error()
}

func exportFinding(f scanfile.Finding) finding {
	return finding{IP: f.IP.String(), Port: f.Port, Addr: netip.AddrPortFrom(f.IP, f.Port).String(), Protocol: f.Protocol, State: f.State, Timestamp: f.Timestamp, Target: f.Target, Prefix: f.Prefix}
}

var exportColumns = []string{"ip", "port", "addr", "protocol", "state", "timestamp", "target", "prefix"}

func exportRow(row finding) []string {
	return []string{row.IP, strconv.FormatUint(uint64(row.Port), 10), row.Addr, row.Protocol, row.State, strconv.FormatUint(row.Timestamp, 10), row.Target, row.Prefix}
}

// ExportResults creates the standard exports for a completed journal.
func ExportResults(path string) error { return exportResults(path, path) }
