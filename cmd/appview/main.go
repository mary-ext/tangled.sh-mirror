package main

import (
	"context"
	"log"
	"log/slog"
	"net/http"
	"os"

	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/state"
)

func main() {
	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	ctx := context.Background()

	c, err := appview.LoadConfig(ctx)
	if err != nil {
		log.Println("failed to load config", "error", err)
		return
	}

	state, err := state.Make(ctx, c)

	if err != nil {
		log.Fatal(err)
	}

	log.Println("starting server on", c.ListenAddr)
	log.Println(http.ListenAndServe(c.ListenAddr, state.Router()))
}
