package db

import (
	"fmt"
	"sort"
	"time"
)

type ProfileTimelineEvent struct {
	EventAt time.Time
	Type    string
	*Issue
	*Pull
	*Repo

	// optional: populate only if Repo is a fork
	Source *Repo
}

func MakeProfileTimeline(e Execer, forDid string) ([]ProfileTimelineEvent, error) {
	timeline := []ProfileTimelineEvent{}
	limit := 30

	pulls, err := GetPullsByOwnerDid(e, forDid)
	if err != nil {
		return timeline, fmt.Errorf("error getting pulls by owner did: %w", err)
	}

	for _, pull := range pulls {
		repo, err := GetRepoByAtUri(e, string(pull.RepoAt))
		if err != nil {
			return timeline, fmt.Errorf("error getting repo by at uri: %w", err)
		}

		timeline = append(timeline, ProfileTimelineEvent{
			EventAt: pull.Created,
			Type:    "pull",
			Pull:    &pull,
			Repo:    repo,
		})
	}

	issues, err := GetIssuesByOwnerDid(e, forDid)
	if err != nil {
		return timeline, fmt.Errorf("error getting issues by owner did: %w", err)
	}

	for _, issue := range issues {
		repo, err := GetRepoByAtUri(e, string(issue.RepoAt))
		if err != nil {
			return timeline, fmt.Errorf("error getting repo by at uri: %w", err)
		}

		timeline = append(timeline, ProfileTimelineEvent{
			EventAt: *issue.Created,
			Type:    "issue",
			Issue:   &issue,
			Repo:    repo,
		})
	}

	repos, err := GetAllReposByDid(e, forDid)
	if err != nil {
		return timeline, fmt.Errorf("error getting all repos by did: %w", err)
	}

	for _, repo := range repos {
		var sourceRepo *Repo
		if repo.Source != "" {
			sourceRepo, err = GetRepoByAtUri(e, repo.Source)
			if err != nil {
				return nil, err
			}
		}

		timeline = append(timeline, ProfileTimelineEvent{
			EventAt: repo.Created,
			Type:    "repo",
			Repo:    &repo,
			Source:  sourceRepo,
		})
	}

	sort.Slice(timeline, func(i, j int) bool {
		return timeline[i].EventAt.After(timeline[j].EventAt)
	})

	if len(timeline) > limit {
		timeline = timeline[:limit]
	}

	return timeline, nil
}
