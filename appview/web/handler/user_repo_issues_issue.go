package handler

import (
	"net/http"

	"tangled.org/core/appview/pages"
	isvc "tangled.org/core/appview/service/issue"
	rsvc "tangled.org/core/appview/service/repo"
	"tangled.org/core/appview/session"
	"tangled.org/core/appview/web/request"
	"tangled.org/core/log"
)

func Issue(s isvc.Service, rs rsvc.Service, p *pages.Pages) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := log.FromContext(ctx).With("handler", "Issue")
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
			return p.RepoSingleIssue(w, pages.RepoSingleIssueParams{
				LoggedInUser: user,
				RepoInfo:     *repoinfo,
				Issue:        issue,
			})
		}()
		if err != nil {
			l.Error("failed to render", "err", err)
			p.Error503(w)
			return
		}
	}
}

func IssueDelete(s isvc.Service, p *pages.Pages) http.HandlerFunc {
	noticeId := "issue-actions-error"
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := log.FromContext(ctx).With("handler", "IssueDelete")
		issue, ok := request.IssueFromContext(ctx)
		if !ok {
			l.Error("failed to get issue")
			// TODO: 503 error with more detailed messages
			p.Error503(w)
			return
		}
		err := s.DeleteIssue(ctx, issue)
		if err != nil {
			p.Notice(w, noticeId, "failed to delete issue")
			return
		}
		p.HxLocation(w, "/")
	}
}
