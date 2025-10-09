package main

import (
	"context"
	"log/slog"
	"os"

	tlog "tangled.org/core/log"
	"tangled.org/core/spindle"
	_ "tangled.org/core/tid"
)

func main() {
	logger := tlog.New("spindl3")
	slog.SetDefault(logger)

	ctx := context.Background()
	ctx = tlog.IntoContext(ctx, logger)

	err := spindle.Run(ctx)
	if err != nil {
		logger.Error("error running spindle", "error", err)
		os.Exit(-1)
	}
}
