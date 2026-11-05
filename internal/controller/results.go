package controller

import (
	"bytes"
	"fmt"
	"io"
	"net/netip"
	"slices"
	"strconv"
	"strings"
	"sync"

	"github.com/st3fan/masscanr/internal/scan"
)

type results struct {
	mu    sync.Mutex
	out   io.Writer
	cfg   scan.Config
	count uint64
}

func (r *results) line(line string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	return writeString(r.out, line+"\n")
}

func writeString(w io.Writer, s string) error {
	n, err := io.WriteString(w, s)
	if err == nil && n != len(s) {
		err = io.ErrShortWrite
	}
	return err
}

type shardWriter struct {
	target  *results
	shard   int
	pending []byte
}

func (w *shardWriter) Write(p []byte) (int, error) {
	n := len(p)
	for len(p) > 0 {
		i := bytes.IndexByte(p, '\n')
		if i < 0 {
			if len(w.pending)+len(p) > 1<<20 {
				return 0, fmt.Errorf("oversized masscan result")
			}
			w.pending = append(w.pending, p...)
			return n, nil
		}
		if len(w.pending)+i > 1<<20 {
			return 0, fmt.Errorf("oversized masscan result")
		}
		w.pending = append(w.pending, p[:i]...)
		if err := w.record(string(w.pending)); err != nil {
			return 0, err
		}
		w.pending = w.pending[:0]
		p = p[i+1:]
	}
	return n, nil
}

func (w *shardWriter) finish() error {
	if len(w.pending) == 0 {
		return nil
	}
	err := w.record(string(w.pending))
	w.pending = nil
	return err
}

func (w *shardWriter) record(line string) error {
	line = strings.TrimSpace(line)
	if line == "" || strings.HasPrefix(line, "#") {
		return nil
	}
	fields := strings.Fields(line)
	if len(fields) != 5 || fields[0] != "open" || fields[1] != "tcp" {
		return fmt.Errorf("invalid masscan list record from shard %d", w.shard)
	}
	port, err := strconv.ParseUint(fields[2], 10, 16)
	if err != nil || !slices.Contains(w.target.cfg.Ports, uint16(port)) {
		return fmt.Errorf("unexpected result port from shard %d", w.shard)
	}
	ip, err := netip.ParseAddr(fields[3])
	if err != nil || !ip.Is4() {
		return fmt.Errorf("invalid result address from shard %d", w.shard)
	}
	inScope := false
	for _, target := range w.target.cfg.Targets {
		if target.Contains(ip) {
			inScope = true
			break
		}
	}
	if !inScope {
		return fmt.Errorf("out-of-scope result from shard %d", w.shard)
	}
	if _, err := strconv.ParseUint(fields[4], 10, 64); err != nil {
		return fmt.Errorf("invalid result timestamp from shard %d", w.shard)
	}
	r := w.target
	r.mu.Lock()
	defer r.mu.Unlock()
	if err := writeString(r.out, fmt.Sprintf("# shard %d\n%s\n", w.shard, strings.Join(fields, " "))); err != nil {
		return err
	}
	r.count++
	return nil
}
