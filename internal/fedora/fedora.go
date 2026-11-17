// Package fedora defines the supported DigitalOcean worker image.
package fedora

import (
	"bufio"
	"fmt"
	"strings"
)

const (
	Platform = "fedora44"
	Release  = "44"
	Image    = "fedora-44-x64"
)

// CheckRelease rejects a different distribution or Fedora release before
// installing scanner packages. The image is selected by its public DO slug.
func CheckRelease(text string) error {
	values := make(map[string]string)
	scanner := bufio.NewScanner(strings.NewReader(text))
	for scanner.Scan() {
		key, value, ok := strings.Cut(scanner.Text(), "=")
		if !ok || (key != "ID" && key != "VERSION_ID") {
			continue
		}
		values[key] = strings.Trim(strings.TrimSpace(value), `"'`)
	}
	if err := scanner.Err(); err != nil {
		return fmt.Errorf("read Fedora release: %w", err)
	}
	if values["ID"] != "fedora" || values["VERSION_ID"] != Release {
		return fmt.Errorf("unsupported worker release; require Fedora %s", Release)
	}
	return nil
}
