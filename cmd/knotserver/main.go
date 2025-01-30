package main

import (
	"flag"
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/icyphox/bild/config"
	"github.com/icyphox/bild/db"
	"github.com/icyphox/bild/knotserver"
)

func main() {
	var cfg string
	flag.StringVar(&cfg, "config", "./config.yaml", "path to config file")
	flag.Parse()

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	c, err := config.Read(cfg)
	if err != nil {
		log.Fatal(err)
	}
	db, err := db.Setup(c.Server.DBPath)
	if err != nil {
		log.Fatalf("failed to setup db: %s", err)
	}

	mux, err := knotserver.Setup(c, db)
	if err != nil {
		log.Fatal(err)
	}

	addr := fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)

	log.Println("starting main server on", addr)
	log.Fatal(http.ListenAndServe(addr, mux))
}
