// Package remote prepares workers and executes masscan through SSH.
package remote

import (
	"bufio"
	"bytes"
	"io"
	"regexp"
	"strconv"
	"time"
)

type Progress struct {
	Percent float64
	Rate    float64 // packets per second
	ETA     time.Duration
	Found   uint64
	Waiting bool
}

var statusPattern = regexp.MustCompile(`rate:\s*([0-9.]+)-kpps,\s*([0-9.]+)% done,\s*(?:(\d+):(\d+):(\d+) remaining|waiting (\d+)-secs),\s*found=(\d+)`)

// ParseProgress matches packaged masscan 1.3.2 stderr, verified on Alpine/musl;
// no dependency on the newer upstream --json-status feature.
func ParseProgress(line string) (Progress, bool) {
	m := statusPattern.FindStringSubmatch(line)
	if m == nil {
		return Progress{}, false
	}
	rate, e1 := strconv.ParseFloat(m[1], 64)
	percent, e2 := strconv.ParseFloat(m[2], 64)
	found, e3 := strconv.ParseUint(m[7], 10, 64)
	if e1 != nil || e2 != nil || e3 != nil || percent > 100 {
		return Progress{}, false
	}
	p := Progress{Rate: rate * 1000, Percent: percent, Found: found, Waiting: m[6] != ""}
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

// Records splits both CR status updates and LF diagnostics, including the last
// record at EOF. Scanner's token limit bounds untrusted remote output.
func Records(reader io.Reader, visit func(string)) error {
	s := bufio.NewScanner(reader)
	s.Buffer(make([]byte, 4096), 1<<20)
	s.Split(func(data []byte, atEOF bool) (int, []byte, error) {
		if i := bytes.IndexAny(data, "\r\n"); i >= 0 {
			return i + 1, data[:i], nil
		}
		if atEOF && len(data) > 0 {
			return len(data), data, nil
		}
		return 0, nil, nil
	})
	for s.Scan() {
		if line := s.Text(); line != "" {
			visit(line)
		}
	}
	return s.Err()
}
