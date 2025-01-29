package middleware

import (
	"context"
	"log"
	"net/http"
	"sync"
	"time"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/icyphox/bild/auth"
)

type cachedIdent struct {
	ident  *identity.Identity
	expiry time.Time
}

var (
	identCache = make(map[string]cachedIdent)
	cacheMutex sync.RWMutex
)

// Only use this middleware for routes that require a handle
// /@{user}/...
func AddDID(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user := r.PathValue("user")

		// Check cache first
		cacheMutex.RLock()
		if cached, ok := identCache[user]; ok && time.Now().Before(cached.expiry) {
			cacheMutex.RUnlock()
			ctx := context.WithValue(r.Context(), "did", cached.ident.DID.String())
			r = r.WithContext(ctx)
			next.ServeHTTP(w, r)
			return
		}
		cacheMutex.RUnlock()

		// Cache miss - resolve and cache
		ident, err := auth.ResolveIdent(r.Context(), user)
		if err != nil {
			log.Println("error resolving identity", err)
			http.Error(w, "error resolving identity", http.StatusNotFound)
			return
		}

		cacheMutex.Lock()
		identCache[user] = cachedIdent{
			ident:  ident,
			expiry: time.Now().Add(24 * time.Hour),
		}
		cacheMutex.Unlock()

		ctx := context.WithValue(r.Context(), "did", ident.DID.String())
		r = r.WithContext(ctx)

		next.ServeHTTP(w, r)
	})
}
