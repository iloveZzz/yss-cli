package main

import (
	"context"
	"github.com/iloveZzz/yss-cli/internal/cli"
	"github.com/iloveZzz/yss-cli/internal/compat"
	"os"
	"os/signal"
	"path/filepath"
	"strings"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), cancellationSignals()...)
	defer cancel()
	args := os.Args[1:]
	alias := strings.TrimSuffix(filepath.Base(os.Args[0]), ".exe")
	if strings.HasPrefix(alias, "create-yss-") {
		os.Exit(compat.Run(ctx, alias, args, os.Stdout, os.Stderr))
	}
	if len(args) > 1 && args[0] == "compat" {
		os.Exit(compat.Run(ctx, args[1], args[2:], os.Stdout, os.Stderr))
	}
	if len(args) == 2 && args[0] == "compat-api" {
		os.Exit(compat.RunAPI(ctx, args[1], os.Stdin, os.Stdout, os.Stderr))
	}
	os.Exit(cli.Run(ctx, args, os.Stdout, os.Stderr))
}
