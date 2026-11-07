package alpine

import (
	"strings"
	"testing"
)

func TestSupportedRelease(t *testing.T) {
	for _, value := range []string{"3.24.4", "3.24.5\n", "3.24.42\r\n"} {
		got, err := Release(value)
		if err != nil || got != strings.TrimSpace(value) {
			t.Fatal(value, got, err)
		}
	}
	for _, value := range []string{"", "3.24.3", "3.25.4", "3.23.9", "4.24.4", "3.24", "3.24.04", "3.24.+4", "3.24.4_rc1", "3.24.4.1", "3.24.4\n3.25.0", "3.24.999999999999999999999999"} {
		if _, err := Release(value); err == nil {
			t.Fatalf("accepted unsupported release %q", value)
		}
	}
}

func TestRepositoriesKeepMirrorAndAddCommunity(t *testing.T) {
	const main = "https://mirror.example/alpine/v3.24/main"
	got, err := Repositories("# package mirror\n" + main + "/ # stable\n" + main + "\n")
	if err != nil || got != main+"\nhttps://mirror.example/alpine/v3.24/community\n" {
		t.Fatal(got, err)
	}
	if again, err := Repositories(got); err != nil || again != got {
		t.Fatal("repository normalization is not idempotent", again, err)
	}
}

func TestRepositoriesRejectUnexpectedSourcesWithoutExposingCredentials(t *testing.T) {
	const main = "https://mirror.example/alpine/v3.24/main\n"
	for _, entry := range []string{"https://mirror.example/alpine/edge/community", "https://mirror.example/alpine/v3.23/main", "https://mirror.example/alpine/v3.24/testing", "@testing https://mirror.example/alpine/v3.24/main", "file:///tmp/main", "/tmp/main", "ftp://mirror.example/alpine/v3.24/main", "https://user:private-password@mirror.example/alpine/v3.24/main", "https://mirror.example/alpine/v3.24/main?token=private-password", "https://mirror.example/alpine/v3.24%2fmain"} {
		_, err := Repositories(main + entry)
		if err == nil || strings.Contains(err.Error(), "private-password") {
			t.Fatalf("invalid repository accepted or credential exposed: %v", err)
		}
	}
	for _, text := range []string{"", "# disabled\n", "https://mirror.example/alpine/v3.24/community"} {
		if _, err := Repositories(text); err == nil {
			t.Fatal("accepted missing main repository")
		}
	}
}
