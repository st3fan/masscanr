// Packaged masscan progress and record decoding.
package masscan

import (
	"math"
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
	if e1 != nil || e2 != nil || e3 != nil || percent > 100 || math.IsInf(rate*1000, 0) {
		return scan.Progress{}, false
	}
	p := scan.Progress{Rate: rate * 1000, Percent: percent, Found: found, Waiting: m[6] != ""}
	parts, units := m[3:6], []uint64{3600, 60, 1}
	if p.Waiting {
		parts, units = m[6:7], []uint64{1}
	}
	var seconds uint64
	const limit = uint64(math.MaxInt64 / int64(time.Second))
	for i, part := range parts {
		n, err := strconv.ParseUint(part, 10, 64)
		if err != nil || n > (limit-seconds)/units[i] {
			p.ETAUnknown = true
			return p, true
		}
		seconds += n * units[i]
	}
	p.ETA = time.Duration(seconds) * time.Second
	return p, true
}
