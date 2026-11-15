package service

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"io"
	"net"
	"os"
	"time"

	"github.com/dbos-inc/dbos-transact-golang/dbos"
	"github.com/st3fan/masscanr/internal/cloud/vultr"
	"github.com/st3fan/masscanr/internal/webui"

	"github.com/gofiber/fiber/v3"
)

func Serve(ctx context.Context, args []string, out io.Writer) (result error) {
	fs := flag.NewFlagSet("masscanr-server", flag.ContinueOnError)
	fs.SetOutput(out)
	listen := fs.String("listen", "127.0.0.1:18080", "HTTP listen address")
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
	lock, e := s.Pool.Acquire(ctx)
	if e != nil {
		return e
	}
	defer lock.Release()
	var acquired bool
	if e = lock.QueryRow(ctx, "SELECT pg_try_advisory_lock(746592017)").Scan(&acquired); e != nil || !acquired {
		return fmt.Errorf("another service owns this database")
	}
	defer lock.Exec(context.Background(), "SELECT pg_advisory_unlock(746592017)")
	if e = os.MkdirAll(*dir, 0700); e != nil {
		return e
	}
	dc, e := s.WorkflowContext(context.Background())
	if e != nil {
		return fmt.Errorf("workflow initialization failed")
	}
	manager := NewManager(s, dc, *dir)
	recovery, e := manager.PrepareRecovery(ctx)
	if e != nil {
		return fmt.Errorf("recovery preparation failed")
	}
	if e = dbos.Launch(dc); e != nil {
		return fmt.Errorf("workflow launch failed")
	}
	defer dbos.Shutdown(dc, 30*time.Second)
	defer func() {
		cleanup, cancel := context.WithTimeout(context.Background(), 30*time.Minute)
		defer cancel()
		result = errors.Join(result, manager.Shutdown(cleanup))
	}()
	if e = manager.ResumeReserved(recovery); e != nil {
		return fmt.Errorf("workflow recovery failed")
	}
	api := &API{Store: s, Catalog: vultr.New(""), Base: ctx, Ready: func() bool { manager.mu.Lock(); defer manager.mu.Unlock(); return !manager.closing }}
	api.Extra = func(app *fiber.App) { manager.Routes(api, app) }
	srv := api.Handler()
	webui.Register(srv, webui.Assets())
	ln, e := net.Listen("tcp", *listen)
	if e != nil {
		return e
	}
	done := make(chan error, 1)
	go func() { done <- srv.Listener(ln) }()
	fmt.Fprintln(out, "Listening on", ln.Addr())
	select {
	case e = <-done:
	case <-ctx.Done():
		manager.mu.Lock()
		manager.closing = true
		manager.mu.Unlock()
		stop, c := context.WithTimeout(context.Background(), 30*time.Second)
		defer c()
		e = srv.ShutdownWithContext(stop)
	}
	return e
}
