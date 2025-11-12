package handler

import (
	"errors"
	"net/http"

	"tangled.org/core/appview/pages"
	isvc "tangled.org/core/appview/service/issue"
	"tangled.org/core/appview/service/repo"
	"tangled.org/core/log"
)

func NewIssue(p *pages.Pages) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		// TODO: render page
	}
}

func NewIssuePost(is isvc.IssueService, p *pages.Pages) http.HandlerFunc {
	noticeId := "issues"
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := log.FromContext(ctx).With("handler", "NewIssuePost")
		repo, ok := repo.FromContext(ctx)
		if !ok {
			l.Error("failed to get repo")
			// TODO: 503 error with more detailed messages
			p.Error503(w)
			return
		}
		var (
			title = r.FormValue("title")
			body  = r.FormValue("body")
		)

		_, err := is.NewIssue(ctx, repo, title, body)
		if err != nil {
			if errors.Is(err, isvc.ErrDatabaseFail) {
				p.Notice(w, noticeId, "Failed to create issue.")
			} else if errors.Is(err, isvc.ErrPDSFail) {
				p.Notice(w, noticeId, "Failed to create issue.")
			} else {
				p.Notice(w, noticeId, "Failed to create issue.")
			}
		}
		p.HxLocation(w, "/")
	}
}
