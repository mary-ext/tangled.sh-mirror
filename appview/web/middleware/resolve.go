package middleware

import (
	"context"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/web/request"
	"tangled.org/core/idresolver"
	"tangled.org/core/log"
)

func ResolveIdent(
	idResolver *idresolver.Resolver,
	pages *pages.Pages,
) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			l := log.FromContext(ctx)
			didOrHandle := chi.URLParam(r, "user")
			didOrHandle = strings.TrimPrefix(didOrHandle, "@")

			id, err := idResolver.ResolveIdent(ctx, didOrHandle)
			if err != nil {
				// invalid did or handle
				l.Warn("failed to resolve did/handle", "handle", didOrHandle, "err", err)
				pages.Error404(w)
				return
			}

			ctx = request.WithOwner(ctx, id)
			// TODO: reomove this later
			ctx = context.WithValue(ctx, "resolvedId", *id)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func ResolveRepo(
	e *db.DB,
	pages *pages.Pages,
) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			l := log.FromContext(ctx)
			repoName := chi.URLParam(r, "repo")
			repoOwner, ok := request.OwnerFromContext(ctx)
			if !ok {
				l.Error("malformed middleware")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			repo, err := db.GetRepo(
				e,
				db.FilterEq("did", repoOwner.DID.String()),
				db.FilterEq("name", repoName),
			)
			if err != nil {
				l.Warn("failed to resolve repo", "err", err)
				pages.ErrorKnot404(w)
				return
			}

			// TODO: pass owner id into repository object

			ctx = request.WithRepo(ctx, repo)
			// TODO: reomove this later
			ctx = context.WithValue(ctx, "repo", repo)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func ResolveIssue(
	e *db.DB,
	pages *pages.Pages,
) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			ctx := r.Context()
			l := log.FromContext(ctx)
			issueIdStr := chi.URLParam(r, "issue")
			issueId, err := strconv.Atoi(issueIdStr)
			if err != nil {
				l.Warn("failed to fully resolve issue ID", "err", err)
				pages.Error404(w)
				return
			}
			repo, ok := request.RepoFromContext(ctx)
			if !ok {
				l.Error("malformed middleware")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			issue, err := db.GetIssue(e, repo.RepoAt(), issueId)
			if err != nil {
				l.Warn("failed to resolve issue", "err", err)
				pages.ErrorKnot404(w)
				return
			}
			issue.Repo = repo

			ctx = request.WithIssue(ctx, issue)
			// TODO: reomove this later
			ctx = context.WithValue(ctx, "issue", issue)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
