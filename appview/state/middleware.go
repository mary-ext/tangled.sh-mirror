package state

import (
	"context"
	"log"
	"net/http"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/xrpc"
	"github.com/go-chi/chi/v5"
	"github.com/sotangled/tangled/appview"
	"github.com/sotangled/tangled/appview/auth"
)

type Middleware func(http.Handler) http.Handler

func AuthMiddleware(s *State) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			session, _ := s.auth.Store.Get(r, appview.SessionName)
			authorized, ok := session.Values[appview.SessionAuthenticated].(bool)
			if !ok || !authorized {
				log.Printf("not logged in, redirecting")
				http.Redirect(w, r, "/login", http.StatusTemporaryRedirect)
				return
			}

			// refresh if nearing expiry
			// TODO: dedup with /login
			expiryStr := session.Values[appview.SessionExpiry].(string)
			expiry, err := time.Parse(time.RFC3339, expiryStr)
			if err != nil {
				log.Println("invalid expiry time", err)
				return
			}
			pdsUrl := session.Values[appview.SessionPds].(string)
			did := session.Values[appview.SessionDid].(string)
			refreshJwt := session.Values[appview.SessionRefreshJwt].(string)

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
					log.Println(err)
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

func RoleMiddleware(s *State, group string) Middleware {
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

func StripLeadingAt(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
		path := req.URL.Path
		if strings.HasPrefix(path, "/@") {
			req.URL.Path = "/" + strings.TrimPrefix(path, "/@")
		}
		next.ServeHTTP(w, req)
	})
}

func ResolveIdent(s *State) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			start := time.Now()
			didOrHandle := chi.URLParam(req, "user")

			log.Println(didOrHandle)
			id, err := s.resolver.ResolveIdent(req.Context(), didOrHandle)
			if err != nil {
				// invalid did or handle
				log.Println("failed to resolve did/handle")
				w.WriteHeader(http.StatusNotFound)
				return
			}

			ctx := context.WithValue(req.Context(), "resolvedId", *id)

			elapsed := time.Since(start)
			log.Println("Execution time:", elapsed)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

func ResolveRepoKnot(s *State) Middleware {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			repoName := chi.URLParam(req, "repo")
			id, ok := req.Context().Value("resolvedId").(identity.Identity)
			if !ok {
				log.Println("malformed middleware")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			repo, err := s.db.GetRepo(id.DID.String(), repoName)
			if err != nil {
				// invalid did or handle
				log.Println("failed to resolve repo")
				w.WriteHeader(http.StatusNotFound)
				return
			}

			ctx := context.WithValue(req.Context(), "knot", repo.Knot)
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}
