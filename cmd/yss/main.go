package main

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/cli"
	"os"
	"os/signal"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), cancellationSignals()...)
	defer cancel()
	args := os.Args[1:]
	os.Exit(cli.Run(ctx, args, os.Stdout, os.Stderr))
}
