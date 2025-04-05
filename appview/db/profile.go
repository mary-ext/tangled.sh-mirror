package db

import (
	"encoding/json"
	"fmt"
	"time"
)

type RepoEvent struct {
	Repo   *Repo
	Source *Repo
}

type ProfileTimeline struct {
	ByMonth []ByMonth
}

type ByMonth struct {
	RepoEvents  []RepoEvent
	IssueEvents IssueEvents
	PullEvents  PullEvents
}

type IssueEvents struct {
	Items []*Issue
}

type IssueEventStats struct {
	Open   int
	Closed int
}

func (i IssueEvents) Stats() IssueEventStats {
	var open, closed int
	for _, issue := range i.Items {
		if issue.Open {
			open += 1
		} else {
			closed += 1
		}
	}

	return IssueEventStats{
		Open:   open,
		Closed: closed,
	}
}

type PullEvents struct {
	Items []*Pull
}

func (p PullEvents) Stats() PullEventStats {
	var open, merged, closed int
	for _, pull := range p.Items {
		switch pull.State {
		case PullOpen:
			open += 1
		case PullMerged:
			merged += 1
		case PullClosed:
			closed += 1
		}
	}

	return PullEventStats{
		Open:   open,
		Merged: merged,
		Closed: closed,
	}
}

type PullEventStats struct {
	Closed int
	Open   int
	Merged int
}

const TimeframeMonths = 3

func MakeProfileTimeline(e Execer, forDid string) (*ProfileTimeline, error) {
	timeline := ProfileTimeline{
		ByMonth: make([]ByMonth, TimeframeMonths),
	}
	currentMonth := time.Now().Month()
	timeframe := fmt.Sprintf("-%d months", TimeframeMonths)

	pulls, err := GetPullsByOwnerDid(e, forDid, timeframe)
	if err != nil {
		return nil, fmt.Errorf("error getting pulls by owner did: %w", err)
	}

	// group pulls by month
	for _, pull := range pulls {
		pullMonth := pull.Created.Month()

		if currentMonth-pullMonth > TimeframeMonths {
			// shouldn't happen; but times are weird
			continue
		}

		idx := currentMonth - pullMonth
		items := &timeline.ByMonth[idx].PullEvents.Items

		*items = append(*items, &pull)
	}

	issues, err := GetIssuesByOwnerDid(e, forDid, timeframe)
	if err != nil {
		return nil, fmt.Errorf("error getting issues by owner did: %w", err)
	}

	for _, issue := range issues {
		issueMonth := issue.Created.Month()

		if currentMonth-issueMonth > TimeframeMonths {
			// shouldn't happen; but times are weird
			continue
		}

		idx := currentMonth - issueMonth
		items := &timeline.ByMonth[idx].IssueEvents.Items

		*items = append(*items, &issue)
	}

	repos, err := GetAllReposByDid(e, forDid)
	if err != nil {
		return nil, fmt.Errorf("error getting all repos by did: %w", err)
	}

	for _, repo := range repos {
		// TODO: get this in the original query; requires COALESCE because nullable
		var sourceRepo *Repo
		if repo.Source != "" {
			sourceRepo, err = GetRepoByAtUri(e, repo.Source)
			if err != nil {
				return nil, err
			}
		}

		repoMonth := repo.Created.Month()

		if currentMonth-repoMonth > TimeframeMonths {
			// shouldn't happen; but times are weird
			continue
		}

		idx := currentMonth - repoMonth

		items := &timeline.ByMonth[idx].RepoEvents
		*items = append(*items, RepoEvent{
			Repo:   &repo,
			Source: sourceRepo,
		})
	}

	x, _ := json.MarshalIndent(timeline, "", "\t")
	fmt.Println(string(x))

	return &timeline, nil
}
