// Package results converts completed scan journals to portable result exports.
package results

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

type Finding struct {
	Target    string `json:"target,omitempty"`
	Prefix    string `json:"prefix,omitempty"`
	IP        string `json:"ip"`
	Port      uint16 `json:"port"`
	Addr      string `json:"addr"`
	Protocol  string `json:"protocol"`
	State     string `json:"state"`
	Timestamp uint64 `json:"timestamp"`
}

func Paths(output string) [2]string {
	base := strings.TrimSuffix(output, ".scan")
	return [2]string{base + ".csv", base + ".jsonl"}
}

// Stage both exports beside their destinations. Publish with exclusive hard links
// so an existing file (including a concurrent export) is never overwritten.
func Export(source, output string) (err error) {
	in, err := os.Open(source)
	if err != nil {
		return err
	}
	defer in.Close()
	paths := Paths(output)
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
	if err = Write(in, staged[0], staged[1]); err != nil {
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

func Write(in io.Reader, csvOut, jsonlOut io.Writer) error {
	cw := csv.NewWriter(csvOut)
	if err := cw.Write(Columns); err != nil {
		return err
	}
	jw := json.NewEncoder(jsonlOut)
	state, err := scanfile.Replay(in, func(f scanfile.Finding) error {
		row := FromScan(f)
		if err := cw.Write(Row(row)); err != nil {
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

func FromScan(f scanfile.Finding) Finding {
	return Finding{IP: f.IP.String(), Port: f.Port, Addr: netip.AddrPortFrom(f.IP, f.Port).String(), Protocol: f.Protocol, State: f.State, Timestamp: f.Timestamp, Target: f.Target, Prefix: f.Prefix}
}

var Columns = []string{"ip", "port", "addr", "protocol", "state", "timestamp", "target", "prefix"}

func Row(row Finding) []string {
	return []string{row.IP, strconv.FormatUint(uint64(row.Port), 10), row.Addr, row.Protocol, row.State, strconv.FormatUint(row.Timestamp, 10), row.Target, row.Prefix}
}

// ExportResults creates the standard exports for a completed journal.
func ExportResults(path string) error { return Export(path, path) }
