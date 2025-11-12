package middleware

import (
	"log"
	"net/http"
	"strconv"
	"strings"

	"github.com/go-chi/chi/v5"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/pages"
	issue_service "tangled.org/core/appview/service/issue"
	owner_service "tangled.org/core/appview/service/owner"
	repo_service "tangled.org/core/appview/service/repo"
	"tangled.org/core/idresolver"
)

func ResolveIdent(
	idResolver *idresolver.Resolver,
	pages *pages.Pages,
) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			didOrHandle := chi.URLParam(r, "user")
			didOrHandle = strings.TrimPrefix(didOrHandle, "@")

			id, err := idResolver.ResolveIdent(r.Context(), didOrHandle)
			if err != nil {
				// invalid did or handle
				log.Printf("failed to resolve did/handle '%s': %s\n", didOrHandle, err)
				pages.Error404(w)
				return
			}

			ctx := owner_service.IntoContext(r.Context(), id)
			log.Println("ident resolved")

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func ResolveRepo(
	e *db.DB,
	idResolver *idresolver.Resolver,
	pages *pages.Pages,
) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			repoName := chi.URLParam(r, "repo")
			repoOwner, ok := owner_service.FromContext(r.Context())
			if !ok {
				log.Println("malformed middleware")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			repo, err := db.GetRepo(
				e,
				db.FilterEq("did", repoOwner.DID.String()),
				db.FilterEq("name", repoName),
			)
			if err != nil {
				log.Println("failed to resolve repo", "err", err)
				pages.ErrorKnot404(w)
				return
			}

			// TODO: pass owner id into repository object

			ctx := repo_service.IntoContext(r.Context(), repo)
			log.Println("repo resolved")

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

func ResolveIssue(
	e *db.DB,
	idResolver *idresolver.Resolver,
	pages *pages.Pages,
) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			issueIdStr := chi.URLParam(r, "issue")
			issueId, err := strconv.Atoi(issueIdStr)
			if err != nil {
				log.Println("failed to fully resolve issue ID", err)
				pages.Error404(w)
				return
			}
			repo, ok := repo_service.FromContext(r.Context())
			if !ok {
				log.Println("malformed middleware")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			issue, err := db.GetIssue(e, repo.RepoAt(), issueId)
			if err != nil {
				log.Println("failed to resolve repo", "err", err)
				pages.ErrorKnot404(w)
				return
			}
			issue.Repo = repo

			ctx := issue_service.IntoContext(r.Context(), issue)
			log.Println("issue resolved")

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
