package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/st3fan/masscanr/internal/cli"
	"github.com/st3fan/masscanr/internal/service"
	"github.com/st3fan/masscanr/internal/watch"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	var err error
	if len(os.Args) > 1 && os.Args[1] == "serve" {
		err = service.Serve(ctx, os.Args[2:], os.Stderr)
	} else if len(os.Args) > 1 && os.Args[1] == "watch" {
		err = watch.Run(ctx, os.Args[2:], os.Stdin, os.Stdout)
	} else {
		err = cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr)
	}
	if err != nil {
		fmt.Fprintln(os.Stderr, "masscanr:", err)
		os.Exit(1)
	}
}
