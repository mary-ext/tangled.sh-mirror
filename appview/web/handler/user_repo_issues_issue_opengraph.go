package handler

import (
	"net/http"

	"tangled.org/core/appview/service/issue"
)

func IssueOpenGraph(s issue.Service) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		panic("unimplemented")
	}
}
