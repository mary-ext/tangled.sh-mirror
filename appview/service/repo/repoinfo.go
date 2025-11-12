package repo

import (
	"context"

	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/oauth"
	"tangled.org/core/appview/pages/repoinfo"
)

// GetRepoInfo converts given `Repo` to `RepoInfo` object.
// The `user` can be nil.
func (s *Service) GetRepoInfo(ctx context.Context, baseRepo *models.Repo, user *oauth.User) (*repoinfo.RepoInfo, error) {
	var (
		repoAt    = baseRepo.RepoAt()
		isStarred = false
		roles     = repoinfo.RolesInRepo{}
	)
	if user != nil {
		isStarred = db.GetStarStatus(s.db, user.Did, repoAt)
		roles.Roles = s.enforcer.GetPermissionsInRepo(user.Did, baseRepo.Knot, baseRepo.DidSlashRepo())
	}

	stats := baseRepo.RepoStats
	if stats == nil {
		starCount, err := db.GetStarCount(s.db, repoAt)
		if err != nil {
			return nil, err
		}
		issueCount, err := db.GetIssueCount(s.db, repoAt)
		if err != nil {
			return nil, err
		}
		pullCount, err := db.GetPullCount(s.db, repoAt)
		if err != nil {
			return nil, err
		}
		stats = &models.RepoStats{
			StarCount:  starCount,
			IssueCount: issueCount,
			PullCount:  pullCount,
		}
	}

	var sourceRepo *models.Repo
	var err error
	if baseRepo.Source != "" {
		sourceRepo, err = db.GetRepoByAtUri(s.db, baseRepo.Source)
		if err != nil {
			return nil, err
		}
	}

	repoInfo := &repoinfo.RepoInfo{
		// ok this is basically a models.Repo
		OwnerDid:    baseRepo.Did,
		OwnerHandle: "", // TODO: shouldn't use
		Name:        baseRepo.Name,
		Rkey:        baseRepo.Rkey,
		Description: baseRepo.Description,
		Website:     baseRepo.Website,
		Topics:      baseRepo.Topics,
		Knot:        baseRepo.Knot,
		Spindle:     baseRepo.Spindle,
		Stats:       *stats,

		// fork repo upstream
		Source: sourceRepo,

		// repo path (context)
		CurrentDir: "",
		Ref:        "",

		// info related to the session
		IsStarred: isStarred,
		Roles:     roles,
	}

	return repoInfo, nil
}
