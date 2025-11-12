package main

import (
	"context"
	"net/http"
	"os"

	"tangled.org/core/appview/config"
	"tangled.org/core/appview/state"
	"tangled.org/core/appview/web"
	tlog "tangled.org/core/log"
)

func main() {
	ctx := context.Background()
	logger := tlog.New("appview")
	ctx = tlog.IntoContext(ctx, logger)

	c, err := config.LoadConfig(ctx)
	if err != nil {
		logger.Error("failed to load config", "error", err)
		return
	}

	state, err := state.Make(ctx, c)
	defer func() {
		if err := state.Close(); err != nil {
			logger.Error("failed to close state", "err", err)
		}
	}()

	if err != nil {
		logger.Error("failed to start appview", "err", err)
		os.Exit(-1)
	}

	logger.Info("starting server", "address", c.Core.ListenAddr)

	if err := http.ListenAndServe(c.Core.ListenAddr, web.RouterFromState(state)); err != nil {
		logger.Error("failed to start appview", "err", err)
	}
}
