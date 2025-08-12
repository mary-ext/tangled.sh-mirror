package xrpc

import (
	"encoding/json"
	"log/slog"
	"net/http"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/jetstream"
	"tangled.sh/tangled.sh/core/knotserver/config"
	"tangled.sh/tangled.sh/core/knotserver/db"
	"tangled.sh/tangled.sh/core/notifier"
	"tangled.sh/tangled.sh/core/rbac"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
	"tangled.sh/tangled.sh/core/xrpc/serviceauth"

	"github.com/go-chi/chi/v5"
)

type Xrpc struct {
	Config      *config.Config
	Db          *db.DB
	Ingester    *jetstream.JetstreamClient
	Enforcer    *rbac.Enforcer
	Logger      *slog.Logger
	Notifier    *notifier.Notifier
	Resolver    *idresolver.Resolver
	ServiceAuth *serviceauth.ServiceAuth
}

func (x *Xrpc) Router() http.Handler {
	r := chi.NewRouter()
	r.Group(func(r chi.Router) {
		r.Use(x.ServiceAuth.VerifyServiceAuth)

		r.Post("/"+tangled.RepoSetDefaultBranchNSID, x.SetDefaultBranch)
		r.Post("/"+tangled.RepoCreateNSID, x.CreateRepo)
		r.Post("/"+tangled.RepoDeleteNSID, x.DeleteRepo)
		r.Post("/"+tangled.RepoForkNSID, x.ForkRepo)
		r.Post("/"+tangled.RepoForkStatusNSID, x.ForkStatus)
		r.Post("/"+tangled.RepoForkSyncNSID, x.ForkSync)

		r.Post("/"+tangled.RepoHiddenRefNSID, x.HiddenRef)

		r.Post("/"+tangled.RepoMergeNSID, x.Merge)
		r.Post("/"+tangled.RepoMergeCheckNSID, x.MergeCheck)
	})
	return r
}

func writeError(w http.ResponseWriter, e xrpcerr.XrpcError, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(e)
}
