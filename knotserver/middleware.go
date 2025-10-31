package knotserver

import (
	"log/slog"
	"net/http"
	"time"
)

func (h *Knot) RequestLogger(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		start := time.Now()

		next.ServeHTTP(w, r)

		// Build query params as slog.Attrs for the group
		queryParams := r.URL.Query()
		queryAttrs := make([]any, 0, len(queryParams))
		for key, values := range queryParams {
			if len(values) == 1 {
				queryAttrs = append(queryAttrs, slog.String(key, values[0]))
			} else {
				queryAttrs = append(queryAttrs, slog.Any(key, values))
			}
		}

		h.l.LogAttrs(r.Context(), slog.LevelInfo, "",
			slog.Group("request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Group("query", queryAttrs...),
				slog.Duration("duration", time.Since(start)),
			),
		)
	})
}

func (h *Knot) CORS(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		// Set CORS headers
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, POST, PUT, DELETE, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type, Authorization")
		w.Header().Set("Access-Control-Max-Age", "86400")

		// Handle preflight requests
		if r.Method == "OPTIONS" {
			w.WriteHeader(http.StatusOK)
			return
		}

		next.ServeHTTP(w, r)
	})
}
