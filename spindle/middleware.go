package spindle

import (
	"log/slog"
	"net/http"
	"time"
)

func (s *Spindle) RequestLogger(next http.Handler) http.Handler {
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

		s.l.LogAttrs(r.Context(), slog.LevelInfo, "",
			slog.Group("request",
				slog.String("method", r.Method),
				slog.String("path", r.URL.Path),
				slog.Group("query", queryAttrs...),
				slog.Duration("duration", time.Since(start)),
			),
		)
	})
}
