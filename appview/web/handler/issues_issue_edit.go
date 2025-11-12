package handler

import (
	"net/http"

	"tangled.org/core/appview/service/issue"
)

func IssueEdit(s issue.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		panic("unimplemented")
	}
}

func IssueEditPost(s issue.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		panic("unimplemented")
	}
}
