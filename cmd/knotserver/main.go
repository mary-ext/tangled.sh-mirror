package main

import (
	"context"
	"net/http"

	"github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/jetstream"
	"github.com/sotangled/tangled/knotserver"
	"github.com/sotangled/tangled/knotserver/config"
	"github.com/sotangled/tangled/knotserver/db"
	"github.com/sotangled/tangled/log"
	"github.com/sotangled/tangled/rbac"
)

func main() {
	ctx := context.Background()
	// ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// defer stop()

	l := log.New("knotserver")

	c, err := config.Load(ctx)
	if err != nil {
		l.Error("failed to load config", "error", err)
		return
	}

	if c.Server.Dev {
		l.Info("running in dev mode, signature verification is disabled")
	}

	db, err := db.Setup(c.Server.DBPath)
	if err != nil {
		l.Error("failed to setup db", "error", err)
		return
	}

	e, err := rbac.NewEnforcer(c.Server.DBPath)
	if err != nil {
		l.Error("failed to setup rbac enforcer", "error", err)
		return
	}

	e.E.EnableAutoSave(true)

	jc, err := jetstream.NewJetstreamClient("knotserver", []string{
		tangled.PublicKeyNSID,
		tangled.KnotMemberNSID,
	}, nil, l, db, true)
	if err != nil {
		l.Error("failed to setup jetstream", "error", err)
	}

	mux, err := knotserver.Setup(ctx, c, db, e, jc, l)
	if err != nil {
		l.Error("failed to setup server", "error", err)
		return
	}
	imux := knotserver.Internal(ctx, db, e)

	l.Info("starting internal server", "address", c.Server.InternalListenAddr)
	go http.ListenAndServe(c.Server.InternalListenAddr, imux)

	l.Info("starting main server", "address", c.Server.ListenAddr)
	l.Error("server error", "error", http.ListenAndServe(c.Server.ListenAddr, mux))

	return
}
