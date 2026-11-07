package controller

import (
	"io"
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
