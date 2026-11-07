// Packaged masscan progress and record decoding.
package masscan

import (
	"regexp"
	"strconv"
	"time"

	"github.com/st3fan/masscanr/internal/scan"
)

var statusPattern = regexp.MustCompile(`rate:\s*([0-9.]+)-kpps,\s*([0-9.]+)% done,\s*(?:(\d+):(\d+):(\d+) remaining|waiting (\d+)-secs),\s*found=(\d+)`)

// ParseProgress matches packaged masscan 1.3.2 stderr, verified on Alpine/musl;
// no dependency on the newer upstream --json-status feature.
func ParseProgress(line string) (scan.Progress, bool) {
	m := statusPattern.FindStringSubmatch(line)
	if m == nil {
		return scan.Progress{}, false
	}
	rate, e1 := strconv.ParseFloat(m[1], 64)
	percent, e2 := strconv.ParseFloat(m[2], 64)
	found, e3 := strconv.ParseUint(m[7], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || percent > 100 {
		return scan.Progress{}, false
	}
	p := scan.Progress{Rate: rate * 1000, Percent: percent, Found: found, Waiting: m[6] != ""}
	if p.Waiting {
		n, _ := strconv.Atoi(m[6])
		p.ETA = time.Duration(n) * time.Second
	} else {
		h, _ := strconv.Atoi(m[3])
		min, _ := strconv.Atoi(m[4])
		sec, _ := strconv.Atoi(m[5])
		p.ETA = time.Duration(h)*time.Hour + time.Duration(min)*time.Minute + time.Duration(sec)*time.Second
	}
	return p, true
}
