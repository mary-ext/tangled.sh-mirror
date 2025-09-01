package repo

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"time"

	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/reporesolver"

	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/gorilla/feeds"
)

func (rp *Repo) getRepoFeed(ctx context.Context, f *reporesolver.ResolvedRepo) (*feeds.Feed, error) {
	const feedLimitPerType = 100

	pulls, err := db.GetPullsWithLimit(rp.db, feedLimitPerType, db.FilterEq("repo_at", f.RepoAt()))
	if err != nil {
		return nil, err
	}

	issues, err := db.GetIssuesWithLimit(rp.db, feedLimitPerType, db.FilterEq("repo_at", f.RepoAt()))
	if err != nil {
		return nil, err
	}

	feed := &feeds.Feed{
		Title:   fmt.Sprintf("activity feed for %s", f.OwnerSlashRepo()),
		Link:    &feeds.Link{Href: fmt.Sprintf("%s/%s", rp.config.Core.AppviewHost, f.OwnerSlashRepo()), Type: "text/html", Rel: "alternate"},
		Items:   make([]*feeds.Item, 0),
		Updated: time.UnixMilli(0),
	}

	for _, pull := range pulls {
		items, err := rp.createPullItems(ctx, pull, f)
		if err != nil {
			return nil, err
		}
		feed.Items = append(feed.Items, items...)
	}

	for _, issue := range issues {
		item, err := rp.createIssueItem(ctx, issue, f)
		if err != nil {
			return nil, err
		}
		feed.Items = append(feed.Items, item)
	}

	slices.SortFunc(feed.Items, func(a, b *feeds.Item) int {
		if a.Created.After(b.Created) {
			return -1
		}
		return 1
	})

	if len(feed.Items) > 0 {
		feed.Updated = feed.Items[0].Created
	}

	return feed, nil
}

func (rp *Repo) createPullItems(ctx context.Context, pull *db.Pull, f *reporesolver.ResolvedRepo) ([]*feeds.Item, error) {
	owner, err := rp.idResolver.ResolveIdent(ctx, pull.OwnerDid)
	if err != nil {
		return nil, err
	}

	var items []*feeds.Item

	state := rp.getPullState(pull)
	description := rp.buildPullDescription(owner.Handle, state, pull, f.OwnerSlashRepo())

	mainItem := &feeds.Item{
		Title:       fmt.Sprintf("[PR #%d] %s", pull.PullId, pull.Title),
		Description: description,
		Link:        &feeds.Link{Href: fmt.Sprintf("%s/%s/pulls/%d", rp.config.Core.AppviewHost, f.OwnerSlashRepo(), pull.PullId)},
		Created:     pull.Created,
		Author:      &feeds.Author{Name: fmt.Sprintf("@%s", owner.Handle)},
	}
	items = append(items, mainItem)

	for _, round := range pull.Submissions {
		if round == nil || round.RoundNumber == 0 {
			continue
		}

		roundItem := &feeds.Item{
			Title:       fmt.Sprintf("[PR #%d] %s (round #%d)", pull.PullId, pull.Title, round.RoundNumber),
			Description: fmt.Sprintf("@%s submitted changes (at round #%d) on PR #%d in %s", owner.Handle, round.RoundNumber, pull.PullId, f.OwnerSlashRepo()),
			Link:        &feeds.Link{Href: fmt.Sprintf("%s/%s/pulls/%d/round/%d/", rp.config.Core.AppviewHost, f.OwnerSlashRepo(), pull.PullId, round.RoundNumber)},
			Created:     round.Created,
			Author:      &feeds.Author{Name: fmt.Sprintf("@%s", owner.Handle)},
		}
		items = append(items, roundItem)
	}

	return items, nil
}

func (rp *Repo) createIssueItem(ctx context.Context, issue db.Issue, f *reporesolver.ResolvedRepo) (*feeds.Item, error) {
	owner, err := rp.idResolver.ResolveIdent(ctx, issue.Did)
	if err != nil {
		return nil, err
	}

	state := "closed"
	if issue.Open {
		state = "opened"
	}

	return &feeds.Item{
		Title:       fmt.Sprintf("[Issue #%d] %s", issue.IssueId, issue.Title),
		Description: fmt.Sprintf("@%s %s issue #%d in %s", owner.Handle, state, issue.IssueId, f.OwnerSlashRepo()),
		Link:        &feeds.Link{Href: fmt.Sprintf("%s/%s/issues/%d", rp.config.Core.AppviewHost, f.OwnerSlashRepo(), issue.IssueId)},
		Created:     issue.Created,
		Author:      &feeds.Author{Name: fmt.Sprintf("@%s", owner.Handle)},
	}, nil
}

func (rp *Repo) getPullState(pull *db.Pull) string {
	if pull.State == db.PullOpen {
		return "opened"
	}
	return pull.State.String()
}

func (rp *Repo) buildPullDescription(handle syntax.Handle, state string, pull *db.Pull, repoName string) string {
	base := fmt.Sprintf("@%s %s pull request #%d", handle, state, pull.PullId)

	if pull.State == db.PullMerged {
		return fmt.Sprintf("%s (on round #%d) in %s", base, pull.LastRoundNumber(), repoName)
	}

	return fmt.Sprintf("%s in %s", base, repoName)
}

func (rp *Repo) RepoAtomFeed(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to fully resolve repo:", err)
		return
	}

	feed, err := rp.getRepoFeed(r.Context(), f)
	if err != nil {
		log.Println("failed to get repo feed:", err)
		rp.pages.Error500(w)
		return
	}

	atom, err := feed.ToAtom()
	if err != nil {
		rp.pages.Error500(w)
		return
	}

	w.Header().Set("content-type", "application/atom+xml")
	w.Write([]byte(atom))
}
