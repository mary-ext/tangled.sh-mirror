package knotserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"
	"runtime/debug"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/jetstream"
	"tangled.sh/tangled.sh/core/knotserver/config"
	"tangled.sh/tangled.sh/core/knotserver/db"
	"tangled.sh/tangled.sh/core/knotserver/xrpc"
	tlog "tangled.sh/tangled.sh/core/log"
	"tangled.sh/tangled.sh/core/notifier"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/xrpc/serviceauth"
)

type Handle struct {
	c        *config.Config
	db       *db.DB
	jc       *jetstream.JetstreamClient
	e        *rbac.Enforcer
	l        *slog.Logger
	n        *notifier.Notifier
	resolver *idresolver.Resolver
}

func Setup(ctx context.Context, c *config.Config, db *db.DB, e *rbac.Enforcer, jc *jetstream.JetstreamClient, l *slog.Logger, n *notifier.Notifier) (http.Handler, error) {
	r := chi.NewRouter()

	h := Handle{
		c:        c,
		db:       db,
		e:        e,
		l:        l,
		jc:       jc,
		n:        n,
		resolver: idresolver.DefaultResolver(),
	}

	err := e.AddKnot(rbac.ThisServer)
	if err != nil {
		return nil, fmt.Errorf("failed to setup enforcer: %w", err)
	}

	err = h.configureOwner()
	if err != nil {
		return nil, err
	}
	h.l.Info("owner set", "did", h.c.Server.Owner)

	err = h.jc.StartJetstream(ctx, h.processMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to start jetstream: %w", err)
	}

	h.jc.AddDid(h.c.Server.Owner)

	// check if the knot knows about any dids
	dids, err := h.db.GetAllDids()
	if err != nil {
		return nil, fmt.Errorf("failed to get all dids: %w", err)
	}
	for _, d := range dids {
		jc.AddDid(d)
	}

	r.Get("/", h.Index)
	r.Get("/capabilities", h.Capabilities)
	r.Get("/version", h.Version)
	r.Get("/owner", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte(h.c.Server.Owner))
	})
	r.Route("/{did}", func(r chi.Router) {
		// Repo routes
		r.Route("/{name}", func(r chi.Router) {
			r.Route("/collaborator", func(r chi.Router) {
				r.Use(h.VerifySignature)
				r.Post("/add", h.AddRepoCollaborator)
			})

			r.Route("/languages", func(r chi.Router) {
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

	// xrpc apis
	r.Mount("/xrpc", h.XrpcRouter())

	// Create a new repository.
	r.Route("/repo", func(r chi.Router) {
		r.Use(h.VerifySignature)
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

	// Socket that streams git oplogs
	r.Get("/events", h.Events)

	// Health check. Used for two-way verification with appview.
	r.With(h.VerifySignature).Get("/health", h.Health)

	// All public keys on the knot.
	r.Get("/keys", h.Keys)

	return r, nil
}

func (h *Handle) XrpcRouter() http.Handler {
	logger := tlog.New("knots")

	serviceAuth := serviceauth.NewServiceAuth(h.l, h.resolver, h.c.Server.Did().String())

	xrpc := &xrpc.Xrpc{
		Config:      h.c,
		Db:          h.db,
		Ingester:    h.jc,
		Enforcer:    h.e,
		Logger:      logger,
		Notifier:    h.n,
		Resolver:    h.resolver,
		ServiceAuth: serviceAuth,
	}
	return xrpc.Router()
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

	w.Header().Set("Content-Type", "text/plain; charset=utf-8")
	fmt.Fprintf(w, "knotserver/%s", version)
}

func (h *Handle) configureOwner() error {
	cfgOwner := h.c.Server.Owner

	rbacDomain := "thisserver"

	existing, err := h.e.GetKnotUsersByRole("server:owner", rbacDomain)
	if err != nil {
		return err
	}

	switch len(existing) {
	case 0:
		// no owner configured, continue
	case 1:
		// find existing owner
		existingOwner := existing[0]

		// no ownership change, this is okay
		if existingOwner == h.c.Server.Owner {
			break
		}

		// remove existing owner
		err = h.e.RemoveKnotOwner(rbacDomain, existingOwner)
		if err != nil {
			return nil
		}
	default:
		return fmt.Errorf("more than one owner in DB, try deleting %q and starting over", h.c.Server.DBPath)
	}

	return h.e.AddKnotOwner(rbacDomain, cfgOwner)
}
