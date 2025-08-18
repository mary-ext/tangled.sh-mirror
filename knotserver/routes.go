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

	// configure owner
	if err = h.configureOwner(); err != nil {
		return nil, err
	}
	h.l.Info("owner set", "did", h.c.Server.Owner)
	h.jc.AddDid(h.c.Server.Owner)

	// configure known-dids in jetstream consumer
	dids, err := h.db.GetAllDids()
	if err != nil {
		return nil, fmt.Errorf("failed to get all dids: %w", err)
	}
	for _, d := range dids {
		jc.AddDid(d)
	}

	err = h.jc.StartJetstream(ctx, h.processMessages)
	if err != nil {
		return nil, fmt.Errorf("failed to start jetstream: %w", err)
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

			r.Route("/languages", func(r chi.Router) {
				r.Get("/", h.RepoLanguages)
				r.Get("/{ref}", h.RepoLanguages)
			})

			r.Get("/", h.RepoIndex)
			r.Get("/info/refs", h.InfoRefs)
			r.Post("/git-upload-pack", h.UploadPack)
			r.Post("/git-receive-pack", h.ReceivePack)
			r.Get("/compare/{rev1}/{rev2}", h.Compare) // git diff-tree compare of two objects

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
				r.Get("/default", h.DefaultBranch)
			})
		})
	})

	// xrpc apis
	r.Mount("/xrpc", h.XrpcRouter())

	// Socket that streams git oplogs
	r.Get("/events", h.Events)

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
