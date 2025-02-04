package knotserver

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"
	tangled "github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/knotserver/config"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/knotserver/jsclient"
	"github.com/sotangled/tangled/rbac"
)

const (
	ThisServer = "thisserver" // resource identifier for rbac enforcement
)

type Handle struct {
	c  *config.Config
	db *db.DB
	js *jsclient.JetstreamClient
	e  *rbac.Enforcer

	// init is a channel that is closed when the knot has been initailized
	// i.e. when the first user (knot owner) has been added.
	init            chan struct{}
	knotInitialized bool
}

func Setup(ctx context.Context, c *config.Config, db *db.DB, e *rbac.Enforcer) (http.Handler, error) {
	r := chi.NewRouter()

	h := Handle{
		c:    c,
		db:   db,
		e:    e,
		init: make(chan struct{}),
	}

	err := e.AddDomain(ThisServer)
	if err != nil {
		return nil, fmt.Errorf("failed to setup enforcer: %w", err)
	}

	err = h.StartJetstream(ctx)
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
		h.js.UpdateDids(dids)
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

	// Create a new repository.
	r.Route("/repo", func(r chi.Router) {
		r.Use(h.VerifySignature)
		r.Put("/new", h.NewRepo)
	})

	// Initialize the knot with an owner and public key.
	r.With(h.VerifySignature).Post("/init", h.Init)

	// Health check. Used for two-way verification with appview.
	r.With(h.VerifySignature).Get("/health", h.Health)

	// All public keys on the knot.
	r.Get("/keys", h.Keys)

	return r, nil
}

func (h *Handle) StartJetstream(ctx context.Context) error {
	collections := []string{tangled.PublicKeyNSID, tangled.KnotMemberNSID}
	dids := []string{}

	var lastTimeUs int64
	var err error
	lastTimeUs, err = h.db.GetLastTimeUs()
	if err != nil {
		log.Println("couldn't get last time us, starting from now")
		lastTimeUs = time.Now().UnixMicro()
	}
	// If last time is older than a week, start from now
	if time.Now().UnixMicro()-lastTimeUs > 7*24*60*60*1000*1000 {
		lastTimeUs = time.Now().UnixMicro()
		log.Printf("last time us is older than a week. discarding that and starting from now.")
		err = h.db.SaveLastTimeUs(lastTimeUs)
		if err != nil {
			log.Println("failed to save last time us")
		}
	}

	log.Printf("found last time_us %d", lastTimeUs)

	h.js = jsclient.NewJetstreamClient(collections, dids)
	messages, err := h.js.ReadJetstream(ctx, lastTimeUs)
	if err != nil {
		return fmt.Errorf("failed to read from jetstream: %w", err)
	}

	go func() {
		log.Println("waiting for knot to be initialized")
		<-h.init
		log.Println("initalized jetstream watcher")

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
					did := data["did"].(string)
					record := commit["record"].(map[string]interface{})
					if err := h.db.AddPublicKeyFromRecord(did, record); err != nil {
						log.Printf("failed to add public key: %v", err)
					} else {
						log.Printf("added public key from firehose: %s", data["did"])
					}
				case tangled.KnotMemberNSID:
					did := data["did"].(string)
					record := commit["record"].(map[string]interface{})
					ok, err := h.e.E.Enforce(did, ThisServer, ThisServer, "server:invite")
					if err != nil || !ok {
						log.Printf("failed to add member from did %s", did)
					} else {
						log.Printf("adding member")
						if err := h.e.AddMember(ThisServer, record["member"].(string)); err != nil {
							log.Printf("failed to add member: %v", err)
						} else {
							log.Printf("added member from firehose: %s", record["member"])
						}
					}
				default:
				}

				lastTimeUs := int64(data["time_us"].(float64))
				if err := h.db.SaveLastTimeUs(lastTimeUs); err != nil {
					log.Printf("failed to save last time us: %v", err)
				}
			}

		}
	}()

	return nil
}
