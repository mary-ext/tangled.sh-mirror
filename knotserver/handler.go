package knotserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	tangled "github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/knotserver/config"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/knotserver/jsclient"
)

type Handle struct {
	c  *config.Config
	db *db.DB
	js *jsclient.JetstreamClient
}

func Setup(ctx context.Context, c *config.Config, db *db.DB) (http.Handler, error) {
	r := chi.NewRouter()

	h := Handle{
		c:  c,
		db: db,
	}

	err := h.StartJetstream(ctx)
	if err != nil {
		return nil, fmt.Errorf("failed to start jetstream: %w", err)
	}

	r.Get("/", h.Index)
	r.Route("/{did}", func(r chi.Router) {
		// Repo routes
		r.Route("/{name}", func(r chi.Router) {
			r.Get("/", h.RepoIndex)
			r.Get("/info/refs", h.InfoRefs)
			r.Post("/git-upload-pack", h.UploadPack)

			r.Route("/tree/{ref}", func(r chi.Router) {
				r.Get("/*", h.RepoTree)
			})

			r.Route("/blob/{ref}", func(r chi.Router) {
				r.Get("/*", h.FileContent)
			})

			r.Get("/log/{ref}", h.Log)
			r.Get("/archive/{file}", h.Archive)
			r.Get("/commit/{ref}", h.Diff)
			r.Get("/refs/", h.Refs)
		})
	})

	// Create a new repository
	r.Route("/repo", func(r chi.Router) {
		r.Use(h.VerifySignature)
		r.Put("/new", h.NewRepo)
	})

	// Add a new user to the knot
	// r.With(h.VerifySignature).Put("/user", h.AddUser)

	// Health check. Used for two-way verification with appview.
	r.With(h.VerifySignature).Get("/health", h.Health)

	// All public keys on the knot
	r.Get("/keys", h.Keys)

	return r, nil
}

func (h *Handle) StartJetstream(ctx context.Context) error {
	colections := []string{tangled.PublicKeyNSID}
	dids := []string{}

	h.js = jsclient.NewJetstreamClient(colections, dids)
	messages, err := h.js.ReadJetstream(ctx)
	if err != nil {
		return fmt.Errorf("failed to read from jetstream: %w", err)
	}

	go func() {
		for msg := range messages {
			var data map[string]interface{}
			if err := json.Unmarshal(msg, &data); err != nil {
				log.Printf("error unmarshaling message: %v", err)
				continue
			}

			if kind, ok := data["kind"].(string); ok && kind == "commit" {
				commit := data["commit"].(map[string]interface{})

				switch commit["collection"].(string) {
				case tangled.PublicKeyNSID:
					record := commit["record"].(map[string]interface{})
					if err := h.db.AddPublicKeyFromRecord(record); err != nil {
						log.Printf("failed to add public key: %v", err)
					}
					log.Printf("added public key from firehose: %s", data["did"])
				default:
				}
			}

		}
	}()

	return nil
}
