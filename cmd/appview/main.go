package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/sotangled/tangled/appview"
	"github.com/sotangled/tangled/appview/state"
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

	addr := fmt.Sprintf("%s:%s", c.Hostname, c.Port)

	log.Println("starting server on", addr)
	log.Println(http.ListenAndServe(addr, state.Router()))
}
