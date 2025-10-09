package main

import (
	"context"
	"log/slog"
	"os"

	"github.com/urfave/cli/v3"
	"tangled.org/core/guard"
	"tangled.org/core/hook"
	"tangled.org/core/keyfetch"
	"tangled.org/core/knotserver"
	tlog "tangled.org/core/log"
)

func main() {
	cmd := &cli.Command{
		Name:  "knot",
		Usage: "knot administration and operation tool",
		Commands: []*cli.Command{
			guard.Command(),
			knotserver.Command(),
			keyfetch.Command(),
			hook.Command(),
		},
	}

	logger := tlog.New("knot")
	slog.SetDefault(logger)

	ctx := context.Background()
	ctx = tlog.IntoContext(ctx, logger)

	if err := cmd.Run(ctx, os.Args); err != nil {
		logger.Error(err.Error())
		os.Exit(-1)
	}
}
