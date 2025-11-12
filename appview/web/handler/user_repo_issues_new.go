package handler

import (
	"errors"
	"fmt"
	"net/http"

	"tangled.org/core/appview/pages"
	isvc "tangled.org/core/appview/service/issue"
	rsvc "tangled.org/core/appview/service/repo"
	"tangled.org/core/appview/session"
	"tangled.org/core/appview/web/request"
	"tangled.org/core/log"
)

func NewIssue(rs rsvc.Service, p *pages.Pages) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := log.FromContext(ctx).With("handler", "NewIssue")

		// render
		err := func() error {
			user := session.UserFromContext(ctx)
			repo, ok := request.RepoFromContext(ctx)
			if !ok {
				return fmt.Errorf("malformed request")
			}
			repoinfo, err := rs.GetRepoInfo(ctx, repo, user)
			if err != nil {
				return err
			}
			return p.RepoNewIssue(w, pages.RepoNewIssueParams{
				LoggedInUser: user,
				RepoInfo:     *repoinfo,
			})
		}()
		if err != nil {
			l.Error("failed to render", "err", err)
			p.Error503(w)
			return
		}
	}
}

func NewIssuePost(is isvc.Service, p *pages.Pages) http.HandlerFunc {
	noticeId := "issues"
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := log.FromContext(ctx).With("handler", "NewIssuePost")
		repo, ok := request.RepoFromContext(ctx)
		if !ok {
			l.Error("malformed request, failed to get repo")
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
