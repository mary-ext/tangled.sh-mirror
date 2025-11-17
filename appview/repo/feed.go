package repo

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"time"

	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pagination"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	"github.com/gorilla/feeds"
)

func (rp *Repo) getRepoFeed(ctx context.Context, repo *models.Repo, ownerSlashRepo string) (*feeds.Feed, error) {
	const feedLimitPerType = 100

	pulls, err := db.GetPullsWithLimit(rp.db, feedLimitPerType, db.FilterEq("repo_at", repo.RepoAt()))
	if err != nil {
		return nil, err
	}

	issues, err := db.GetIssuesPaginated(
		rp.db,
		pagination.Page{Limit: feedLimitPerType},
		db.FilterEq("repo_at", repo.RepoAt()),
	)
	if err != nil {
		return nil, err
	}

	feed := &feeds.Feed{
		Title:   fmt.Sprintf("activity feed for @%s", ownerSlashRepo),
		Link:    &feeds.Link{Href: fmt.Sprintf("%s/%s", rp.config.Core.AppviewHost, ownerSlashRepo), Type: "text/html", Rel: "alternate"},
		Items:   make([]*feeds.Item, 0),
		Updated: time.UnixMilli(0),
	}

	for _, pull := range pulls {
		items, err := rp.createPullItems(ctx, pull, repo, ownerSlashRepo)
		if err != nil {
			return nil, err
		}
		feed.Items = append(feed.Items, items...)
	}

	for _, issue := range issues {
		item, err := rp.createIssueItem(ctx, issue, repo, ownerSlashRepo)
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

func (rp *Repo) createPullItems(ctx context.Context, pull *models.Pull, repo *models.Repo, ownerSlashRepo string) ([]*feeds.Item, error) {
	owner, err := rp.idResolver.ResolveIdent(ctx, pull.OwnerDid)
	if err != nil {
		return nil, err
	}

	var items []*feeds.Item

	state := rp.getPullState(pull)
	description := rp.buildPullDescription(owner.Handle, state, pull, ownerSlashRepo)

	mainItem := &feeds.Item{
		Title:       fmt.Sprintf("[PR #%d] %s", pull.PullId, pull.Title),
		Description: description,
		Link:        &feeds.Link{Href: fmt.Sprintf("%s/%s/pulls/%d", rp.config.Core.AppviewHost, ownerSlashRepo, pull.PullId)},
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
			Description: fmt.Sprintf("@%s submitted changes (at round #%d) on PR #%d in @%s", owner.Handle, round.RoundNumber, pull.PullId, ownerSlashRepo),
			Link:        &feeds.Link{Href: fmt.Sprintf("%s/%s/pulls/%d/round/%d/", rp.config.Core.AppviewHost, ownerSlashRepo, pull.PullId, round.RoundNumber)},
			Created:     round.Created,
			Author:      &feeds.Author{Name: fmt.Sprintf("@%s", owner.Handle)},
		}
		items = append(items, roundItem)
	}

	return items, nil
}

func (rp *Repo) createIssueItem(ctx context.Context, issue models.Issue, repo *models.Repo, ownerSlashRepo string) (*feeds.Item, error) {
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
		Description: fmt.Sprintf("@%s %s issue #%d in @%s", owner.Handle, state, issue.IssueId, ownerSlashRepo),
		Link:        &feeds.Link{Href: fmt.Sprintf("%s/%s/issues/%d", rp.config.Core.AppviewHost, ownerSlashRepo, issue.IssueId)},
		Created:     issue.Created,
		Author:      &feeds.Author{Name: fmt.Sprintf("@%s", owner.Handle)},
	}, nil
}

func (rp *Repo) getPullState(pull *models.Pull) string {
	if pull.State == models.PullOpen {
		return "opened"
	}
	return pull.State.String()
}

func (rp *Repo) buildPullDescription(handle syntax.Handle, state string, pull *models.Pull, repoName string) string {
	base := fmt.Sprintf("@%s %s pull request #%d", handle, state, pull.PullId)

	if pull.State == models.PullMerged {
		return fmt.Sprintf("%s (on round #%d) in %s", base, pull.LastRoundNumber(), repoName)
	}

	return fmt.Sprintf("%s in %s", base, repoName)
}

func (rp *Repo) AtomFeed(w http.ResponseWriter, r *http.Request) {
	f, err := rp.repoResolver.Resolve(r)
	if err != nil {
		log.Println("failed to fully resolve repo:", err)
		return
	}
	repoOwnerId, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok || repoOwnerId.Handle.IsInvalidHandle() {
		log.Println("failed to get resolved repo owner id")
		return
	}
	ownerSlashRepo := repoOwnerId.Handle.String() + "/" + f.Name

	feed, err := rp.getRepoFeed(r.Context(), f, ownerSlashRepo)
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
