package xrpc

import (
	"context"
	_ "embed"
	"encoding/json"
	"fmt"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bluesky-social/indigo/atproto/auth"
	"github.com/go-chi/chi/v5"

	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/idresolver"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/spindle/config"
	"tangled.sh/tangled.sh/core/spindle/db"
	"tangled.sh/tangled.sh/core/spindle/engine"
	"tangled.sh/tangled.sh/core/spindle/secrets"
)

const ActorDid string = "ActorDid"

type Xrpc struct {
	Logger   *slog.Logger
	Db       *db.DB
	Enforcer *rbac.Enforcer
	Engine   *engine.Engine
	Config   *config.Config
	Resolver *idresolver.Resolver
	Vault    secrets.Manager
}

func (x *Xrpc) Router() http.Handler {
	r := chi.NewRouter()

	r.With(x.VerifyServiceAuth).Post("/"+tangled.RepoAddSecretNSID, x.AddSecret)
	r.With(x.VerifyServiceAuth).Post("/"+tangled.RepoRemoveSecretNSID, x.RemoveSecret)
	r.With(x.VerifyServiceAuth).Get("/"+tangled.RepoListSecretsNSID, x.ListSecrets)

	return r
}

func (x *Xrpc) VerifyServiceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		l := x.Logger.With("url", r.URL)

		token := r.Header.Get("Authorization")
		token = strings.TrimPrefix(token, "Bearer ")

		s := auth.ServiceAuthValidator{
			Audience: x.Config.Server.Did().String(),
			Dir:      x.Resolver.Directory(),
		}

		did, err := s.Validate(r.Context(), token, nil)
		if err != nil {
			l.Error("signature verification failed", "err", err)
			writeError(w, AuthError(err), http.StatusForbidden)
			return
		}

		r = r.WithContext(
			context.WithValue(r.Context(), ActorDid, did),
		)

		next.ServeHTTP(w, r)
	})
}

type XrpcError struct {
	Tag     string `json:"error"`
	Message string `json:"message"`
}

func NewXrpcError(opts ...ErrOpt) XrpcError {
	x := XrpcError{}
	for _, o := range opts {
		o(&x)
	}

	return x
}

type ErrOpt = func(xerr *XrpcError)

func WithTag(tag string) ErrOpt {
	return func(xerr *XrpcError) {
		xerr.Tag = tag
	}
}

func WithMessage[S ~string](s S) ErrOpt {
	return func(xerr *XrpcError) {
		xerr.Message = string(s)
	}
}

func WithError(e error) ErrOpt {
	return func(xerr *XrpcError) {
		xerr.Message = e.Error()
	}
}

var MissingActorDidError = NewXrpcError(
	WithTag("MissingActorDid"),
	WithMessage("actor DID not supplied"),
)

var AuthError = func(err error) XrpcError {
	return NewXrpcError(
		WithTag("Auth"),
		WithError(fmt.Errorf("signature verification failed: %w", err)),
	)
}

var InvalidRepoError = func(r string) XrpcError {
	return NewXrpcError(
		WithTag("InvalidRepo"),
		WithError(fmt.Errorf("supplied at-uri is not a repo: %s", r)),
	)
}

func GenericError(err error) XrpcError {
	return NewXrpcError(
		WithTag("Generic"),
		WithError(err),
	)
}

var AccessControlError = func(d string) XrpcError {
	return NewXrpcError(
		WithTag("AccessControl"),
		WithError(fmt.Errorf("DID does not have sufficent access permissions for this operation: %s", d)),
	)
}

// this is slightly different from http_util::write_error to follow the spec:
//
// the json object returned must include an "error" and a "message"
func writeError(w http.ResponseWriter, e XrpcError, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(e)
}
