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

	r.With(x.ServiceAuth.VerifyServiceAuth).Post("/"+tangled.RepoSetDefaultBranchNSID, x.SetDefaultBranch)

	return r
}

// this is slightly different from http_util::write_error to follow the spec:
//
// the json object returned must include an "error" and a "message"
func writeError(w http.ResponseWriter, e xrpcerr.XrpcError, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(e)
}
