package main

import (
	"fmt"
	"log"
	"log/slog"
	"net/http"
	"os"

	"github.com/icyphox/bild/appview/state"
)

func main() {

	slog.SetDefault(slog.New(slog.NewTextHandler(os.Stdout, nil)))

	state, err := state.Make()

	if err != nil {
		log.Fatal(err)
	}

	addr := fmt.Sprintf("%s:%d", "localhost", 3000)

	log.Println("starting server on", addr)
	log.Println(http.ListenAndServe(addr, state.Router()))
}
