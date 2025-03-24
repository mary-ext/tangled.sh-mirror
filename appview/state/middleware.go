package state

import (
	"context"
	"log"
	"net/http"
	"strconv"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/auth"
	"tangled.sh/tangled.sh/core/appview/db"
)

type Middleware func(http.Handler) http.Handler

func AuthMiddleware(s *State) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			redirectFunc := func(w http.ResponseWriter, r *http.Request) {
				http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
			}
			if r.Header.Get("HX-Request") == "true" {
				redirectFunc = func(w http.ResponseWriter, _ *http.Request) {
					w.Header().Set("HX-Redirect", "/login")
					w.WriteHeader(http.StatusOK)
				}
			}

			session, err := s.auth.GetSession(r)
			if session.IsNew || err != nil {
				log.Printf("not logged in, redirecting")
				redirectFunc(w, r)
				return
			}

			authorized, ok := session.Values[appview.SessionAuthenticated].(bool)
			if !ok || !authorized {
				log.Printf("not logged in, redirecting")
				redirectFunc(w, r)
				return
			}

			// refresh if nearing expiry
			// TODO: dedup with /login
			expiryStr := session.Values[appview.SessionExpiry].(string)
			expiry, err := time.Parse(time.RFC3339, expiryStr)
			if err != nil {
				log.Println("invalid expiry time", err)
				redirectFunc(w, r)
				return
			}
			pdsUrl, ok1 := session.Values[appview.SessionPds].(string)
			did, ok2 := session.Values[appview.SessionDid].(string)
			refreshJwt, ok3 := session.Values[appview.SessionRefreshJwt].(string)

			if !ok1 || !ok2 || !ok3 {
				log.Println("invalid expiry time", err)
				redirectFunc(w, r)
				return
			}

			if time.Now().After(expiry) {
				log.Println("token expired, refreshing ...")

				client := xrpc.Client{
					Host: pdsUrl,
					Auth: &xrpc.AuthInfo{
						Did:        did,
						AccessJwt:  refreshJwt,
						RefreshJwt: refreshJwt,
					},
				}
				atSession, err := comatproto.ServerRefreshSession(r.Context(), &client)
				if err != nil {
					log.Println("failed to refresh session", err)
					redirectFunc(w, r)
					return
				}

				sessionish := auth.RefreshSessionWrapper{atSession}

				err = s.auth.StoreSession(r, w, &sessionish, pdsUrl)
				if err != nil {
					log.Printf("failed to store session for did: %s\n: %s", atSession.Did, err)
					return
				}

				log.Println("successfully refreshed token")
			}

			next.ServeHTTP(w, r)
		})
	}
}

func knotRoleMiddleware(s *State, group string) Middleware {
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

func KnotOwner(s *State) Middleware {
	return knotRoleMiddleware(s, "server:owner")
}

func RepoPermissionMiddleware(s *State, requiredPerm string) Middleware {
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
			f, err := fullyResolvedRepo(r)
			if err != nil {
				http.Error(w, "malformed url", http.StatusBadRequest)
				return
			}

			ok, err := s.enforcer.E.Enforce(actor.Did, f.Knot, f.OwnerSlashRepo(), requiredPerm)
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

func ResolveIdent(s *State) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			didOrHandle := chi.URLParam(req, "user")

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

func ResolveRepo(s *State) Middleware {
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
func ResolvePull(s *State) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f, err := fullyResolvedRepo(r)
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
