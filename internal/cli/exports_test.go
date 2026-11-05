package cli

import (
	"bytes"
	"encoding/csv"
	"encoding/json"
	"errors"
	"io"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

const exportFixture = "#masscan\n# shard 1\nopen tcp 80 192.0.2.1 1700000000\n# shard 2\nopen tcp 443 192.0.2.1 1700000001\nopen tcp 443 192.0.2.1 1700000001\n# status complete\n"

func TestExportFindingSchemasAndOrder(t *testing.T) {
	var csvOut, jsonlOut bytes.Buffer
	if err := writeExports(strings.NewReader(exportFixture), &csvOut, &jsonlOut); err != nil {
		t.Fatal(err)
	}
	rows, err := csv.NewReader(&csvOut).ReadAll()
	want := [][]string{
		{"ip", "port", "addr", "protocol", "state", "timestamp"},
		{"192.0.2.1", "80", "192.0.2.1:80", "tcp", "open", "1700000000"},
		{"192.0.2.1", "443", "192.0.2.1:443", "tcp", "open", "1700000001"},
		{"192.0.2.1", "443", "192.0.2.1:443", "tcp", "open", "1700000001"},
	}
	if err != nil || !reflect.DeepEqual(rows, want) {
		t.Fatal(rows, err)
	}
	lines := strings.Split(strings.TrimSuffix(jsonlOut.String(), "\n"), "\n")
	if len(lines) != 3 {
		t.Fatal(lines)
	}
	for i, line := range lines {
		var got map[string]any
		if err := json.Unmarshal([]byte(line), &got); err != nil {
			t.Fatal(err)
		}
		port, timestamp := float64(443), float64(1700000001)
		if i == 0 {
			port, timestamp = 80, 1700000000
		}
		if !reflect.DeepEqual(got, map[string]any{"ip": "192.0.2.1", "port": port, "addr": want[i+1][2], "protocol": "tcp", "state": "open", "timestamp": timestamp}) {
			t.Fatal(got)
		}
	}
}

func TestEmptyScanExports(t *testing.T) {
	var c, j bytes.Buffer
	if err := writeExports(strings.NewReader("#masscan\n# status complete\n"), &c, &j); err != nil || c.String() != "ip,port,addr,protocol,state,timestamp\n" || j.Len() != 0 {
		t.Fatal(err, c.String(), j.String())
	}
}

func TestExportRejectsInvalidOrIncompleteList(t *testing.T) {
	for _, input := range []string{
		"", "# status incomplete\n", "open tcp 80 192.0.2.1 1\n",
		"open tcp 65536 192.0.2.1 1\n# status complete\n",
		"open tcp 80 ::1 1\n# status complete\n",
		"open tcp 80 invalid 1\n# status complete\n",
		"open tcp 80 192.0.2.1 -1\n# status complete\n",
		"open udp 80 192.0.2.1 1\n# status complete\n",
		"garbage\n# status complete\n",
		"# status complete\nopen tcp 80 192.0.2.1 1\n",
		"# status complete\n# status complete\n",
		strings.Repeat("x", 1<<20) + "\n# status complete\n",
	} {
		if err := writeExports(strings.NewReader(input), io.Discard, io.Discard); err == nil {
			t.Fatal("accepted malformed or incomplete list")
		}
	}
}

type failedExportWriter struct{}

func (failedExportWriter) Write([]byte) (int, error) { return 0, io.ErrClosedPipe }

func TestExportWriteFailures(t *testing.T) {
	for _, writers := range [][2]io.Writer{{failedExportWriter{}, io.Discard}, {io.Discard, failedExportWriter{}}} {
		if err := writeExports(strings.NewReader(exportFixture), writers[0], writers[1]); !errors.Is(err, io.ErrClosedPipe) {
			t.Fatal(err)
		}
	}
}

func TestExportFilesAndRollback(t *testing.T) {
	for _, mode := range []string{"success", "malformed", "csv-collision", "jsonl-collision"} {
		t.Run(mode, func(t *testing.T) {
			dir := t.TempDir()
			path := filepath.Join(dir, "scan.list")
			data := exportFixture
			if mode == "malformed" {
				data = "garbage\n# status complete\n"
			}
			if err := os.WriteFile(path, []byte(data), 0600); err != nil {
				t.Fatal(err)
			}
			paths := exportPaths(path)
			collision := ""
			if mode == "csv-collision" {
				collision = paths[0]
			} else if mode == "jsonl-collision" {
				collision = paths[1]
			}
			if collision != "" {
				if err := os.WriteFile(collision, []byte("existing"), 0600); err != nil {
					t.Fatal(err)
				}
			}
			err := exportResults(path, path)
			if (err == nil) != (mode == "success") {
				t.Fatal(err)
			}
			original, err := os.ReadFile(path)
			if err != nil || string(original) != data {
				t.Fatal("original list changed", err)
			}
			for _, p := range paths {
				contents, err := os.ReadFile(p)
				switch {
				case mode == "success":
					info, statErr := os.Stat(p)
					if err != nil || statErr != nil || info.Mode().Perm() != 0600 || len(contents) == 0 {
						t.Fatal(p, err, statErr)
					}
				case p == collision:
					if err != nil || string(contents) != "existing" {
						t.Fatal("existing export changed", err)
					}
				default:
					if !errors.Is(err, os.ErrNotExist) {
						t.Fatal("partial export remains", p, err)
					}
				}
			}
			staged, err := filepath.Glob(filepath.Join(dir, ".masscanr-export-*"))
			if err != nil || len(staged) != 0 {
				t.Fatal("temporary exports remain", staged, err)
			}
		})
	}
}

func TestExportPaths(t *testing.T) {
	for _, tc := range []struct {
		input string
		want  [2]string
	}{
		{"dir/masscanr-office-20261105T042952.086607609Z.list", [2]string{"dir/masscanr-office-20261105T042952.086607609Z.csv", "dir/masscanr-office-20261105T042952.086607609Z.jsonl"}},
		{"custom.txt", [2]string{"custom.txt.csv", "custom.txt.jsonl"}},
		{"custom.csv", [2]string{"custom.csv.csv", "custom.csv.jsonl"}},
	} {
		if got := exportPaths(tc.input); got != tc.want {
			t.Fatal(got)
		}
	}
}
