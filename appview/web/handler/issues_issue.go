package handler

import (
	"net/http"

	"tangled.org/core/appview/pages"
	isvc "tangled.org/core/appview/service/issue"
	"tangled.org/core/log"
)

func Issue(s isvc.IssueService) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		panic("unimplemented")
	}
}

func IssueDelete(s isvc.IssueService, p *pages.Pages) http.HandlerFunc {
	noticeId := "issue-actions-error"
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := log.FromContext(ctx).With("handler", "IssueDelete")
		issue, ok := isvc.FromContext(ctx)
		if !ok {
			l.Error("failed to get issue")
			// TODO: 503 error with more detailed messages
			p.Error503(w)
			return
		}
		err := s.DeleteIssue(ctx, issue)
		if err != nil {
			p.Notice(w, noticeId, "failed to delete issue")
		}
		p.HxLocation(w, "/")
	}
}
