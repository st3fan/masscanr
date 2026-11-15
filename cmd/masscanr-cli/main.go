package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/st3fan/masscanr/internal/watch"
)

func main() {
	if len(os.Args) < 2 || os.Args[1] != "watch" {
		fmt.Fprintln(os.Stderr, "Usage: masscanr-cli watch <run-id> | --all [--server URL] [--interval 2s] [--plain]")
		os.Exit(2)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM, syscall.SIGHUP)
	defer stop()
	if err := watch.Run(ctx, os.Args[2:], os.Stdin, os.Stdout); err != nil {
		fmt.Fprintln(os.Stderr, "masscanr-cli:", err)
		os.Exit(1)
	}
}
