package main

import (
	"context"
	"fmt"
	"os"
	"os/signal"

	"github.com/CyrusSE/agenthop/internal/cli"
)

func main() {
	app, err := cli.NewApp()
	if err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
	defer app.Index.Close()

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()

	root := app.Root()
	root.SetContext(ctx)
	if err := root.ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}
