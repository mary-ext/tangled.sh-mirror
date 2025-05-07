package state

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	"slices"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/middleware"
)

func knotRoleMiddleware(s *State, group string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// requires auth also
			actor := s.auth.GetUser(r)
			if actor == nil {
				// we need a logged in user
				log.Printf("not logged in, redirecting")
				http.Error(w, "Forbiden", http.StatusUnauthorized)
				return
			}
			domain := chi.URLParam(r, "domain")
			if domain == "" {
				http.Error(w, "malformed url", http.StatusBadRequest)
				return
			}

			ok, err := s.enforcer.E.HasGroupingPolicy(actor.Did, group, domain)
			if err != nil || !ok {
				// we need a logged in user
				log.Printf("%s does not have perms of a %s in domain %s", actor.Did, group, domain)
				http.Error(w, "Forbiden", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func KnotOwner(s *State) middleware.Middleware {
	return knotRoleMiddleware(s, "server:owner")
}

func RepoPermissionMiddleware(s *State, requiredPerm string) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// requires auth also
			actor := s.auth.GetUser(r)
			if actor == nil {
				// we need a logged in user
				log.Printf("not logged in, redirecting")
				http.Error(w, "Forbiden", http.StatusUnauthorized)
				return
			}
			f, err := s.fullyResolvedRepo(r)
			if err != nil {
				http.Error(w, "malformed url", http.StatusBadRequest)
				return
			}

			ok, err := s.enforcer.E.Enforce(actor.Did, f.Knot, f.DidSlashRepo(), requiredPerm)
			if err != nil || !ok {
				// we need a logged in user
				log.Printf("%s does not have perms of a %s in repo %s", actor.Did, requiredPerm, f.OwnerSlashRepo())
				http.Error(w, "Forbiden", http.StatusUnauthorized)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func StripLeadingAt(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.EscapedPath()
		if strings.HasPrefix(path, "/@") {
			req.URL.RawPath = "/" + strings.TrimPrefix(path, "/@")
		}
		next.ServeHTTP(w, req)
	})
}

func ResolveIdent(s *State) middleware.Middleware {
	excluded := []string{"favicon.ico"}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			didOrHandle := chi.URLParam(req, "user")
			if slices.Contains(excluded, didOrHandle) {
				next.ServeHTTP(w, req)
				return
			}

			id, err := s.resolver.ResolveIdent(req.Context(), didOrHandle)
			if err != nil {
				// invalid did or handle
				log.Println("failed to resolve did/handle:", err)
				w.WriteHeader(http.StatusNotFound)
				return
			}

			ctx := context.WithValue(req.Context(), "resolvedId", *id)

			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

func ResolveRepo(s *State) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			repoName := chi.URLParam(req, "repo")
			id, ok := req.Context().Value("resolvedId").(identity.Identity)
			if !ok {
				log.Println("malformed middleware")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			repo, err := db.GetRepo(s.db, id.DID.String(), repoName)
			if err != nil {
				// invalid did or handle
				log.Println("failed to resolve repo")
				w.WriteHeader(http.StatusNotFound)
				return
			}

			ctx := context.WithValue(req.Context(), "knot", repo.Knot)
			ctx = context.WithValue(ctx, "repoAt", repo.AtUri)
			ctx = context.WithValue(ctx, "repoDescription", repo.Description)
			ctx = context.WithValue(ctx, "repoAddedAt", repo.Created.Format(time.RFC3339))
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

// middleware that is tacked on top of /{user}/{repo}/pulls/{pull}
func ResolvePull(s *State) middleware.Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f, err := s.fullyResolvedRepo(r)
			if err != nil {
				log.Println("failed to fully resolve repo", err)
				http.Error(w, "invalid repo url", http.StatusNotFound)
				return
			}

			prId := chi.URLParam(r, "pull")
			prIdInt, err := strconv.Atoi(prId)
			if err != nil {
				http.Error(w, "bad pr id", http.StatusBadRequest)
				log.Println("failed to parse pr id", err)
				return
			}

			pr, err := db.GetPull(s.db, f.RepoAt, prIdInt)
			if err != nil {
				log.Println("failed to get pull and comments", err)
				return
			}

			ctx := context.WithValue(r.Context(), "pull", pr)

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
