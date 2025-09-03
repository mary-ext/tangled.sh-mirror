package db

import (
	"sort"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
)

type TimelineEvent struct {
	*Repo
	*Follow
	*Star

	EventAt time.Time

	// optional: populate only if Repo is a fork
	Source *Repo

	// optional: populate only if event is Follow
	*Profile
	*FollowStats
	*FollowStatus

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

	stars, err := getTimelineStars(e, limit)
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

	var origRepos []Repo
	if args != nil {
		origRepos, err = GetRepos(e, 0, FilterIn("at_uri", args))
	}
	if err != nil {
		return nil, err
	}

	uriToRepo := make(map[string]Repo)
	for _, r := range origRepos {
		uriToRepo[r.RepoAt().String()] = r
	}

	var starStatuses map[string]bool
	if loggedInUserDid != "" {
		var repoAts []syntax.ATURI
		for _, r := range repos {
			repoAts = append(repoAts, r.RepoAt())
		}
		var err error
		starStatuses, err = GetStarStatuses(e, loggedInUserDid, repoAts)
		if err != nil {
			return nil, err
		}
	}

	var events []TimelineEvent
	for _, r := range repos {
		var source *Repo
		if r.Source != "" {
			if origRepo, ok := uriToRepo[r.Source]; ok {
				source = &origRepo
			}
		}

		var isStarred bool
		if starStatuses != nil {
			isStarred = starStatuses[r.RepoAt().String()]
		}

		var starCount int64
		if r.RepoStats != nil {
			starCount = int64(r.RepoStats.StarCount)
		}

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

func getTimelineStars(e Execer, limit int) ([]TimelineEvent, error) {
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

	var events []TimelineEvent
	for _, s := range stars {
		events = append(events, TimelineEvent{
			Star:    &s,
			EventAt: s.Created,
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

	var followStatuses map[string]FollowStatus
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

		followStatus := IsNotFollowing
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
