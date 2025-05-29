package main

import (
	"context"
	"os"

	"github.com/urfave/cli/v3"
	"tangled.sh/tangled.sh/core/guard"
	"tangled.sh/tangled.sh/core/hook"
	"tangled.sh/tangled.sh/core/keyfetch"
	"tangled.sh/tangled.sh/core/knotserver"
	"tangled.sh/tangled.sh/core/log"
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

	ctx := context.Background()
	logger := log.New("knot")
	ctx = log.IntoContext(ctx, logger.With("command", cmd.Name))

	if err := cmd.Run(ctx, os.Args); err != nil {
		logger.Error(err.Error())
		os.Exit(-1)
	}
}
