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
}

// TODO: this gathers heterogenous events from different sources and aggregates
// them in code; if we did this entirely in sql, we could order and limit and paginate easily
func MakeTimeline(e Execer) ([]TimelineEvent, error) {
	var events []TimelineEvent
	limit := 50

	repos, err := GetAllRepos(e, limit)
	if err != nil {
		return nil, err
	}

	follows, err := GetAllFollows(e, limit)
	if err != nil {
		return nil, err
	}

	stars, err := GetAllStars(e, limit)
	if err != nil {
		return nil, err
	}

	for _, repo := range repos {
		events = append(events, TimelineEvent{
			Repo:    &repo,
			EventAt: repo.Created,
		})
	}

	for _, follow := range follows {
		events = append(events, TimelineEvent{
			Follow:  &follow,
			EventAt: follow.FollowedAt,
		})
	}

	for _, star := range stars {
		events = append(events, TimelineEvent{
			Star:    &star,
			EventAt: star.Created,
		})
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].EventAt.After(events[j].EventAt)
	})

	// Limit the slice to 100 events
	if len(events) > limit {
		events = events[:limit]
	}

	return events, nil
}
