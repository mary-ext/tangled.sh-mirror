package main

import (
	"context"
	"fmt"
	"net/http"

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

	mux, err := knotserver.Setup(ctx, c, db, e, l)
	if err != nil {
		l.Error("failed to setup server", "error", err)
		return
	}
	addr := fmt.Sprintf("%s:%d", c.Server.Host, c.Server.Port)

	imux := knotserver.Internal(ctx, db, e)
	iaddr := fmt.Sprintf("%s:%d", c.Server.Host, c.Server.InternalPort)

	l.Info("starting internal server", "address", iaddr)
	go http.ListenAndServe(iaddr, imux)

	l.Info("starting main server", "address", addr)
	l.Error("server error", "error", http.ListenAndServe(addr, mux))

	return
}
