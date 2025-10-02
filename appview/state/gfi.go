package state

import (
	"fmt"
	"log"
	"net/http"
	"sort"

	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pages"
	"tangled.org/core/appview/pagination"
	"tangled.org/core/consts"
)

func (s *State) GoodFirstIssues(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	page, ok := r.Context().Value("page").(pagination.Page)
	if !ok {
		page = pagination.FirstPage()
	}

	goodFirstIssueLabel := fmt.Sprintf("at://%s/%s/%s", consts.TangledDid, tangled.LabelDefinitionNSID, "good-first-issue")

	repoLabels, err := db.GetRepoLabels(s.db, db.FilterEq("label_at", goodFirstIssueLabel))
	if err != nil {
		log.Println("failed to get repo labels", err)
		s.pages.Error503(w)
		return
	}

	if len(repoLabels) == 0 {
		s.pages.GoodFirstIssues(w, pages.GoodFirstIssuesParams{
			LoggedInUser: user,
			RepoGroups:   []*models.RepoGroup{},
			LabelDefs:    make(map[string]*models.LabelDefinition),
			Page:         page,
		})
		return
	}

	repoUris := make([]string, 0, len(repoLabels))
	for _, rl := range repoLabels {
		repoUris = append(repoUris, rl.RepoAt.String())
	}

	allIssues, err := db.GetIssues(
		s.db,
		db.FilterIn("repo_at", repoUris),
		db.FilterEq("open", 1),
	)
	if err != nil {
		log.Println("failed to get issues", err)
		s.pages.Error503(w)
		return
	}

	var goodFirstIssues []models.Issue
	for _, issue := range allIssues {
		if issue.Labels.ContainsLabel(goodFirstIssueLabel) {
			goodFirstIssues = append(goodFirstIssues, issue)
		}
	}

	repoGroups := make(map[string]*models.RepoGroup)
	for _, issue := range goodFirstIssues {
		repoKey := fmt.Sprintf("%s/%s", issue.Repo.Did, issue.Repo.Name)
		if group, exists := repoGroups[repoKey]; exists {
			group.Issues = append(group.Issues, issue)
		} else {
			repoGroups[repoKey] = &models.RepoGroup{
				Repo:   issue.Repo,
				Issues: []models.Issue{issue},
			}
		}
	}

	var sortedGroups []*models.RepoGroup
	for _, group := range repoGroups {
		sortedGroups = append(sortedGroups, group)
	}

	sort.Slice(sortedGroups, func(i, j int) bool {
		return sortedGroups[i].Repo.Name < sortedGroups[j].Repo.Name
	})

	groupStart := page.Offset
	groupEnd := page.Offset + page.Limit
	if groupStart > len(sortedGroups) {
		groupStart = len(sortedGroups)
	}
	if groupEnd > len(sortedGroups) {
		groupEnd = len(sortedGroups)
	}

	paginatedGroups := sortedGroups[groupStart:groupEnd]

	var allIssuesFromGroups []models.Issue
	for _, group := range paginatedGroups {
		allIssuesFromGroups = append(allIssuesFromGroups, group.Issues...)
	}

	var allLabelDefs []models.LabelDefinition
	if len(allIssuesFromGroups) > 0 {
		labelDefUris := make(map[string]bool)
		for _, issue := range allIssuesFromGroups {
			for labelDefUri := range issue.Labels.Inner() {
				labelDefUris[labelDefUri] = true
			}
		}

		uriList := make([]string, 0, len(labelDefUris))
		for uri := range labelDefUris {
			uriList = append(uriList, uri)
		}

		if len(uriList) > 0 {
			allLabelDefs, err = db.GetLabelDefinitions(s.db, db.FilterIn("at_uri", uriList))
			if err != nil {
				log.Println("failed to fetch labels", err)
			}
		}
	}

	labelDefsMap := make(map[string]*models.LabelDefinition)
	for i := range allLabelDefs {
		labelDefsMap[allLabelDefs[i].AtUri().String()] = &allLabelDefs[i]
	}

	s.pages.GoodFirstIssues(w, pages.GoodFirstIssuesParams{
		LoggedInUser: user,
		RepoGroups:   paginatedGroups,
		LabelDefs:    labelDefsMap,
		Page:         page,
	})
}
