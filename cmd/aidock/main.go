package main

import (
	"context"
	"os"
	"os/signal"

	"github.com/pzqf/AIDock/internal/cli"
	"github.com/pzqf/AIDock/internal/isolation"
	"github.com/pzqf/AIDock/internal/mcprelay"
)

func main() {
	if handled, code := mcprelay.Helper(os.Args[1:], os.Stdin, os.Stdout); handled {
		os.Exit(code)
	}
	if handled, code := isolation.Helper(os.Args[1:]); handled {
		os.Exit(code)
	}
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	os.Exit(cli.Run(ctx, os.Args[1:], os.Stdout, os.Stderr))
}
