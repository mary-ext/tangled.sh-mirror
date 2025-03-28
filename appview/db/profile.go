package db

import (
	"sort"
	"time"
)

type ProfileTimelineEvent struct {
	EventAt time.Time
	Type    string
	*Issue
	*Pull
	*Repo
}

func MakeProfileTimeline(e Execer, forDid string) ([]ProfileTimelineEvent, error) {
	timeline := []ProfileTimelineEvent{}
	limit := 30

	pulls, err := GetPullsByOwnerDid(e, forDid)
	if err != nil {
		return timeline, err
	}

	for _, pull := range pulls {
		repo, err := GetRepoByAtUri(e, string(pull.RepoAt))
		if err != nil {
			return timeline, err
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
		return timeline, err
	}

	for _, issue := range issues {
		repo, err := GetRepoByAtUri(e, string(issue.RepoAt))
		if err != nil {
			return timeline, err
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
		return timeline, err
	}

	for _, repo := range repos {
		timeline = append(timeline, ProfileTimelineEvent{
			EventAt: repo.Created,
			Type:    "repo",
			Repo:    &repo,
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
