package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/pzqf/AIDock/internal/cli"
	"github.com/pzqf/AIDock/internal/isolation"
)

func main() {
	if handled, code := isolation.Helper(os.Args[1:]); handled {
		os.Exit(code)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
