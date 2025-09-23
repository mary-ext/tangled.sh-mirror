package models

import (
	"fmt"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.org/core/api/tangled"
)

type Profile struct {
	// ids
	ID  int
	Did string

	// data
	Description    string
	IncludeBluesky bool
	Location       string
	Links          [5]string
	Stats          [2]VanityStat
	PinnedRepos    [6]syntax.ATURI
}

func (p Profile) IsLinksEmpty() bool {
	for _, l := range p.Links {
		if l != "" {
			return false
		}
	}
	return true
}

func (p Profile) IsStatsEmpty() bool {
	for _, s := range p.Stats {
		if s.Kind != "" {
			return false
		}
	}
	return true
}

func (p Profile) IsPinnedReposEmpty() bool {
	for _, r := range p.PinnedRepos {
		if r != "" {
			return false
		}
	}
	return true
}

type VanityStatKind string

const (
	VanityStatMergedPRCount    VanityStatKind = "merged-pull-request-count"
	VanityStatClosedPRCount    VanityStatKind = "closed-pull-request-count"
	VanityStatOpenPRCount      VanityStatKind = "open-pull-request-count"
	VanityStatOpenIssueCount   VanityStatKind = "open-issue-count"
	VanityStatClosedIssueCount VanityStatKind = "closed-issue-count"
	VanityStatRepositoryCount  VanityStatKind = "repository-count"
)

func (v VanityStatKind) String() string {
	switch v {
	case VanityStatMergedPRCount:
		return "Merged PRs"
	case VanityStatClosedPRCount:
		return "Closed PRs"
	case VanityStatOpenPRCount:
		return "Open PRs"
	case VanityStatOpenIssueCount:
		return "Open Issues"
	case VanityStatClosedIssueCount:
		return "Closed Issues"
	case VanityStatRepositoryCount:
		return "Repositories"
	}
	return ""
}

type VanityStat struct {
	Kind  VanityStatKind
	Value uint64
}

func (p *Profile) ProfileAt() syntax.ATURI {
	return syntax.ATURI(fmt.Sprintf("at://%s/%s/%s", p.Did, tangled.ActorProfileNSID, "self"))
}

type RepoEvent struct {
	Repo   *Repo
	Source *Repo
}

type ProfileTimeline struct {
	ByMonth []ByMonth
}

func (p *ProfileTimeline) IsEmpty() bool {
	if p == nil {
		return true
	}

	for _, m := range p.ByMonth {
		if !m.IsEmpty() {
			return false
		}
	}

	return true
}

type ByMonth struct {
	RepoEvents  []RepoEvent
	IssueEvents IssueEvents
	PullEvents  PullEvents
}

func (b ByMonth) IsEmpty() bool {
	return len(b.RepoEvents) == 0 &&
		len(b.IssueEvents.Items) == 0 &&
		len(b.PullEvents.Items) == 0
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
