package cli

import (
	"bufio"
	"encoding/csv"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/netip"
	"os"
	"path/filepath"
	"strconv"
	"strings"
)

type finding struct {
	IP        string `json:"ip"`
	Port      uint16 `json:"port"`
	Addr      string `json:"addr"`
	Protocol  string `json:"protocol"`
	State     string `json:"state"`
	Timestamp uint64 `json:"timestamp"`
}

func exportPaths(output string) [2]string {
	base := strings.TrimSuffix(output, ".list")
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
	if err := cw.Write([]string{"ip", "port", "addr", "protocol", "state", "timestamp"}); err != nil {
		return err
	}
	jw := json.NewEncoder(jsonlOut)
	scanner := bufio.NewScanner(in)
	scanner.Buffer(make([]byte, 4096), 1<<20)
	complete := false
	lineNumber := 0
	for scanner.Scan() {
		lineNumber++
		line := strings.TrimSpace(scanner.Text())
		if line == "# status complete" && !complete {
			complete = true
			continue
		}
		if strings.HasPrefix(line, "# status ") {
			return fmt.Errorf("line %d: invalid or incomplete scan status", lineNumber)
		}
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		fields := strings.Fields(line)
		if complete || len(fields) != 5 || fields[0] != "open" || fields[1] != "tcp" {
			return fmt.Errorf("line %d: invalid masscan list record", lineNumber)
		}
		ip, ipErr := netip.ParseAddr(fields[3])
		port, portErr := strconv.ParseUint(fields[2], 10, 16)
		timestamp, timestampErr := strconv.ParseUint(fields[4], 10, 64)
		if ipErr != nil || !ip.Is4() || portErr != nil || timestampErr != nil {
			return fmt.Errorf("line %d: invalid finding address, port or timestamp", lineNumber)
		}
		f := finding{IP: ip.String(), Port: uint16(port), Addr: netip.AddrPortFrom(ip, uint16(port)).String(), Protocol: fields[1], State: fields[0], Timestamp: timestamp}
		if err := cw.Write([]string{f.IP, strconv.FormatUint(port, 10), f.Addr, f.Protocol, f.State, strconv.FormatUint(timestamp, 10)}); err != nil {
			return err
		}
		if err := jw.Encode(f); err != nil {
			return err
		}
	}
	if err := scanner.Err(); err != nil {
		return err
	}
	if !complete {
		return fmt.Errorf("missing scan completion footer")
	}
	cw.Flush()
	return cw.Error()
}
