package db

import (
	"context"
	"sort"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
)

type TimelineEvent struct {
	*Repo
	*Follow
	*Star

	EventAt time.Time

	// optional: populate only if Repo is a fork
	Source *Repo
}

// TODO: this gathers heterogenous events from different sources and aggregates
// them in code; if we did this entirely in sql, we could order and limit and paginate easily
func MakeTimeline(ctx context.Context, e Execer) ([]TimelineEvent, error) {
	span := trace.SpanFromContext(ctx)
	defer span.End()

	var events []TimelineEvent
	limit := 50

	span.SetAttributes(attribute.Int("timeline.limit", limit))

	repos, err := GetAllRepos(ctx, e, limit)
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.String("error.from", "GetAllRepos"))
		return nil, err
	}
	span.SetAttributes(attribute.Int("timeline.repos.count", len(repos)))

	follows, err := GetAllFollows(e, limit)
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.String("error.from", "GetAllFollows"))
		return nil, err
	}
	span.SetAttributes(attribute.Int("timeline.follows.count", len(follows)))

	stars, err := GetAllStars(e, limit)
	if err != nil {
		span.RecordError(err)
		span.SetAttributes(attribute.String("error.from", "GetAllStars"))
		return nil, err
	}
	span.SetAttributes(attribute.Int("timeline.stars.count", len(stars)))

	for _, repo := range repos {
		var sourceRepo *Repo
		if repo.Source != "" {
			sourceRepo, err = GetRepoByAtUri(ctx, e, repo.Source)
			if err != nil {
				span.RecordError(err)
				span.SetAttributes(
					attribute.String("error.from", "GetRepoByAtUri"),
					attribute.String("repo.source", repo.Source),
				)
				return nil, err
			}
		}

		events = append(events, TimelineEvent{
			Repo:    &repo,
			EventAt: repo.Created,
			Source:  sourceRepo,
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

	span.SetAttributes(attribute.Int("timeline.events.total", len(events)))

	return events, nil
}
