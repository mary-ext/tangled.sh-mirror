package xrpc

import (
	"encoding/json"
	"fmt"
	"net/http"
	"time"

	"github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/bluesky-social/indigo/xrpc"
	securejoin "github.com/cyphar/filepath-securejoin"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/rbac"
	"tangled.sh/tangled.sh/core/spindle/secrets"
	xrpcerr "tangled.sh/tangled.sh/core/xrpc/errors"
)

func (x *Xrpc) ListSecrets(w http.ResponseWriter, r *http.Request) {
	l := x.Logger
	fail := func(e xrpcerr.XrpcError) {
		l.Error("failed", "kind", e.Tag, "error", e.Message)
		writeError(w, e, http.StatusBadRequest)
	}

	actorDid, ok := r.Context().Value(ActorDid).(syntax.DID)
	if !ok {
		fail(xrpcerr.MissingActorDidError)
		return
	}

	repoParam := r.URL.Query().Get("repo")
	if repoParam == "" {
		fail(xrpcerr.GenericError(fmt.Errorf("empty params")))
		return
	}

	// unfortunately we have to resolve repo-at here
	repoAt, err := syntax.ParseATURI(repoParam)
	if err != nil {
		fail(xrpcerr.InvalidRepoError(repoParam))
		return
	}

	// resolve this aturi to extract the repo record
	ident, err := x.Resolver.ResolveIdent(r.Context(), repoAt.Authority().String())
	if err != nil || ident.Handle.IsInvalidHandle() {
		fail(xrpcerr.GenericError(fmt.Errorf("failed to resolve handle: %w", err)))
		return
	}

	xrpcc := xrpc.Client{Host: ident.PDSEndpoint()}
	resp, err := atproto.RepoGetRecord(r.Context(), &xrpcc, "", tangled.RepoNSID, repoAt.Authority().String(), repoAt.RecordKey().String())
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	repo := resp.Value.Val.(*tangled.Repo)
	didPath, err := securejoin.SecureJoin(repo.Owner, repo.Name)
	if err != nil {
		fail(xrpcerr.GenericError(err))
		return
	}

	if ok, err := x.Enforcer.IsSettingsAllowed(actorDid.String(), rbac.ThisServer, didPath); !ok || err != nil {
		l.Error("insufficent permissions", "did", actorDid.String())
		writeError(w, xrpcerr.AccessControlError(actorDid.String()), http.StatusUnauthorized)
		return
	}

	ls, err := x.Vault.GetSecretsLocked(r.Context(), secrets.DidSlashRepo(didPath))
	if err != nil {
		l.Error("failed to get secret from vault", "did", actorDid.String(), "err", err)
		writeError(w, xrpcerr.GenericError(err), http.StatusInternalServerError)
		return
	}

	var out tangled.RepoListSecrets_Output
	for _, l := range ls {
		out.Secrets = append(out.Secrets, &tangled.RepoListSecrets_Secret{
			Repo:      repoAt.String(),
			Key:       l.Key,
			CreatedAt: l.CreatedAt.Format(time.RFC3339),
			CreatedBy: l.CreatedBy.String(),
		})
	}

	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	json.NewEncoder(w).Encode(out)
}
