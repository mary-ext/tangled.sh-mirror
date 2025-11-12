package middleware

import (
	"log"
	"net/http"
	"strconv"

	"tangled.org/core/appview/pagination"
)

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

		ctx := pagination.IntoContext(r.Context(), page)
		next.ServeHTTP(w, r.WithContext(ctx))
	})
}
