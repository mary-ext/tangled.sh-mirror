package main

import (
	"context"
	"log/slog"
	"os"

	tlog "tangled.org/core/log"
	"tangled.org/core/spindle"
)

func main() {
	logger := tlog.New("spindle")
	slog.SetDefault(logger)

	ctx := context.Background()
	ctx = tlog.IntoContext(ctx, logger)

	err := spindle.Run(ctx)
	if err != nil {
		logger.Error("error running spindle", "error", err)
		os.Exit(-1)
	}
}
