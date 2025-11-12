package handler

import (
	"errors"
	"net/http"

	"tangled.org/core/appview/pages"
	isvc "tangled.org/core/appview/service/issue"
	rsvc "tangled.org/core/appview/service/repo"
	"tangled.org/core/appview/session"
	"tangled.org/core/appview/web/request"
	"tangled.org/core/log"
)

func IssueEdit(is isvc.Service, rs rsvc.Service, p *pages.Pages) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := log.FromContext(ctx).With("handler", "IssueEdit")
		issue, ok := request.IssueFromContext(ctx)
		if !ok {
			l.Error("malformed request, failed to get issue")
			p.Error503(w)
			return
		}

		// render
		err := func() error {
			user := session.UserFromContext(ctx)
			repoinfo, err := rs.GetRepoInfo(ctx, issue.Repo, user)
			if err != nil {
				return err
			}
			return p.EditIssueFragment(w, pages.EditIssueParams{
				LoggedInUser: user,
				RepoInfo:     *repoinfo,

				Issue: issue,
			})
		}()
		if err != nil {
			l.Error("failed to render", "err", err)
			p.Error503(w)
			return
		}
	}
}

func IssueEditPost(is isvc.Service, p *pages.Pages) http.HandlerFunc {
	noticeId := "issues"
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := log.FromContext(ctx).With("handler", "IssueEdit")
		issue, ok := request.IssueFromContext(ctx)
		if !ok {
			l.Error("malformed request, failed to get issue")
			p.Error503(w)
			return
		}

		newIssue := *issue
		newIssue.Title = r.FormValue("title")
		newIssue.Body = r.FormValue("body")

		err := is.EditIssue(ctx, &newIssue)
		if err != nil {
			if errors.Is(err, isvc.ErrDatabaseFail) {
				p.Notice(w, noticeId, "Failed to edit issue.")
			} else if errors.Is(err, isvc.ErrPDSFail) {
				p.Notice(w, noticeId, "Failed to edit issue.")
			} else {
				p.Notice(w, noticeId, "Failed to edit issue.")
			}
		}

		p.HxRefresh(w)
	}
}
