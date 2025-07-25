package middleware

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/idresolver"
	"tangled.sh/tangled.sh/core/appview/oauth"
	"tangled.sh/tangled.sh/core/appview/pages"
	"tangled.sh/tangled.sh/core/appview/pagination"
	"tangled.sh/tangled.sh/core/appview/reporesolver"
	"tangled.sh/tangled.sh/core/rbac"
)

type Middleware struct {
	oauth        *oauth.OAuth
	db           *db.DB
	enforcer     *rbac.Enforcer
	repoResolver *reporesolver.RepoResolver
	idResolver   *idresolver.Resolver
	pages        *pages.Pages
}

func New(oauth *oauth.OAuth, db *db.DB, enforcer *rbac.Enforcer, repoResolver *reporesolver.RepoResolver, idResolver *idresolver.Resolver, pages *pages.Pages) Middleware {
	return Middleware{
		oauth:        oauth,
		db:           db,
		enforcer:     enforcer,
		repoResolver: repoResolver,
		idResolver:   idResolver,
		pages:        pages,
	}
}

type middlewareFunc func(http.Handler) http.Handler

func AuthMiddleware(a *oauth.OAuth) middlewareFunc {
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

			_, auth, err := a.GetSession(r)
			if err != nil {
				log.Println("not logged in, redirecting", "err", err)
				redirectFunc(w, r)
				return
			}

			if !auth {
				log.Printf("not logged in, redirecting")
				redirectFunc(w, r)
				return
			}

			next.ServeHTTP(w, r)
		})
	}
}

func Paginate(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		page := pagination.FirstPage()

		offsetVal := r.URL.Query().Get("offset")
		if offsetVal != "" {
			offset, err := strconv.Atoi(offsetVal)
			if err != nil {
				log.Println("invalid offset")
			} else {
				page.Offset = offset
			}
		}

		limitVal := r.URL.Query().Get("limit")
		if limitVal != "" {
			limit, err := strconv.Atoi(limitVal)
			if err != nil {
				log.Println("invalid limit")
			} else {
				page.Limit = limit
			}
		}

		ctx := context.WithValue(r.Context(), "page", page)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}

func (mw Middleware) knotRoleMiddleware(group string) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// requires auth also
			actor := mw.oauth.GetUser(r)
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

			ok, err := mw.enforcer.E.HasGroupingPolicy(actor.Did, group, domain)
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

func (mw Middleware) KnotOwner() middlewareFunc {
	return mw.knotRoleMiddleware("server:owner")
}

func (mw Middleware) RepoPermissionMiddleware(requiredPerm string) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// requires auth also
			actor := mw.oauth.GetUser(r)
			if actor == nil {
				// we need a logged in user
				log.Printf("not logged in, redirecting")
				http.Error(w, "Forbiden", http.StatusUnauthorized)
				return
			}
			f, err := mw.repoResolver.Resolve(r)
			if err != nil {
				http.Error(w, "malformed url", http.StatusBadRequest)
				return
			}

			ok, err := mw.enforcer.E.Enforce(actor.Did, f.Knot, f.DidSlashRepo(), requiredPerm)
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

func (mw Middleware) ResolveIdent() middlewareFunc {
	excluded := []string{"favicon.ico"}

	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			didOrHandle := chi.URLParam(req, "user")
			if slices.Contains(excluded, didOrHandle) {
				next.ServeHTTP(w, req)
				return
			}

			id, err := mw.idResolver.ResolveIdent(req.Context(), didOrHandle)
			if err != nil {
				// invalid did or handle
				log.Println("failed to resolve did/handle:", err)
				mw.pages.Error404(w)
				return
			}

			ctx := context.WithValue(req.Context(), "resolvedId", *id)

			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

func (mw Middleware) ResolveRepo() middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, req *http.Request) {
			repoName := chi.URLParam(req, "repo")
			id, ok := req.Context().Value("resolvedId").(identity.Identity)
			if !ok {
				log.Println("malformed middleware")
				w.WriteHeader(http.StatusInternalServerError)
				return
			}

			repo, err := db.GetRepo(mw.db, id.DID.String(), repoName)
			if err != nil {
				// invalid did or handle
				log.Println("failed to resolve repo")
				mw.pages.Error404(w)
				return
			}

			ctx := context.WithValue(req.Context(), "knot", repo.Knot)
			ctx = context.WithValue(ctx, "repoAt", repo.AtUri)
			ctx = context.WithValue(ctx, "repoDescription", repo.Description)
			ctx = context.WithValue(ctx, "repoSpindle", repo.Spindle)
			ctx = context.WithValue(ctx, "repoAddedAt", repo.Created.Format(time.RFC3339))
			next.ServeHTTP(w, req.WithContext(ctx))
		})
	}
}

// middleware that is tacked on top of /{user}/{repo}/pulls/{pull}
func (mw Middleware) ResolvePull() middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f, err := mw.repoResolver.Resolve(r)
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

			pr, err := db.GetPull(mw.db, f.RepoAt, prIdInt)
			if err != nil {
				log.Println("failed to get pull and comments", err)
				return
			}

			ctx := context.WithValue(r.Context(), "pull", pr)

			if pr.IsStacked() {
				stack, err := db.GetStack(mw.db, pr.StackId)
				if err != nil {
					log.Println("failed to get stack", err)
					return
				}
				abandonedPulls, err := db.GetAbandonedPulls(mw.db, pr.StackId)
				if err != nil {
					log.Println("failed to get abandoned pulls", err)
					return
				}

				ctx = context.WithValue(ctx, "stack", stack)
				ctx = context.WithValue(ctx, "abandonedPulls", abandonedPulls)
			}

			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}

// this should serve the go-import meta tag even if the path is technically
// a 404 like tangled.sh/oppi.li/go-git/v5
func (mw Middleware) GoImport() middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			f, err := mw.repoResolver.Resolve(r)
			if err != nil {
				log.Println("failed to fully resolve repo", err)
				http.Error(w, "invalid repo url", http.StatusNotFound)
				return
			}

			fullName := f.OwnerHandle() + "/" + f.RepoName

			if r.Header.Get("User-Agent") == "Go-http-client/1.1" {
				if r.URL.Query().Get("go-get") == "1" {
					html := fmt.Sprintf(
						`<meta name="go-import" content="tangled.sh/%s git https://tangled.sh/%s"/>`,
						fullName,
						fullName,
					)
					w.Header().Set("Content-Type", "text/html")
					w.Write([]byte(html))
					return
				}
			}

			next.ServeHTTP(w, r)
		})
	}
}
