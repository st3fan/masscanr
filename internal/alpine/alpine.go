// Package alpine defines the supported temporary-worker platform.
package alpine

import (
	"bufio"
	"fmt"
	"net/url"
	"strconv"
	"strings"
)

const (
	Series       = "3.24"
	MinimumPatch = 4
	Family       = "alpinelinux"
	Architecture = "x64"
)

// Release accepts stable patches from the supported series, starting at 3.24.4.
func Release(text string) (string, error) {
	value := strings.TrimSpace(text)
	parts := strings.Split(value, ".")
	if len(parts) == 3 && parts[0]+"."+parts[1] == Series {
		patch, err := strconv.Atoi(parts[2])
		if err == nil && patch >= MinimumPatch && strconv.Itoa(patch) == parts[2] {
			return value, nil
		}
	}
	return "", fmt.Errorf("unsupported Alpine release %q; require stable %s.%d or later %s patch", value, Series, MinimumPatch, Series)
}

// Repositories validates every active entry before returning a canonical file.
// Keep the image's signed-package mirrors and add a missing community repository
// from its main mirror. Never mix release series or enable testing repositories.
func Repositories(text string) (string, error) {
	var entries []string
	seen := make(map[string]bool)
	var main *url.URL
	community := false
	reader := bufio.NewScanner(strings.NewReader(text))
	line := 0
	for reader.Scan() {
		line++
		entry, _, _ := strings.Cut(reader.Text(), "#")
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}
		u, err := url.Parse(entry)
		if err != nil || len(strings.Fields(entry)) != 1 || (u.Scheme != "http" && u.Scheme != "https") || u.Hostname() == "" || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.RawPath != "" {
			return "", fmt.Errorf("APK repository line %d must be a credential-free HTTP(S) stable %s main/community URL", line, Series)
		}
		u.Path = strings.TrimRight(u.Path, "/")
		switch {
		case strings.HasSuffix(u.Path, "/v"+Series+"/main"):
			if main == nil {
				copy := *u
				main = &copy
			}
		case strings.HasSuffix(u.Path, "/v"+Series+"/community"):
			community = true
		default:
			return "", fmt.Errorf("APK repository line %d is outside stable v%s main/community", line, Series)
		}
		entry = u.String()
		if !seen[entry] {
			entries = append(entries, entry)
			seen[entry] = true
		}
	}
	if err := reader.Err(); err != nil {
		return "", fmt.Errorf("read APK repositories: %w", err)
	}
	if main == nil {
		return "", fmt.Errorf("stable Alpine v%s main repository is required", Series)
	}
	if !community {
		main.Path = strings.TrimSuffix(main.Path, "/main") + "/community"
		entries = append(entries, main.String())
	}
	return strings.Join(entries, "\n") + "\n", nil
}
