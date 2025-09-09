package xrpc

import (
	"encoding/json"
	"log/slog"
	"net/http"
	"strings"

	securejoin "github.com/cyphar/filepath-securejoin"
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
		r.Post("/"+tangled.RepoForkStatusNSID, x.ForkStatus)
		r.Post("/"+tangled.RepoForkSyncNSID, x.ForkSync)
		r.Post("/"+tangled.RepoHiddenRefNSID, x.HiddenRef)
		r.Post("/"+tangled.RepoMergeNSID, x.Merge)
	})

	// merge check is an open endpoint
	//
	// TODO: should we constrain this more?
	// - we can calculate on PR submit/resubmit/gitRefUpdate etc.
	// - use ETags on clients to keep requests to a minimum
	r.Post("/"+tangled.RepoMergeCheckNSID, x.MergeCheck)

	// repo query endpoints (no auth required)
	r.Get("/"+tangled.RepoTreeNSID, x.RepoTree)
	r.Get("/"+tangled.RepoLogNSID, x.RepoLog)
	r.Get("/"+tangled.RepoBranchesNSID, x.RepoBranches)
	r.Get("/"+tangled.RepoTagsNSID, x.RepoTags)
	r.Get("/"+tangled.RepoBlobNSID, x.RepoBlob)
	r.Get("/"+tangled.RepoDiffNSID, x.RepoDiff)
	r.Get("/"+tangled.RepoCompareNSID, x.RepoCompare)
	r.Get("/"+tangled.RepoGetDefaultBranchNSID, x.RepoGetDefaultBranch)
	r.Get("/"+tangled.RepoBranchNSID, x.RepoBranch)
	r.Get("/"+tangled.RepoArchiveNSID, x.RepoArchive)
	r.Get("/"+tangled.RepoLanguagesNSID, x.RepoLanguages)

	// knot query endpoints (no auth required)
	r.Get("/"+tangled.KnotListKeysNSID, x.ListKeys)
	r.Get("/"+tangled.KnotVersionNSID, x.Version)

	// service query endpoints (no auth required)
	r.Get("/"+tangled.OwnerNSID, x.Owner)

	return r
}

// parseRepoParam parses a repo parameter in 'did/repoName' format and returns
// the full repository path on disk
func (x *Xrpc) parseRepoParam(repo string) (string, error) {
	if repo == "" {
		return "", xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("missing repo parameter"),
		)
	}

	// Parse repo string (did/repoName format)
	parts := strings.SplitN(repo, "/", 2)
	if len(parts) != 2 {
		return "", xrpcerr.NewXrpcError(
			xrpcerr.WithTag("InvalidRequest"),
			xrpcerr.WithMessage("invalid repo format, expected 'did/repoName'"),
		)
	}

	did := parts[0]
	repoName := parts[1]

	// Construct repository path using the same logic as didPath
	didRepoPath, err := securejoin.SecureJoin(did, repoName)
	if err != nil {
		return "", xrpcerr.RepoNotFoundError
	}

	repoPath, err := securejoin.SecureJoin(x.Config.Repo.ScanPath, didRepoPath)
	if err != nil {
		return "", xrpcerr.RepoNotFoundError
	}

	return repoPath, nil
}

func writeError(w http.ResponseWriter, e xrpcerr.XrpcError, status int) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	json.NewEncoder(w).Encode(e)
}
