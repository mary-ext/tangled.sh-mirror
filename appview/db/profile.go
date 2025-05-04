package db

import (
	"database/sql"
	"fmt"
	"log"
	"time"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"tangled.sh/tangled.sh/core/api/tangled"
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

const TimeframeMonths = 7

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

		if currentMonth-pullMonth >= TimeframeMonths {
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

		if currentMonth-issueMonth >= TimeframeMonths {
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

		if currentMonth-repoMonth >= TimeframeMonths {
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

	return &timeline, nil
}

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

func UpsertProfile(tx *sql.Tx, profile *Profile) error {
	defer tx.Rollback()

	// update links
	_, err := tx.Exec(`delete from profile_links where did = ?`, profile.Did)
	if err != nil {
		return err
	}
	// update vanity stats
	_, err = tx.Exec(`delete from profile_stats where did = ?`, profile.Did)
	if err != nil {
		return err
	}

	// update pinned repos
	_, err = tx.Exec(`delete from profile_pinned_repositories where did = ?`, profile.Did)
	if err != nil {
		return err
	}

	includeBskyValue := 0
	if profile.IncludeBluesky {
		includeBskyValue = 1
	}

	_, err = tx.Exec(
		`insert or replace into profile (
			did,
			description,
			include_bluesky,
			location
		)
		values (?, ?, ?, ?)`,
		profile.Did,
		profile.Description,
		includeBskyValue,
		profile.Location,
	)

	if err != nil {
		log.Println("profile", "err", err)
		return err
	}

	for _, link := range profile.Links {
		if link == "" {
			continue
		}

		_, err := tx.Exec(
			`insert into profile_links (did, link) values (?, ?)`,
			profile.Did,
			link,
		)

		if err != nil {
			log.Println("profile_links", "err", err)
			return err
		}
	}

	for _, v := range profile.Stats {
		if v.Kind == "" {
			continue
		}

		_, err := tx.Exec(
			`insert into profile_stats (did, kind) values (?, ?)`,
			profile.Did,
			v.Kind,
		)

		if err != nil {
			log.Println("profile_stats", "err", err)
			return err
		}
	}

	for _, pin := range profile.PinnedRepos {
		if pin == "" {
			continue
		}

		_, err := tx.Exec(
			`insert into profile_pinned_repositories (did, at_uri) values (?, ?)`,
			profile.Did,
			pin,
		)

		if err != nil {
			log.Println("profile_pinned_repositories")
			return err
		}
	}

	return tx.Commit()
}

func GetProfile(e Execer, did string) (*Profile, error) {
	var profile Profile
	profile.Did = did

	includeBluesky := 0
	err := e.QueryRow(
		`select description, include_bluesky, location from profile where did = ?`,
		did,
	).Scan(&profile.Description, &includeBluesky, &profile.Location)
	if err == sql.ErrNoRows {
		profile := Profile{}
		profile.Did = did
		return &profile, nil
	}

	if err != nil {
		return nil, err
	}

	if includeBluesky != 0 {
		profile.IncludeBluesky = true
	}

	rows, err := e.Query(`select link from profile_links where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	i := 0
	for rows.Next() {
		if err := rows.Scan(&profile.Links[i]); err != nil {
			return nil, err
		}
		i++
	}

	rows, err = e.Query(`select kind from profile_stats where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	i = 0
	for rows.Next() {
		if err := rows.Scan(&profile.Stats[i].Kind); err != nil {
			return nil, err
		}
		value, err := GetVanityStat(e, profile.Did, profile.Stats[i].Kind)
		if err != nil {
			return nil, err
		}
		profile.Stats[i].Value = value
		i++
	}

	rows, err = e.Query(`select at_uri from profile_pinned_repositories where did = ?`, did)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	i = 0
	for rows.Next() {
		if err := rows.Scan(&profile.PinnedRepos[i]); err != nil {
			return nil, err
		}
		i++
	}

	return &profile, nil
}

func GetVanityStat(e Execer, did string, stat VanityStatKind) (uint64, error) {
	query := ""
	var args []any
	switch stat {
	case VanityStatMergedPRCount:
		query = `select count(id) from pulls where owner_did = ? and state = ?`
		args = append(args, did, PullMerged)
	case VanityStatClosedPRCount:
		query = `select count(id) from pulls where owner_did = ? and state = ?`
		args = append(args, did, PullClosed)
	case VanityStatOpenPRCount:
		query = `select count(id) from pulls where owner_did = ? and state = ?`
		args = append(args, did, PullOpen)
	case VanityStatOpenIssueCount:
		query = `select count(id) from issues where owner_did = ? and open = 1`
		args = append(args, did)
	case VanityStatClosedIssueCount:
		query = `select count(id) from issues where owner_did = ? and open = 0`
		args = append(args, did)
	case VanityStatRepositoryCount:
		query = `select count(id) from repos where did = ?`
		args = append(args, did)
	}

	var result uint64
	err := e.QueryRow(query, args...).Scan(&result)
	if err != nil {
		return 0, err
	}

	return result, nil
}
