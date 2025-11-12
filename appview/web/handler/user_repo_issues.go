package handler

import (
	"net/http"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/pagination"
	isvc "tangled.org/core/appview/service/issue"
	rsvc "tangled.org/core/appview/service/repo"
	"tangled.org/core/appview/session"
	"tangled.org/core/appview/web/request"
	"tangled.org/core/log"
)

func RepoIssues(is isvc.Service, rs rsvc.Service, p *pages.Pages, d *db.DB) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		ctx := r.Context()
		l := log.FromContext(ctx).With("handler", "RepoIssues")
		repo, ok := request.RepoFromContext(ctx)
		if !ok {
			l.Error("malformed request")
			p.Error503(w)
			return
		}

		query := r.URL.Query()
		searchOpts := models.IssueSearchOptions{
			RepoAt:  repo.RepoAt().String(),
			Keyword: query.Get("q"),
			IsOpen:  query.Get("state") != "closed",
			Page:    pagination.FromContext(ctx),
		}

		issues, err := is.GetIssues(ctx, repo, searchOpts)
		if err != nil {
			l.Error("failed to get issues")
			p.Error503(w)
			return
		}

		// render page
		err = func() error {
			user := session.UserFromContext(ctx)
			repoinfo, err := rs.GetRepoInfo(ctx, repo, user)
			if err != nil {
				return err
			}
			labelDefs, err := db.GetLabelDefinitions(
				d,
				db.FilterIn("at_uri", repo.Labels),
				db.FilterContains("scope", tangled.RepoIssueNSID),
			)
			if err != nil {
				return err
			}
			defs := make(map[string]*models.LabelDefinition)
			for _, l := range labelDefs {
				defs[l.AtUri().String()] = &l
			}
			return p.RepoIssues(w, pages.RepoIssuesParams{
				LoggedInUser: user,
				RepoInfo:     *repoinfo,

				Issues:          issues,
				LabelDefs:       defs,
				FilteringByOpen: searchOpts.IsOpen,
				FilterQuery:     searchOpts.Keyword,
				Page:            searchOpts.Page,
			})
		}()
		if err != nil {
			l.Error("failed to render", "err", err)
			p.Error503(w)
			return
		}
	}
}
