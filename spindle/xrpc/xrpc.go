package xrpc

import (
	_ "embed"
	"encoding/json"
	"log/slog"
	"net/http"

	"github.com/go-chi/chi/v5"

	"tangled.org/core/api/tangled"
	"tangled.org/core/idresolver"
	"tangled.org/core/rbac"
	"tangled.org/core/spindle/config"
	"tangled.org/core/spindle/db"
	"tangled.org/core/spindle/models"
	"tangled.org/core/spindle/secrets"
	xrpcerr "tangled.org/core/xrpc/errors"
	"tangled.org/core/xrpc/serviceauth"
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

	r.Group(func(r chi.Router) {
		r.Use(x.ServiceAuth.VerifyServiceAuth)

		r.Post("/"+tangled.RepoAddSecretNSID, x.AddSecret)
		r.Post("/"+tangled.RepoRemoveSecretNSID, x.RemoveSecret)
		r.Get("/"+tangled.RepoListSecretsNSID, x.ListSecrets)
	})

	// service query endpoints (no auth required)
	r.Get("/"+tangled.OwnerNSID, x.Owner)

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
