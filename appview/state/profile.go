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
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
)

func (s *State) Profile(w http.ResponseWriter, r *http.Request) {
	tabVal := r.URL.Query().Get("tab")
	switch tabVal {
	case "":
		s.profilePage(w, r)
	case "repos":
		s.reposPage(w, r)
	}
}

func (s *State) profilePage(w http.ResponseWriter, r *http.Request) {
	didOrHandle := chi.URLParam(r, "user")
	if didOrHandle == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	ident, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		s.pages.Error404(w)
		return
	}

	profile, err := db.GetProfile(s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting profile data for %s: %s", ident.DID.String(), err)
	}

	repos, err := db.GetRepos(
		s.db,
		0,
		db.FilterEq("did", ident.DID.String()),
	)
	if err != nil {
		log.Printf("getting repos for %s: %s", ident.DID.String(), err)
	}

	// filter out ones that are pinned
	pinnedRepos := []db.Repo{}
	for i, r := range repos {
		// if this is a pinned repo, add it
		if slices.Contains(profile.PinnedRepos[:], r.RepoAt()) {
			pinnedRepos = append(pinnedRepos, r)
		}

		// if there are no saved pins, add the first 4 repos
		if profile.IsPinnedReposEmpty() && i < 4 {
			pinnedRepos = append(pinnedRepos, r)
		}
	}

	collaboratingRepos, err := db.CollaboratingIn(s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting collaborating repos for %s: %s", ident.DID.String(), err)
	}

	pinnedCollaboratingRepos := []db.Repo{}
	for _, r := range collaboratingRepos {
		// if this is a pinned repo, add it
		if slices.Contains(profile.PinnedRepos[:], r.RepoAt()) {
			pinnedCollaboratingRepos = append(pinnedCollaboratingRepos, r)
		}
	}

	timeline, err := db.MakeProfileTimeline(s.db, ident.DID.String())
	if err != nil {
		log.Printf("failed to create profile timeline for %s: %s", ident.DID.String(), err)
	}

	var didsToResolve []string
	for _, r := range collaboratingRepos {
		didsToResolve = append(didsToResolve, r.Did)
	}
	for _, byMonth := range timeline.ByMonth {
		for _, pe := range byMonth.PullEvents.Items {
			didsToResolve = append(didsToResolve, pe.Repo.Did)
		}
		for _, ie := range byMonth.IssueEvents.Items {
			didsToResolve = append(didsToResolve, ie.Metadata.Repo.Did)
		}
		for _, re := range byMonth.RepoEvents {
			didsToResolve = append(didsToResolve, re.Repo.Did)
			if re.Source != nil {
				didsToResolve = append(didsToResolve, re.Source.Did)
			}
		}
	}

	resolvedIds := s.idResolver.ResolveIdents(r.Context(), didsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	followers, following, err := db.GetFollowerFollowingCount(s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting follow stats repos for %s: %s", ident.DID.String(), err)
	}

	loggedInUser := s.oauth.GetUser(r)
	followStatus := db.IsNotFollowing
	if loggedInUser != nil {
		followStatus = db.GetFollowStatus(s.db, loggedInUser.Did, ident.DID.String())
	}

	now := time.Now()
	startOfYear := time.Date(now.Year(), 1, 1, 0, 0, 0, 0, time.UTC)
	punchcard, err := db.MakePunchcard(
		s.db,
		db.FilterEq("did", ident.DID.String()),
		db.FilterGte("date", startOfYear.Format(time.DateOnly)),
		db.FilterLte("date", now.Format(time.DateOnly)),
	)
	if err != nil {
		log.Println("failed to get punchcard for did", "did", ident.DID.String(), "err", err)
	}

	s.pages.ProfilePage(w, pages.ProfilePageParams{
		LoggedInUser:       loggedInUser,
		Repos:              pinnedRepos,
		CollaboratingRepos: pinnedCollaboratingRepos,
		DidHandleMap:       didHandleMap,
		Card: pages.ProfileCard{
			UserDid:      ident.DID.String(),
			UserHandle:   ident.Handle.String(),
			Profile:      profile,
			FollowStatus: followStatus,
			Followers:    followers,
			Following:    following,
		},
		Punchcard:       punchcard,
		ProfileTimeline: timeline,
	})
}

func (s *State) reposPage(w http.ResponseWriter, r *http.Request) {
	ident, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		s.pages.Error404(w)
		return
	}

	profile, err := db.GetProfile(s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting profile data for %s: %s", ident.DID.String(), err)
	}

	repos, err := db.GetRepos(
		s.db,
		0,
		db.FilterEq("did", ident.DID.String()),
	)
	if err != nil {
		log.Printf("getting repos for %s: %s", ident.DID.String(), err)
	}

	loggedInUser := s.oauth.GetUser(r)
	followStatus := db.IsNotFollowing
	if loggedInUser != nil {
		followStatus = db.GetFollowStatus(s.db, loggedInUser.Did, ident.DID.String())
	}

	followers, following, err := db.GetFollowerFollowingCount(s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting follow stats repos for %s: %s", ident.DID.String(), err)
	}

	s.pages.ReposPage(w, pages.ReposPageParams{
		LoggedInUser: loggedInUser,
		Repos:        repos,
		DidHandleMap: map[string]string{ident.DID.String(): ident.Handle.String()},
		Card: pages.ProfileCard{
			UserDid:      ident.DID.String(),
			UserHandle:   ident.Handle.String(),
			Profile:      profile,
			FollowStatus: followStatus,
			Followers:    followers,
			Following:    following,
		},
	})
}

func (s *State) feedFromRequest(w http.ResponseWriter, r *http.Request) *feeds.Feed {
	ident, ok := r.Context().Value("resolvedId").(identity.Identity)
	if !ok {
		s.pages.Error404(w)
		return nil
	}

	feed, err := s.GetProfileFeed(r.Context(), ident.Handle.String(), ident.DID.String())
	if err != nil {
		s.pages.Error500(w)
		return nil
	}

	return feed
}

func (s *State) AtomFeedPage(w http.ResponseWriter, r *http.Request) {
	feed := s.feedFromRequest(w, r)
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

func (s *State) GetProfileFeed(ctx context.Context, handle string, did string) (*feeds.Feed, error) {
	timeline, err := db.MakeProfileTimeline(s.db, did)
	if err != nil {
		return nil, err
	}

	author := &feeds.Author{
		Name: fmt.Sprintf("@%s", handle),
	}
	feed := &feeds.Feed{
		Title:   fmt.Sprintf("timeline feed for %s", author.Name),
		Link:    &feeds.Link{Href: fmt.Sprintf("%s/@%s", s.config.Core.AppviewHost, handle), Type: "text/html", Rel: "alternate"},
		Items:   make([]*feeds.Item, 0),
		Updated: time.UnixMilli(0),
		Author:  author,
	}
	for _, byMonth := range timeline.ByMonth {
		for _, pull := range byMonth.PullEvents.Items {
			owner, err := s.idResolver.ResolveIdent(ctx, pull.Repo.Did)
			if err != nil {
				return nil, err
			}
			feed.Items = append(feed.Items, &feeds.Item{
				Title:   fmt.Sprintf("%s created pull request '%s' in @%s/%s", author.Name, pull.Title, owner.Handle, pull.Repo.Name),
				Link:    &feeds.Link{Href: fmt.Sprintf("%s/@%s/%s/pulls/%d", s.config.Core.AppviewHost, owner.Handle, pull.Repo.Name, pull.PullId), Type: "text/html", Rel: "alternate"},
				Created: pull.Created,
				Author:  author,
			})
			for _, submission := range pull.Submissions {
				feed.Items = append(feed.Items, &feeds.Item{
					Title:   fmt.Sprintf("%s submitted pull request '%s' (round #%d) in @%s/%s", author.Name, pull.Title, submission.RoundNumber, owner.Handle, pull.Repo.Name),
					Link:    &feeds.Link{Href: fmt.Sprintf("%s/@%s/%s/pulls/%d", s.config.Core.AppviewHost, owner.Handle, pull.Repo.Name, pull.PullId), Type: "text/html", Rel: "alternate"},
					Created: submission.Created,
					Author:  author,
				})
			}
		}
		for _, issue := range byMonth.IssueEvents.Items {
			owner, err := s.idResolver.ResolveIdent(ctx, issue.Metadata.Repo.Did)
			if err != nil {
				return nil, err
			}
			feed.Items = append(feed.Items, &feeds.Item{
				Title:   fmt.Sprintf("%s created issue '%s' in @%s/%s", author.Name, issue.Title, owner.Handle, issue.Metadata.Repo.Name),
				Link:    &feeds.Link{Href: fmt.Sprintf("%s/@%s/%s/issues/%d", s.config.Core.AppviewHost, owner.Handle, issue.Metadata.Repo.Name, issue.IssueId), Type: "text/html", Rel: "alternate"},
				Created: issue.Created,
				Author:  author,
			})
		}
		for _, repo := range byMonth.RepoEvents {
			var title string
			if repo.Source != nil {
				id, err := s.idResolver.ResolveIdent(ctx, repo.Source.Did)
				if err != nil {
					return nil, err
				}
				title = fmt.Sprintf("%s forked repository @%s/%s to '%s'", author.Name, id.Handle, repo.Source.Name, repo.Repo.Name)
			} else {
				title = fmt.Sprintf("%s created repository '%s'", author.Name, repo.Repo.Name)
			}
			feed.Items = append(feed.Items, &feeds.Item{
				Title:   title,
				Link:    &feeds.Link{Href: fmt.Sprintf("%s/@%s/%s", s.config.Core.AppviewHost, handle, repo.Repo.Name), Type: "text/html", Rel: "alternate"},
				Created: repo.Repo.Created,
				Author:  author,
			})
		}
	}
	slices.SortFunc(feed.Items, func(a *feeds.Item, b *feeds.Item) int {
		return int(b.Created.UnixMilli()) - int(a.Created.UnixMilli())
	})
	if len(feed.Items) > 0 {
		feed.Updated = feed.Items[0].Created
	}

	return feed, nil
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

	repos, err := db.GetAllReposByDid(s.db, user.Did)
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

	var didsToResolve []string
	for _, r := range allRepos {
		didsToResolve = append(didsToResolve, r.Did)
	}
	resolvedIds := s.idResolver.ResolveIdents(r.Context(), didsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	s.pages.EditPinsFragment(w, pages.EditPinsParams{
		LoggedInUser: user,
		Profile:      profile,
		AllRepos:     allRepos,
		DidHandleMap: didHandleMap,
	})
}
