package xrpc

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/spindle/config"
	"tangled.sh/tangled.sh/core/spindle/db"
	"tangled.sh/tangled.sh/core/spindle/models"
	"tangled.sh/tangled.sh/core/spindle/secrets"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
	"tangled.sh/tangled.sh/core/xrpc/serviceauth"
)

const ActorDid string = "ActorDid"

type Xrpc struct {
	Logger      *slog.Logger
	Db          *db.DB
	Enforcer    *rbac.Enforcer
	Engines     map[string]models.Engine
	Config      *config.Config
	Resolver    *idresolver.Resolver
	Vault       secrets.Manager
	ServiceAuth *serviceauth.ServiceAuth
}

func (x *Xrpc) Router() http.Handler {
	r := chi.NewRouter()

	r.With(x.ServiceAuth.VerifyServiceAuth).Post("/"+tangled.RepoAddSecretNSID, x.AddSecret)
	r.With(x.ServiceAuth.VerifyServiceAuth).Post("/"+tangled.RepoRemoveSecretNSID, x.RemoveSecret)
	r.With(x.ServiceAuth.VerifyServiceAuth).Get("/"+tangled.RepoListSecretsNSID, x.ListSecrets)

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
