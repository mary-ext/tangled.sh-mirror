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

	c, err := appview.LoadConfig(context.Background())
	if err != nil {
		log.Println("failed to load config", "error", err)
		return
	}

	state, err := state.Make(c)

	if err != nil {
		log.Fatal(err)
	}

	log.Println("starting server on", c.Core.ListenAddr)
	log.Println(http.ListenAndServe(c.Core.ListenAddr, state.Router()))
}
