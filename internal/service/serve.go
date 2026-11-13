package service

import (
	"context"
	"flag"
	"fmt"
	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"io"
	"net"
	"net/http"
	"os"
	"time"
)

func Serve(ctx context.Context, args []string, out io.Writer) error {
	fs := flag.NewFlagSet("masscanr serve", flag.ContinueOnError)
	fs.SetOutput(out)
	listen := fs.String("listen", "127.0.0.1:8080", "HTTP listen address")
	dir := fs.String("data-dir", "masscanr-data", "artifact directory")
	if e := fs.Parse(args); e != nil {
		if e == flag.ErrHelp {
			return nil
		}
		return e
	}
	if fs.NArg() != 0 {
		return fmt.Errorf("unexpected arguments")
	}
	url := os.Getenv("DBOS_SYSTEM_DATABASE_URL")
	if url == "" {
		return fmt.Errorf("DBOS_SYSTEM_DATABASE_URL is required")
	}
	s, e := Open(ctx, url)
	if e != nil {
		return fmt.Errorf("database initialization failed")
	}
	defer s.Pool.Close()
	if e = os.MkdirAll(*dir, 0700); e != nil {
		return e
	}
	dc, e := s.WorkflowContext(context.Background())
	if e != nil {
		return fmt.Errorf("workflow initialization failed")
	}
	if e = dbos.Launch(dc); e != nil {
		return fmt.Errorf("workflow launch failed")
	}
	defer dbos.Shutdown(dc, 30*time.Second)
	api := &API{Store: s}
	srv := &http.Server{Handler: api.Handler(), ReadHeaderTimeout: 10 * time.Second, IdleTimeout: 60 * time.Second}
	ln, e := net.Listen("tcp", *listen)
	if e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() { done <- srv.Serve(ln) }()
	fmt.Fprintln(out, "Listening on", ln.Addr())
	select {
	case e = <-done:
	case <-ctx.Done():
		stop, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		e = srv.Shutdown(stop)
	}
	if e == http.ErrServerClosed {
		return nil
	}
	return e
}
