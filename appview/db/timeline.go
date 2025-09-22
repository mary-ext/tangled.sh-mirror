package db

import (
	"sort"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/appview/models"
)

type TimelineEvent struct {
	*models.Repo
	*models.Follow
	*Star

	EventAt time.Time

	// optional: populate only if Repo is a fork
	Source *models.Repo

	// optional: populate only if event is Follow
	*Profile
	*models.FollowStats
	*models.FollowStatus

	// optional: populate only if event is Repo
	IsStarred bool
	StarCount int64
}

// TODO: this gathers heterogenous events from different sources and aggregates
// them in code; if we did this entirely in sql, we could order and limit and paginate easily
func MakeTimeline(e Execer, limit int, loggedInUserDid string) ([]TimelineEvent, error) {
	var events []TimelineEvent

	repos, err := getTimelineRepos(e, limit, loggedInUserDid)
	if err != nil {
		return nil, err
	}

	stars, err := getTimelineStars(e, limit, loggedInUserDid)
	if err != nil {
		return nil, err
	}

	follows, err := getTimelineFollows(e, limit, loggedInUserDid)
	if err != nil {
		return nil, err
	}

	events = append(events, repos...)
	events = append(events, stars...)
	events = append(events, follows...)

	sort.Slice(events, func(i, j int) bool {
		return events[i].EventAt.After(events[j].EventAt)
	})

	// Limit the slice to 100 events
	if len(events) > limit {
		events = events[:limit]
	}

	return events, nil
}

func fetchStarStatuses(e Execer, loggedInUserDid string, repos []models.Repo) (map[string]bool, error) {
	if loggedInUserDid == "" {
		return nil, nil
	}

	var repoAts []syntax.ATURI
	for _, r := range repos {
		repoAts = append(repoAts, r.RepoAt())
	}

	return GetStarStatuses(e, loggedInUserDid, repoAts)
}

func getRepoStarInfo(repo *models.Repo, starStatuses map[string]bool) (bool, int64) {
	var isStarred bool
	if starStatuses != nil {
		isStarred = starStatuses[repo.RepoAt().String()]
	}

	var starCount int64
	if repo.RepoStats != nil {
		starCount = int64(repo.RepoStats.StarCount)
	}

	return isStarred, starCount
}

func getTimelineRepos(e Execer, limit int, loggedInUserDid string) ([]TimelineEvent, error) {
	repos, err := GetRepos(e, limit)
	if err != nil {
		return nil, err
	}

	// fetch all source repos
	var args []string
	for _, r := range repos {
		if r.Source != "" {
			args = append(args, r.Source)
		}
	}

	var origRepos []models.Repo
	if args != nil {
		origRepos, err = GetRepos(e, 0, FilterIn("at_uri", args))
	}
	if err != nil {
		return nil, err
	}

	uriToRepo := make(map[string]models.Repo)
	for _, r := range origRepos {
		uriToRepo[r.RepoAt().String()] = r
	}

	starStatuses, err := fetchStarStatuses(e, loggedInUserDid, repos)
	if err != nil {
		return nil, err
	}

	var events []TimelineEvent
	for _, r := range repos {
		var source *models.Repo
		if r.Source != "" {
			if origRepo, ok := uriToRepo[r.Source]; ok {
				source = &origRepo
			}
		}

		isStarred, starCount := getRepoStarInfo(&r, starStatuses)

		events = append(events, TimelineEvent{
			Repo:      &r,
			EventAt:   r.Created,
			Source:    source,
			IsStarred: isStarred,
			StarCount: starCount,
		})
	}

	return events, nil
}

func getTimelineStars(e Execer, limit int, loggedInUserDid string) ([]TimelineEvent, error) {
	stars, err := GetStars(e, limit)
	if err != nil {
		return nil, err
	}

	// filter star records without a repo
	n := 0
	for _, s := range stars {
		if s.Repo != nil {
			stars[n] = s
			n++
		}
	}
	stars = stars[:n]

	var repos []models.Repo
	for _, s := range stars {
		repos = append(repos, *s.Repo)
	}

	starStatuses, err := fetchStarStatuses(e, loggedInUserDid, repos)
	if err != nil {
		return nil, err
	}

	var events []TimelineEvent
	for _, s := range stars {
		isStarred, starCount := getRepoStarInfo(s.Repo, starStatuses)

		events = append(events, TimelineEvent{
			Star:      &s,
			EventAt:   s.Created,
			IsStarred: isStarred,
			StarCount: starCount,
		})
	}

	return events, nil
}

func getTimelineFollows(e Execer, limit int, loggedInUserDid string) ([]TimelineEvent, error) {
	follows, err := GetFollows(e, limit)
	if err != nil {
		return nil, err
	}

	var subjects []string
	for _, f := range follows {
		subjects = append(subjects, f.SubjectDid)
	}

	if subjects == nil {
		return nil, nil
	}

	profiles, err := GetProfiles(e, FilterIn("did", subjects))
	if err != nil {
		return nil, err
	}

	followStatMap, err := GetFollowerFollowingCounts(e, subjects)
	if err != nil {
		return nil, err
	}

	var followStatuses map[string]models.FollowStatus
	if loggedInUserDid != "" {
		followStatuses, err = GetFollowStatuses(e, loggedInUserDid, subjects)
		if err != nil {
			return nil, err
		}
	}

	var events []TimelineEvent
	for _, f := range follows {
		profile, _ := profiles[f.SubjectDid]
		followStatMap, _ := followStatMap[f.SubjectDid]

		followStatus := models.IsNotFollowing
		if followStatuses != nil {
			followStatus = followStatuses[f.SubjectDid]
		}

		events = append(events, TimelineEvent{
			Follow:       &f,
			Profile:      profile,
			FollowStats:  &followStatMap,
			FollowStatus: &followStatus,
			EventAt:      f.FollowedAt,
		})
	}

	return events, nil
}
