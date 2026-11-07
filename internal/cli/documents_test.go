package cli

import (
	"bytes"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func savedDocument(t *testing.T, data string) string {
	t.Helper()
	path := filepath.Join(t.TempDir(), "inventory.scan")
	if err := os.WriteFile(path, []byte(data), 0600); err != nil {
		t.Fatal(err)
	}
	return path
}

func unfinished(data string) string {
	trimmed := strings.TrimSuffix(data, "\n")
	return trimmed[:strings.LastIndex(trimmed, "\n")+1]
}

func TestOfflineInspect(t *testing.T) {
	t.Setenv("VULTR_API_TOKEN", "")
	for _, tc := range []struct{ name, data, status string }{
		{"complete", exportFixture, "Scan outcome: complete"},
		{"failed", scanFixture("masscan", fixtureFindings(), "failed"), "Scan outcome: failed"},
		{"cancelled", scanFixture("masscan", fixtureFindings(), "cancelled"), "Scan outcome: cancelled"},
		{"unfinished", unfinished(exportFixture), "unconfirmed (partial results)"},
		{"truncated", unfinished(exportFixture) + `{"type":`, "unconfirmed (truncated document)"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := savedDocument(t, tc.data)
			var out, errout bytes.Buffer
			if err := execute(t.Context(), []string{"inspect", path}, &out, &errout, noScanner(t), nil); err != nil {
				t.Fatal(err)
			}
			for _, want := range []string{tc.status, "Accepted findings: 3", "192.0.2.0/24", "80,443", "Last recorded workers (historical observations)", "backend masscan"} {
				if !strings.Contains(out.String(), want) {
					t.Fatal(want, out.String())
				}
			}
		})
	}
}

func TestOfflineExportFormatsAndNoOverwrite(t *testing.T) {
	t.Setenv("VULTR_API_TOKEN", "")
	for _, format := range []string{"list", "csv", "jsonl"} {
		t.Run(format, func(t *testing.T) {
			path := savedDocument(t, exportFixture)
			dest := filepath.Join(t.TempDir(), "export."+format)
			var out, errout bytes.Buffer
			args := []string{"export", path, "--format=" + format, "--output=" + dest}
			if err := execute(t.Context(), args, &out, &errout, noScanner(t), nil); err != nil {
				t.Fatal(err)
			}
			data, err := os.ReadFile(dest)
			if err != nil {
				t.Fatal(err)
			}
			info, _ := os.Stat(dest)
			if info.Mode().Perm() != 0600 || out.Len() != 0 {
				t.Fatal("permissions or stdout", info.Mode(), out.String())
			}
			switch format {
			case "list":
				if !strings.HasPrefix(string(data), "#masscan\n") || strings.Count(string(data), "open tcp") != 3 || !strings.HasSuffix(string(data), "# status complete\n") {
					t.Fatal(string(data))
				}
			case "csv":
				if strings.Count(string(data), "\n") != 4 || !strings.HasPrefix(string(data), "ip,port,addr,protocol,state,timestamp") {
					t.Fatal(string(data))
				}
			case "jsonl":
				if strings.Count(string(data), "\n") != 3 || strings.Contains(string(data), `"type"`) {
					t.Fatal(string(data))
				}
			}
			if err := execute(t.Context(), args, &out, &errout, noScanner(t), nil); err == nil {
				t.Fatal("overwrote export")
			}
			again, _ := os.ReadFile(dest)
			if !bytes.Equal(data, again) {
				t.Fatal("changed existing export")
			}
		})
	}
}

func TestPartialExportRequiresExplicitChoiceAndNeverSkipsCorruption(t *testing.T) {
	for _, tc := range []struct {
		name, data string
		corrupt    bool
	}{
		{"failed", scanFixture("masscan", fixtureFindings(), "failed"), false},
		{"cancelled", scanFixture("masscan", fixtureFindings(), "cancelled"), false},
		{"unfinished", unfinished(exportFixture), false},
		{"truncated", unfinished(exportFixture) + `{"type":`, false},
		{"corrupt", exportFixture + "{}\n", true},
		{"legacy-list", "#masscan\nopen tcp 80 192.0.2.1 1700000000\n# status complete\n", true},
	} {
		t.Run(tc.name, func(t *testing.T) {
			path := savedDocument(t, tc.data)
			dest := filepath.Join(t.TempDir(), "export.csv")
			var out, errout bytes.Buffer
			args := []string{"export", path, "--format=csv", "--output=" + dest}
			if err := execute(t.Context(), args, &out, &errout, noScanner(t), nil); err == nil {
				t.Fatal("accepted incomplete/corrupt document")
			}
			if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
				t.Fatal("published failed export", err)
			}
			err := execute(t.Context(), append(args, "--allow-incomplete"), &out, &errout, noScanner(t), nil)
			if (err != nil) != tc.corrupt {
				t.Fatal(err)
			}
			if !tc.corrupt && !strings.Contains(errout.String(), "Partial results exported") {
				t.Fatal(errout.String())
			}
			if tc.corrupt {
				if _, err := os.Stat(dest); !errors.Is(err, os.ErrNotExist) {
					t.Fatal("published corruption", err)
				}
			}
			staged, _ := filepath.Glob(filepath.Join(filepath.Dir(dest), ".masscanr-export-*"))
			if len(staged) != 0 {
				t.Fatal(staged)
			}
		})
	}
}

func TestDocumentCommandHelpAndValidation(t *testing.T) {
	for _, args := range [][]string{{"inspect", "--help"}, {"export", "--help"}, {"help", "inspect"}, {"help", "export"}} {
		var out, errout bytes.Buffer
		if err := execute(t.Context(), args, &out, &errout, noScanner(t), nil); err != nil {
			t.Fatal(args, err)
		}
	}
	for _, args := range [][]string{{"inspect"}, {"inspect", "a", "b"}, {"export"}, {"export", "a"}, {"export", "a", "--format=xml", "--output=b"}, {"export", "a", "--format=csv", "--output=-"}} {
		var out, errout bytes.Buffer
		if err := execute(t.Context(), args, &out, &errout, noScanner(t), nil); err == nil {
			t.Fatal("accepted invalid args", args)
		}
	}
}
