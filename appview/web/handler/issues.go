package handler

import (
	"net/http"

	"tangled.org/core/appview/service/issue"
)

func RepoIssues(s issue.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
	}
}
