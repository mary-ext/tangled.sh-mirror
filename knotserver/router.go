package knotserver

import (
	"context"
	"fmt"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/idresolver"
	"tangled.org/core/jetstream"
	"tangled.org/core/knotserver/config"
	"tangled.org/core/knotserver/db"
	"tangled.org/core/knotserver/xrpc"
	"tangled.org/core/log"
	"tangled.org/core/notifier"
	"tangled.org/core/rbac"
	"tangled.org/core/xrpc/serviceauth"
)

type Knot struct {
	c        *config.Config
	db       *db.DB
	jc       *jetstream.JetstreamClient
	e        *rbac.Enforcer
	l        *slog.Logger
	n        *notifier.Notifier
	resolver *idresolver.Resolver
}

func Setup(ctx context.Context, c *config.Config, db *db.DB, e *rbac.Enforcer, jc *jetstream.JetstreamClient, n *notifier.Notifier) (http.Handler, error) {
	h := Knot{
		c:        c,
		db:       db,
		e:        e,
		l:        log.FromContext(ctx),
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

	return h.Router(), nil
}

func (h *Knot) Router() http.Handler {
	r := chi.NewRouter()

	r.Use(h.RequestLogger)

	r.Get("/", func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("This is a knot server. More info at https://tangled.sh"))
	})

	r.Route("/{did}", func(r chi.Router) {
		r.Route("/{name}", func(r chi.Router) {
			// routes for git operations
			r.Get("/info/refs", h.InfoRefs)
			r.Post("/git-upload-pack", h.UploadPack)
			r.Post("/git-receive-pack", h.ReceivePack)
		})
	})

	// xrpc apis
	r.Mount("/xrpc", h.XrpcRouter())

	// Socket that streams git oplogs
	r.Get("/events", h.Events)

	return r
}

func (h *Knot) XrpcRouter() http.Handler {
	serviceAuth := serviceauth.NewServiceAuth(h.l, h.resolver, h.c.Server.Did().String())

	l := log.SubLogger(h.l, "xrpc")

	xrpc := &xrpc.Xrpc{
		Config:      h.c,
		Db:          h.db,
		Ingester:    h.jc,
		Enforcer:    h.e,
		Logger:      l,
		Notifier:    h.n,
		Resolver:    h.resolver,
		ServiceAuth: serviceAuth,
	}

	return xrpc.Router()
}

func (h *Knot) configureOwner() error {
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
		if err = h.db.RemoveDid(existingOwner); err != nil {
			return err
		}
		if err = h.e.RemoveKnotOwner(rbacDomain, existingOwner); err != nil {
			return err
		}

	default:
		return fmt.Errorf("more than one owner in DB, try deleting %q and starting over", h.c.Server.DBPath)
	}

	if err = h.db.AddDid(cfgOwner); err != nil {
		return fmt.Errorf("failed to add owner to DB: %w", err)
	}
	if err := h.e.AddKnotOwner(rbacDomain, cfgOwner); err != nil {
		return fmt.Errorf("failed to add owner to RBAC: %w", err)
	}

	return nil
}
