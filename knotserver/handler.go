package knotserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/jetstream"
	"tangled.sh/tangled.sh/core/knotserver/config"
	"tangled.sh/tangled.sh/core/knotserver/db"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/resolver"
)

const (
	ThisServer = "thisserver" // resource identifier for rbac enforcement
)

type Handle struct {
	c     *config.Config
	db    *db.DB
	jc    *jetstream.JetstreamClient
	e     *rbac.Enforcer
	l     *slog.Logger
	clock syntax.TIDClock
}

func Setup(ctx context.Context, c *config.Config, db *db.DB, e *rbac.Enforcer, jc *jetstream.JetstreamClient, l *slog.Logger) (http.Handler, error) {
	h := Handle{
		c:  c,
		db: db,
		e:  e,
		l:  l,
		jc: jc,
	}

	err := e.AddDomain(ThisServer)
	if err != nil {
		return nil, fmt.Errorf("failed to setup enforcer: %w", err)
	}

	// if this knot does not already have an owner, publish it
	if _, err := h.db.Owner(); err != nil {
		l.Info("publishing this knot ...", "owner", h.c.Owner.Did)
		err = h.Publish()
		if err != nil {
			return nil, fmt.Errorf("failed to announce knot: %w", err)
		}
	}

	l.Info("this knot has been published", "owner", h.c.Owner.Did)

	err = h.jc.StartJetstream(ctx, h.processMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to start jetstream: %w", err)
	}

	dids, err := db.GetAllDids()
	if err != nil {
		return nil, fmt.Errorf("failed to get all Dids: %w", err)
	}

	for _, d := range dids {
		h.jc.AddDid(d)
	}

	r := chi.NewRouter()

	r.Get("/", h.Index)
	r.Get("/capabilities", h.Capabilities)
	r.Get("/version", h.Version)
	r.Route("/{did}", func(r chi.Router) {
		// Repo routes
		r.Route("/{name}", func(r chi.Router) {
			r.Route("/collaborator", func(r chi.Router) {
				r.Use(h.VerifySignature)
				r.Post("/add", h.AddRepoCollaborator)
			})

			r.Route("/languages", func(r chi.Router) {
				r.With(h.VerifySignature)
				r.Get("/", h.RepoLanguages)
				r.Get("/{ref}", h.RepoLanguages)
			})

			r.Get("/", h.RepoIndex)
			r.Get("/info/refs", h.InfoRefs)
			r.Post("/git-upload-pack", h.UploadPack)
			r.Post("/git-receive-pack", h.ReceivePack)
			r.Get("/compare/{rev1}/{rev2}", h.Compare) // git diff-tree compare of two objects

			r.With(h.VerifySignature).Post("/hidden-ref/{forkRef}/{remoteRef}", h.NewHiddenRef)

			r.Route("/merge", func(r chi.Router) {
				r.With(h.VerifySignature)
				r.Post("/", h.Merge)
				r.Post("/check", h.MergeCheck)
			})

			r.Route("/tree/{ref}", func(r chi.Router) {
				r.Get("/", h.RepoIndex)
				r.Get("/*", h.RepoTree)
			})

			r.Route("/blob/{ref}", func(r chi.Router) {
				r.Get("/*", h.Blob)
			})

			r.Route("/raw/{ref}", func(r chi.Router) {
				r.Get("/*", h.BlobRaw)
			})

			r.Get("/log/{ref}", h.Log)
			r.Get("/archive/{file}", h.Archive)
			r.Get("/commit/{ref}", h.Diff)
			r.Get("/tags", h.Tags)
			r.Route("/branches", func(r chi.Router) {
				r.Get("/", h.Branches)
				r.Get("/{branch}", h.Branch)
				r.Route("/default", func(r chi.Router) {
					r.Get("/", h.DefaultBranch)
					r.With(h.VerifySignature).Put("/", h.SetDefaultBranch)
				})
			})
		})
	})

	// Create a new repository.
	r.Route("/repo", func(r chi.Router) {
		r.Use(h.VerifySignature)
		r.Put("/new", h.NewRepo)
		r.Delete("/", h.RemoveRepo)
		r.Route("/fork", func(r chi.Router) {
			r.Post("/", h.RepoFork)
			r.Post("/sync/{branch}", h.RepoForkSync)
			r.Get("/sync/{branch}", h.RepoForkAheadBehind)
		})
	})

	r.Route("/member", func(r chi.Router) {
		r.Use(h.VerifySignature)
		r.Put("/add", h.AddMember)
	})

	// Initialize the knot with an owner and public key.
	r.With(h.VerifySignature).Post("/init", h.Init)

	// Health check. Used for two-way verification with appview.
	r.With(h.VerifySignature).Get("/health", h.Health)

	// Return did of the owner of this knot
	r.Get("/owner", h.Owner)

	// All public keys on the knot.
	r.Get("/keys", h.Keys)

	return r, nil
}

// version is set during build time.
var version string

func (h *Handle) Version(w http.ResponseWriter, r *http.Request) {
	if version == "" {
		info, ok := debug.ReadBuildInfo()
		if !ok {
			http.Error(w, "failed to read build info", http.StatusInternalServerError)
			return
		}

		var modVer string
		for _, mod := range info.Deps {
			if mod.Path == "tangled.sh/tangled.sh/knotserver" {
				version = mod.Version
				break
			}
		}

		if modVer == "" {
			version = "unknown"
		}
	}

	w.Header().Set("Content-Type", "text/plain")
	fmt.Fprintf(w, "knotserver/%s", version)
}

func (h *Handle) Publish() error {
	ownerDid := h.c.Owner.Did
	appPassword := h.c.Owner.AppPassword

	res := resolver.DefaultResolver()
	ident, err := res.ResolveIdent(context.Background(), ownerDid)
	if err != nil {
		return err
	}

	client := xrpc.Client{
		Host: ident.PDSEndpoint(),
	}

	resp, err := atproto.ServerCreateSession(context.Background(), &client, &atproto.ServerCreateSession_Input{
		Identifier: ownerDid,
		Password:   appPassword,
	})
	if err != nil {
		return err
	}

	authClient := xrpc.Client{
		Host: ident.PDSEndpoint(),
		Auth: &xrpc.AuthInfo{
			AccessJwt:  resp.AccessJwt,
			RefreshJwt: resp.RefreshJwt,
			Handle:     resp.Handle,
			Did:        resp.Did,
		},
	}

	rkey := h.TID()

	// write a "knot" record to the owners's pds
	_, err = atproto.RepoPutRecord(context.Background(), &authClient, &atproto.RepoPutRecord_Input{
		Collection: tangled.KnotNSID,
		Repo:       ownerDid,
		Rkey:       rkey,
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.Knot{
				CreatedAt: time.Now().Format(time.RFC3339),
				Host:      h.c.Server.Hostname,
			},
		},
	})
	if err != nil {
		return err
	}

	err = h.db.SetOwner(ownerDid, rkey)
	if err != nil {
		return err
	}

	err = h.db.AddDid(ownerDid)
	if err != nil {
		return err
	}

	return nil
}

func (h *Handle) TID() string {
	return h.clock.Next().String()
}
