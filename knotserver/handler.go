package knotserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"github.com/sotangled/tangled/jetstream"
	"github.com/sotangled/tangled/knotserver/config"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/rbac"
)

const (
	ThisServer = "thisserver" // resource identifier for rbac enforcement
)

type Handle struct {
	c  *config.Config
	db *db.DB
	jc *jetstream.JetstreamClient
	e  *rbac.Enforcer
	l  *slog.Logger

	// init is a channel that is closed when the knot has been initailized
	// i.e. when the first user (knot owner) has been added.
	init            chan struct{}
	knotInitialized bool
}

func Setup(ctx context.Context, c *config.Config, db *db.DB, e *rbac.Enforcer, jc *jetstream.JetstreamClient, l *slog.Logger) (http.Handler, error) {
	r := chi.NewRouter()

	h := Handle{
		c:    c,
		db:   db,
		e:    e,
		l:    l,
		jc:   jc,
		init: make(chan struct{}),
	}

	err := e.AddDomain(ThisServer)
	if err != nil {
		return nil, fmt.Errorf("failed to setup enforcer: %w", err)
	}

	err = h.jc.StartJetstream(ctx, h.processMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to start jetstream: %w", err)
	}

	// Check if the knot knows about any Dids;
	// if it does, it is already initialized and we can repopulate the
	// Jetstream subscriptions.
	dids, err := db.GetAllDids()
	if err != nil {
		return nil, fmt.Errorf("failed to get all Dids: %w", err)
	}
	if len(dids) > 0 {
		h.knotInitialized = true
		close(h.init)
		h.jc.UpdateDids(dids)
	}

	r.Get("/", h.Index)
	r.Route("/{did}", func(r chi.Router) {
		// Repo routes
		r.Route("/{name}", func(r chi.Router) {
			r.Post("/collaborator/add", h.AddRepoCollaborator)

			r.Get("/", h.RepoIndex)
			r.Get("/info/refs", h.InfoRefs)
			r.Post("/git-upload-pack", h.UploadPack)

			r.Route("/tree/{ref}", func(r chi.Router) {
				r.Get("/", h.RepoIndex)
				r.Get("/*", h.RepoTree)
			})

			r.Route("/blob/{ref}", func(r chi.Router) {
				r.Get("/*", h.Blob)
			})

			r.Get("/log/{ref}", h.Log)
			r.Get("/archive/{file}", h.Archive)
			r.Get("/commit/{ref}", h.Diff)
			r.Get("/tags", h.Tags)
			r.Get("/branches", h.Branches)
		})
	})

	// Create a new repository.
	r.Route("/repo", func(r chi.Router) {
		r.Use(h.VerifySignature)
		r.Put("/new", h.NewRepo)
	})

	r.Route("/member", func(r chi.Router) {
		r.Use(h.VerifySignature)
		r.Put("/add", h.AddMember)
	})

	// Initialize the knot with an owner and public key.
	r.With(h.VerifySignature).Post("/init", h.Init)

	// Health check. Used for two-way verification with appview.
	r.With(h.VerifySignature).Get("/health", h.Health)

	// All public keys on the knot.
	r.Get("/keys", h.Keys)

	return r, nil
}
