package main

import (
	"context"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/sotangled/tangled/knotserver"
	"github.com/sotangled/tangled/knotserver/config"
	"github.com/sotangled/tangled/knotserver/db"
)

func main() {
	ctx := context.Background()
	// ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// defer stop()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	c, err := config.Load(ctx)
	if err != nil {
		log.Fatal(err)
	}

	if c.Server.Dev {
		log.Println("running in dev mode, signature verification is disabled")
	}

	db, err := db.Setup(c.Server.DBPath)
	if err != nil {
		log.Fatalf("failed to setup db: %s", err)
	}

	mux, err := knotserver.Setup(ctx, c, db)
	if err != nil {
		log.Fatal(err)
	}

	addr := fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)

	log.Println("starting main server on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
