package db

import (
	"log"
	"sort"
	"time"
)

type TimelineEvent struct {
	*Repo
	*Follow
	EventAt time.Time
}

func (d *DB) MakeTimeline() ([]TimelineEvent, error) {
	var events []TimelineEvent

	repos, err := d.GetAllRepos()
	if err != nil {
		return nil, err
	}

	follows, err := d.GetAllFollows()
	if err != nil {
		return nil, err
	}

	for _, repo := range repos {
		log.Println(repo.Created)
		events = append(events, TimelineEvent{
			Repo:    &repo,
			Follow:  nil,
			EventAt: repo.Created,
		})
	}

	for _, follow := range follows {
		events = append(events, TimelineEvent{
			Repo:    nil,
			Follow:  &follow,
			EventAt: follow.FollowedAt,
		})
	}

	sort.Slice(events, func(i, j int) bool {
		return events[i].EventAt.After(events[j].EventAt)
	})

	return events, nil
}
