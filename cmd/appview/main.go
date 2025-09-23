package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"

	"tangled.org/core/appview/config"
	"tangled.org/core/appview/state"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	ctx := context.Background()

	c, err := config.LoadConfig(ctx)
	if err != nil {
		log.Println("failed to load config", "error", err)
		return
	}

	state, err := state.Make(ctx, c)
	defer func() {
		log.Println(state.Close())
	}()

	if err != nil {
		log.Fatal(err)
	}

	log.Println("starting server on", c.Core.ListenAddr)
	log.Println(http.ListenAndServe(c.Core.ListenAddr, state.Router()))
}
