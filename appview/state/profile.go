package state

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"slices"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/go-chi/chi/v5"
	"github.com/gorilla/feeds"
	"tangled.org/core/api/tangled"
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/models"
	"tangled.org/core/appview/pages"
)

func (s *State) Profile(w http.ResponseWriter, r *http.Request) {
	tabVal := r.URL.Query().Get("tab")
	switch tabVal {
	case "repos":
		s.reposPage(w, r)
	case "followers":
		s.followersPage(w, r)
	case "following":
		s.followingPage(w, r)
	case "starred":
		s.starredPage(w, r)
	case "strings":
		s.stringsPage(w, r)
	default:
		s.profileOverview(w, r)
	}
}

func (s *State) profile(r *http.Request) (*pages.ProfileCard, error) {
	didOrHandle := chi.URLParam(r, "user")
	if didOrHandle == "" {
		return nil, fmt.Errorf("empty DID or handle")
	}

	ident, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		return nil, fmt.Errorf("failed to resolve ID")
	}
	did := ident.DID.String()

	profile, err := db.GetProfile(s.db, did)
	if err != nil {
		return nil, fmt.Errorf("failed to get profile: %w", err)
	}

	repoCount, err := db.CountRepos(s.db, db.FilterEq("did", did))
	if err != nil {
		return nil, fmt.Errorf("failed to get repo count: %w", err)
	}

	stringCount, err := db.CountStrings(s.db, db.FilterEq("did", did))
	if err != nil {
		return nil, fmt.Errorf("failed to get string count: %w", err)
	}

	starredCount, err := db.CountStars(s.db, db.FilterEq("starred_by_did", did))
	if err != nil {
		return nil, fmt.Errorf("failed to get starred repo count: %w", err)
	}

	followStats, err := db.GetFollowerFollowingCount(s.db, did)
	if err != nil {
		return nil, fmt.Errorf("failed to get follower stats: %w", err)
	}

	loggedInUser := s.oauth.GetUser(r)
	followStatus := models.IsNotFollowing
	if loggedInUser != nil {
		followStatus = db.GetFollowStatus(s.db, loggedInUser.Did, did)
	}

	now := time.Now()
	startOfYear := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	punchcard, err := db.MakePunchcard(
		s.db,
		db.FilterEq("did", did),
		db.FilterGte("date", startOfYear.Format(time.DateOnly)),
		db.FilterLte("date", now.Format(time.DateOnly)),
	)
	if err != nil {
		return nil, fmt.Errorf("failed to get punchcard for %s: %w", did, err)
	}

	return &pages.ProfileCard{
		UserDid:      did,
		UserHandle:   ident.Handle.String(),
		Profile:      profile,
		FollowStatus: followStatus,
		Stats: pages.ProfileStats{
			RepoCount:      repoCount,
			StringCount:    stringCount,
			StarredCount:   starredCount,
			FollowersCount: followStats.Followers,
			FollowingCount: followStats.Following,
		},
		Punchcard: punchcard,
	}, nil
}

func (s *State) profileOverview(w http.ResponseWriter, r *http.Request) {
	l := s.logger.With("handler", "profileHomePage")

	profile, err := s.profile(r)
	if err != nil {
		l.Error("failed to build profile card", "err", err)
		s.pages.Error500(w)
		return
	}
	l = l.With("profileDid", profile.UserDid, "profileHandle", profile.UserHandle)

	repos, err := db.GetRepos(
		s.db,
		0,
		db.FilterEq("did", profile.UserDid),
	)
	if err != nil {
		l.Error("failed to fetch repos", "err", err)
	}

	// filter out ones that are pinned
	pinnedRepos := []models.Repo{}
	for i, r := range repos {
		// if this is a pinned repo, add it
		if slices.Contains(profile.Profile.PinnedRepos[:], r.RepoAt()) {
			pinnedRepos = append(pinnedRepos, r)
		}

		// if there are no saved pins, add the first 4 repos
		if profile.Profile.IsPinnedReposEmpty() && i < 4 {
			pinnedRepos = append(pinnedRepos, r)
		}
	}

	collaboratingRepos, err := db.CollaboratingIn(s.db, profile.UserDid)
	if err != nil {
		l.Error("failed to fetch collaborating repos", "err", err)
	}

	pinnedCollaboratingRepos := []models.Repo{}
	for _, r := range collaboratingRepos {
		// if this is a pinned repo, add it
		if slices.Contains(profile.Profile.PinnedRepos[:], r.RepoAt()) {
			pinnedCollaboratingRepos = append(pinnedCollaboratingRepos, r)
		}
	}

	timeline, err := db.MakeProfileTimeline(s.db, profile.UserDid)
	if err != nil {
		l.Error("failed to create timeline", "err", err)
	}

	s.pages.ProfileOverview(w, pages.ProfileOverviewParams{
		LoggedInUser:       s.oauth.GetUser(r),
		Card:               profile,
		Repos:              pinnedRepos,
		CollaboratingRepos: pinnedCollaboratingRepos,
		ProfileTimeline:    timeline,
	})
}

func (s *State) reposPage(w http.ResponseWriter, r *http.Request) {
	l := s.logger.With("handler", "reposPage")

	profile, err := s.profile(r)
	if err != nil {
		l.Error("failed to build profile card", "err", err)
		s.pages.Error500(w)
		return
	}
	l = l.With("profileDid", profile.UserDid, "profileHandle", profile.UserHandle)

	repos, err := db.GetRepos(
		s.db,
		0,
		db.FilterEq("did", profile.UserDid),
	)
	if err != nil {
		l.Error("failed to get repos", "err", err)
		s.pages.Error500(w)
		return
	}

	err = s.pages.ProfileRepos(w, pages.ProfileReposParams{
		LoggedInUser: s.oauth.GetUser(r),
		Repos:        repos,
		Card:         profile,
	})
}

func (s *State) starredPage(w http.ResponseWriter, r *http.Request) {
	l := s.logger.With("handler", "starredPage")

	profile, err := s.profile(r)
	if err != nil {
		l.Error("failed to build profile card", "err", err)
		s.pages.Error500(w)
		return
	}
	l = l.With("profileDid", profile.UserDid, "profileHandle", profile.UserHandle)

	stars, err := db.GetStars(s.db, 0, db.FilterEq("starred_by_did", profile.UserDid))
	if err != nil {
		l.Error("failed to get stars", "err", err)
		s.pages.Error500(w)
		return
	}
	var repoAts []string
	for _, s := range stars {
		repoAts = append(repoAts, string(s.RepoAt))
	}

	repos, err := db.GetRepos(
		s.db,
		0,
		db.FilterIn("at_uri", repoAts),
	)
	if err != nil {
		l.Error("failed to get repos", "err", err)
		s.pages.Error500(w)
		return
	}

	err = s.pages.ProfileStarred(w, pages.ProfileStarredParams{
		LoggedInUser: s.oauth.GetUser(r),
		Repos:        repos,
		Card:         profile,
	})
}

func (s *State) stringsPage(w http.ResponseWriter, r *http.Request) {
	l := s.logger.With("handler", "stringsPage")

	profile, err := s.profile(r)
	if err != nil {
		l.Error("failed to build profile card", "err", err)
		s.pages.Error500(w)
		return
	}
	l = l.With("profileDid", profile.UserDid, "profileHandle", profile.UserHandle)

	strings, err := db.GetStrings(s.db, 0, db.FilterEq("did", profile.UserDid))
	if err != nil {
		l.Error("failed to get strings", "err", err)
		s.pages.Error500(w)
		return
	}

	err = s.pages.ProfileStrings(w, pages.ProfileStringsParams{
		LoggedInUser: s.oauth.GetUser(r),
		Strings:      strings,
		Card:         profile,
	})
}

type FollowsPageParams struct {
	Follows []pages.FollowCard
	Card    *pages.ProfileCard
}

func (s *State) followPage(
	r *http.Request,
	fetchFollows func(db.Execer, string) ([]models.Follow, error),
	extractDid func(models.Follow) string,
) (*FollowsPageParams, error) {
	l := s.logger.With("handler", "reposPage")

	profile, err := s.profile(r)
	if err != nil {
		return nil, err
	}
	l = l.With("profileDid", profile.UserDid, "profileHandle", profile.UserHandle)

	loggedInUser := s.oauth.GetUser(r)
	params := FollowsPageParams{
		Card: profile,
	}

	follows, err := fetchFollows(s.db, profile.UserDid)
	if err != nil {
		l.Error("failed to fetch follows", "err", err)
		return &params, err
	}

	if len(follows) == 0 {
		return &params, nil
	}

	followDids := make([]string, 0, len(follows))
	for _, follow := range follows {
		followDids = append(followDids, extractDid(follow))
	}

	profiles, err := db.GetProfiles(s.db, db.FilterIn("did", followDids))
	if err != nil {
		l.Error("failed to get profiles", "followDids", followDids, "err", err)
		return &params, err
	}

	followStatsMap, err := db.GetFollowerFollowingCounts(s.db, followDids)
	if err != nil {
		log.Printf("getting follow counts for %s: %s", followDids, err)
	}

	loggedInUserFollowing := make(map[string]struct{})
	if loggedInUser != nil {
		following, err := db.GetFollowing(s.db, loggedInUser.Did)
		if err != nil {
			l.Error("failed to get follow list", "err", err, "loggedInUser", loggedInUser.Did)
			return &params, err
		}
		loggedInUserFollowing = make(map[string]struct{}, len(following))
		for _, follow := range following {
			loggedInUserFollowing[follow.SubjectDid] = struct{}{}
		}
	}

	followCards := make([]pages.FollowCard, len(follows))
	for i, did := range followDids {
		followStats := followStatsMap[did]
		followStatus := models.IsNotFollowing
		if _, exists := loggedInUserFollowing[did]; exists {
			followStatus = models.IsFollowing
		} else if loggedInUser != nil && loggedInUser.Did == did {
			followStatus = models.IsSelf
		}

		var profile *db.Profile
		if p, exists := profiles[did]; exists {
			profile = p
		} else {
			profile = &db.Profile{}
			profile.Did = did
		}
		followCards[i] = pages.FollowCard{
			UserDid:        did,
			FollowStatus:   followStatus,
			FollowersCount: followStats.Followers,
			FollowingCount: followStats.Following,
			Profile:        profile,
		}
	}

	params.Follows = followCards

	return &params, nil
}

func (s *State) followersPage(w http.ResponseWriter, r *http.Request) {
	followPage, err := s.followPage(r, db.GetFollowers, func(f models.Follow) string { return f.UserDid })
	if err != nil {
		s.pages.Notice(w, "all-followers", "Failed to load followers")
		return
	}

	s.pages.ProfileFollowers(w, pages.ProfileFollowersParams{
		LoggedInUser: s.oauth.GetUser(r),
		Followers:    followPage.Follows,
		Card:         followPage.Card,
	})
}

func (s *State) followingPage(w http.ResponseWriter, r *http.Request) {
	followPage, err := s.followPage(r, db.GetFollowing, func(f models.Follow) string { return f.SubjectDid })
	if err != nil {
		s.pages.Notice(w, "all-following", "Failed to load following")
		return
	}

	s.pages.ProfileFollowing(w, pages.ProfileFollowingParams{
		LoggedInUser: s.oauth.GetUser(r),
		Following:    followPage.Follows,
		Card:         followPage.Card,
	})
}

func (s *State) AtomFeedPage(w http.ResponseWriter, r *http.Request) {
	ident, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		s.pages.Error404(w)
		return
	}

	feed, err := s.getProfileFeed(r.Context(), &ident)
	if err != nil {
		s.pages.Error500(w)
		return
	}

	if feed == nil {
		return
	}

	atom, err := feed.ToAtom()
	if err != nil {
		s.pages.Error500(w)
		return
	}

	w.Header().Set("content-type", "application/atom+xml")
	w.Write([]byte(atom))
}

func (s *State) getProfileFeed(ctx context.Context, id *identity.Identity) (*feeds.Feed, error) {
	timeline, err := db.MakeProfileTimeline(s.db, id.DID.String())
	if err != nil {
		return nil, err
	}

	author := &feeds.Author{
		Name: fmt.Sprintf("@%s", id.Handle),
	}

	feed := feeds.Feed{
		Title:   fmt.Sprintf("%s's timeline", author.Name),
		Link:    &feeds.Link{Href: fmt.Sprintf("%s/@%s", s.config.Core.AppviewHost, id.Handle), Type: "text/html", Rel: "alternate"},
		Items:   make([]*feeds.Item, 0),
		Updated: time.UnixMilli(0),
		Author:  author,
	}

	for _, byMonth := range timeline.ByMonth {
		if err := s.addPullRequestItems(ctx, &feed, byMonth.PullEvents.Items, author); err != nil {
			return nil, err
		}
		if err := s.addIssueItems(ctx, &feed, byMonth.IssueEvents.Items, author); err != nil {
			return nil, err
		}
		if err := s.addRepoItems(ctx, &feed, byMonth.RepoEvents, author); err != nil {
			return nil, err
		}
	}

	slices.SortFunc(feed.Items, func(a *feeds.Item, b *feeds.Item) int {
		return int(b.Created.UnixMilli()) - int(a.Created.UnixMilli())
	})

	if len(feed.Items) > 0 {
		feed.Updated = feed.Items[0].Created
	}

	return &feed, nil
}

func (s *State) addPullRequestItems(ctx context.Context, feed *feeds.Feed, pulls []*models.Pull, author *feeds.Author) error {
	for _, pull := range pulls {
		owner, err := s.idResolver.ResolveIdent(ctx, pull.Repo.Did)
		if err != nil {
			return err
		}

		// Add pull request creation item
		feed.Items = append(feed.Items, s.createPullRequestItem(pull, owner, author))
	}
	return nil
}

func (s *State) addIssueItems(ctx context.Context, feed *feeds.Feed, issues []*models.Issue, author *feeds.Author) error {
	for _, issue := range issues {
		owner, err := s.idResolver.ResolveIdent(ctx, issue.Repo.Did)
		if err != nil {
			return err
		}

		feed.Items = append(feed.Items, s.createIssueItem(issue, owner, author))
	}
	return nil
}

func (s *State) addRepoItems(ctx context.Context, feed *feeds.Feed, repos []db.RepoEvent, author *feeds.Author) error {
	for _, repo := range repos {
		item, err := s.createRepoItem(ctx, repo, author)
		if err != nil {
			return err
		}
		feed.Items = append(feed.Items, item)
	}
	return nil
}

func (s *State) createPullRequestItem(pull *models.Pull, owner *identity.Identity, author *feeds.Author) *feeds.Item {
	return &feeds.Item{
		Title:   fmt.Sprintf("%s created pull request '%s' in @%s/%s", author.Name, pull.Title, owner.Handle, pull.Repo.Name),
		Link:    &feeds.Link{Href: fmt.Sprintf("%s/@%s/%s/pulls/%d", s.config.Core.AppviewHost, owner.Handle, pull.Repo.Name, pull.PullId), Type: "text/html", Rel: "alternate"},
		Created: pull.Created,
		Author:  author,
	}
}

func (s *State) createIssueItem(issue *models.Issue, owner *identity.Identity, author *feeds.Author) *feeds.Item {
	return &feeds.Item{
		Title:   fmt.Sprintf("%s created issue '%s' in @%s/%s", author.Name, issue.Title, owner.Handle, issue.Repo.Name),
		Link:    &feeds.Link{Href: fmt.Sprintf("%s/@%s/%s/issues/%d", s.config.Core.AppviewHost, owner.Handle, issue.Repo.Name, issue.IssueId), Type: "text/html", Rel: "alternate"},
		Created: issue.Created,
		Author:  author,
	}
}

func (s *State) createRepoItem(ctx context.Context, repo db.RepoEvent, author *feeds.Author) (*feeds.Item, error) {
	var title string
	if repo.Source != nil {
		sourceOwner, err := s.idResolver.ResolveIdent(ctx, repo.Source.Did)
		if err != nil {
			return nil, err
		}
		title = fmt.Sprintf("%s forked repository @%s/%s to '%s'", author.Name, sourceOwner.Handle, repo.Source.Name, repo.Repo.Name)
	} else {
		title = fmt.Sprintf("%s created repository '%s'", author.Name, repo.Repo.Name)
	}

	return &feeds.Item{
		Title:   title,
		Link:    &feeds.Link{Href: fmt.Sprintf("%s/@%s/%s", s.config.Core.AppviewHost, author.Name[1:], repo.Repo.Name), Type: "text/html", Rel: "alternate"}, // Remove @ prefix
		Created: repo.Repo.Created,
		Author:  author,
	}, nil
}

func (s *State) UpdateProfileBio(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	err := r.ParseForm()
	if err != nil {
		log.Println("invalid profile update form", err)
		s.pages.Notice(w, "update-profile", "Invalid form.")
		return
	}

	profile, err := db.GetProfile(s.db, user.Did)
	if err != nil {
		log.Printf("getting profile data for %s: %s", user.Did, err)
	}

	profile.Description = r.FormValue("description")
	profile.IncludeBluesky = r.FormValue("includeBluesky") == "on"
	profile.Location = r.FormValue("location")

	var links [5]string
	for i := range 5 {
		iLink := r.FormValue(fmt.Sprintf("link%d", i))
		links[i] = iLink
	}
	profile.Links = links

	// Parse stats (exactly 2)
	stat0 := r.FormValue("stat0")
	stat1 := r.FormValue("stat1")

	if stat0 != "" {
		profile.Stats[0].Kind = db.VanityStatKind(stat0)
	}

	if stat1 != "" {
		profile.Stats[1].Kind = db.VanityStatKind(stat1)
	}

	if err := db.ValidateProfile(s.db, profile); err != nil {
		log.Println("invalid profile", err)
		s.pages.Notice(w, "update-profile", err.Error())
		return
	}

	s.updateProfile(profile, w, r)
}

func (s *State) UpdateProfilePins(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	err := r.ParseForm()
	if err != nil {
		log.Println("invalid profile update form", err)
		s.pages.Notice(w, "update-profile", "Invalid form.")
		return
	}

	profile, err := db.GetProfile(s.db, user.Did)
	if err != nil {
		log.Printf("getting profile data for %s: %s", user.Did, err)
	}

	i := 0
	var pinnedRepos [6]syntax.ATURI
	for key, values := range r.Form {
		if i >= 6 {
			log.Println("invalid pin update form", err)
			s.pages.Notice(w, "update-profile", "Only 6 repositories can be pinned at a time.")
			return
		}
		if strings.HasPrefix(key, "pinnedRepo") && len(values) > 0 && values[0] != "" && i < 6 {
			aturi, err := syntax.ParseATURI(values[0])
			if err != nil {
				log.Println("invalid profile update form", err)
				s.pages.Notice(w, "update-profile", "Invalid form.")
				return
			}
			pinnedRepos[i] = aturi
			i++
		}
	}
	profile.PinnedRepos = pinnedRepos

	s.updateProfile(profile, w, r)
}

func (s *State) updateProfile(profile *db.Profile, w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)
	tx, err := s.db.BeginTx(r.Context(), nil)
	if err != nil {
		log.Println("failed to start transaction", err)
		s.pages.Notice(w, "update-profile", "Failed to update profile, try again later.")
		return
	}

	client, err := s.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to get authorized client", err)
		s.pages.Notice(w, "update-profile", "Failed to update profile, try again later.")
		return
	}

	// yeah... lexgen dose not support syntax.ATURI in the record for some reason,
	// nor does it support exact size arrays
	var pinnedRepoStrings []string
	for _, r := range profile.PinnedRepos {
		pinnedRepoStrings = append(pinnedRepoStrings, r.String())
	}

	var vanityStats []string
	for _, v := range profile.Stats {
		vanityStats = append(vanityStats, string(v.Kind))
	}

	ex, _ := client.RepoGetRecord(r.Context(), "", tangled.ActorProfileNSID, user.Did, "self")
	var cid *string
	if ex != nil {
		cid = ex.Cid
	}

	_, err = client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
		Collection: tangled.ActorProfileNSID,
		Repo:       user.Did,
		Rkey:       "self",
		Record: &lexutil.LexiconTypeDecoder{
			Val: &tangled.ActorProfile{
				Bluesky:            profile.IncludeBluesky,
				Description:        &profile.Description,
				Links:              profile.Links[:],
				Location:           &profile.Location,
				PinnedRepositories: pinnedRepoStrings,
				Stats:              vanityStats[:],
			}},
		SwapRecord: cid,
	})
	if err != nil {
		log.Println("failed to update profile", err)
		s.pages.Notice(w, "update-profile", "Failed to update PDS, try again later.")
		return
	}

	err = db.UpsertProfile(tx, profile)
	if err != nil {
		log.Println("failed to update profile", err)
		s.pages.Notice(w, "update-profile", "Failed to update profile, try again later.")
		return
	}

	s.notifier.UpdateProfile(r.Context(), profile)

	s.pages.HxRedirect(w, "/"+user.Did)
}

func (s *State) EditBioFragment(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	profile, err := db.GetProfile(s.db, user.Did)
	if err != nil {
		log.Printf("getting profile data for %s: %s", user.Did, err)
	}

	s.pages.EditBioFragment(w, pages.EditBioParams{
		LoggedInUser: user,
		Profile:      profile,
	})
}

func (s *State) EditPinsFragment(w http.ResponseWriter, r *http.Request) {
	user := s.oauth.GetUser(r)

	profile, err := db.GetProfile(s.db, user.Did)
	if err != nil {
		log.Printf("getting profile data for %s: %s", user.Did, err)
	}

	repos, err := db.GetRepos(s.db, 0, db.FilterEq("did", user.Did))
	if err != nil {
		log.Printf("getting repos for %s: %s", user.Did, err)
	}

	collaboratingRepos, err := db.CollaboratingIn(s.db, user.Did)
	if err != nil {
		log.Printf("getting collaborating repos for %s: %s", user.Did, err)
	}

	allRepos := []pages.PinnedRepo{}

	for _, r := range repos {
		isPinned := slices.Contains(profile.PinnedRepos[:], r.RepoAt())
		allRepos = append(allRepos, pages.PinnedRepo{
			IsPinned: isPinned,
			Repo:     r,
		})
	}
	for _, r := range collaboratingRepos {
		isPinned := slices.Contains(profile.PinnedRepos[:], r.RepoAt())
		allRepos = append(allRepos, pages.PinnedRepo{
			IsPinned: isPinned,
			Repo:     r,
		})
	}

	s.pages.EditPinsFragment(w, pages.EditPinsParams{
		LoggedInUser: user,
		Profile:      profile,
		AllRepos:     allRepos,
	})
}
