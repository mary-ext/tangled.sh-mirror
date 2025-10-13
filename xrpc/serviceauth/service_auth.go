package serviceauth

import (
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	"github.com/bluesky-social/indigo/atproto/auth"
	"tangled.org/core/idresolver"
	"tangled.org/core/log"
	xrpcerr "tangled.org/core/xrpc/errors"
)

const ActorDid string = "ActorDid"

type ServiceAuth struct {
	logger      *slog.Logger
	resolver    *idresolver.Resolver
	audienceDid string
}

func NewServiceAuth(logger *slog.Logger, resolver *idresolver.Resolver, audienceDid string) *ServiceAuth {
	return &ServiceAuth{
		logger:      log.SubLogger(logger, "serviceauth"),
		resolver:    resolver,
		audienceDid: audienceDid,
	}
}

func (sa *ServiceAuth) VerifyServiceAuth(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		token := r.Header.Get("Authorization")
		token = strings.TrimPrefix(token, "Bearer ")

		s := auth.ServiceAuthValidator{
			Audience: sa.audienceDid,
			Dir:      sa.resolver.Directory(),
		}

		did, err := s.Validate(r.Context(), token, nil)
		if err != nil {
			sa.logger.Error("signature verification failed", "err", err)
			writeError(w, xrpcerr.AuthError(err), http.StatusForbidden)
			return
		}

		sa.logger.Debug("valid signature", ActorDid, did)

		r = r.WithContext(
			context.WithValue(r.Context(), ActorDid, did),
		)

		next.ServeHTTP(w, r)
	})
}

// this is slightly different from http_util::write_error to follow the spec:
//
// the json object returned must include an "error" and a "message"
func writeError(w http.ResponseWriter, e xrpcerr.XrpcError, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(e)
}
