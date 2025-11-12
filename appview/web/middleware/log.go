package middleware

import (
	"log/slog"
	"net/http"

	"tangled.org/core/log"
)

func WithLogger(l *slog.Logger) middlewareFunc {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			// NOTE: can add some metadata here
			ctx := log.IntoContext(r.Context(), l)
			next.ServeHTTP(w, r.WithContext(ctx))
		})
	}
}
