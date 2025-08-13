package db

import (
	"sort"
	"time"
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
}

type FollowStats struct {
	Followers int
	Following int
}

const Limit = 50

// TODO: this gathers heterogenous events from different sources and aggregates
// them in code; if we did this entirely in sql, we could order and limit and paginate easily
func MakeTimeline(e Execer) ([]TimelineEvent, error) {
	var events []TimelineEvent

	repos, err := getTimelineRepos(e)
	if err != nil {
		return nil, err
	}

	stars, err := getTimelineStars(e)
	if err != nil {
		return nil, err
	}

	follows, err := getTimelineFollows(e)
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
	if len(events) > Limit {
		events = events[:Limit]
	}

	return events, nil
}

func getTimelineRepos(e Execer) ([]TimelineEvent, error) {
	repos, err := GetRepos(e, Limit)
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

	var events []TimelineEvent
	for _, r := range repos {
		var source *Repo
		if r.Source != "" {
			if origRepo, ok := uriToRepo[r.Source]; ok {
				source = &origRepo
			}
		}

		events = append(events, TimelineEvent{
			Repo:    &r,
			EventAt: r.Created,
			Source:  source,
		})
	}

	return events, nil
}

func getTimelineStars(e Execer) ([]TimelineEvent, error) {
	stars, err := GetStars(e, Limit)
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

func getTimelineFollows(e Execer) ([]TimelineEvent, error) {
	follows, err := GetAllFollows(e, Limit)
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

	profileMap := make(map[string]Profile)
	profiles, err := GetProfiles(e, FilterIn("did", subjects))
	if err != nil {
		return nil, err
	}
	for _, p := range profiles {
		profileMap[p.Did] = p
	}

	followStatMap := make(map[string]FollowStats)
	for _, s := range subjects {
		followers, following, err := GetFollowerFollowingCount(e, s)
		if err != nil {
			return nil, err
		}
		followStatMap[s] = FollowStats{
			Followers: followers,
			Following: following,
		}
	}

	var events []TimelineEvent
	for _, f := range follows {
		profile, _ := profileMap[f.SubjectDid]
		followStatMap, _ := followStatMap[f.SubjectDid]

		events = append(events, TimelineEvent{
			Follow:      &f,
			Profile:     &profile,
			FollowStats: &followStatMap,
			EventAt:     f.FollowedAt,
		})
	}

	return events, nil
}
