package middleware

import (
	"net/http"
)

type middlewareFunc func(http.Handler) http.Handler
