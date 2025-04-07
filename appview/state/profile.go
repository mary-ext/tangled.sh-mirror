package state

import (
	"fmt"
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
)

func (s *State) ProfilePage(w http.ResponseWriter, r *http.Request) {
	didOrHandle := chi.URLParam(r, "user")
	if didOrHandle == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	ident, err := s.resolver.ResolveIdent(r.Context(), didOrHandle)
	if err != nil {
		log.Printf("resolving identity: %s", err)
		w.WriteHeader(http.StatusNotFound)
		return
	}

	repos, err := db.GetAllReposByDid(s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting repos for %s: %s", ident.DID.String(), err)
	}

	collaboratingRepos, err := db.CollaboratingIn(s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting collaborating repos for %s: %s", ident.DID.String(), err)
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

	resolvedIds := s.resolver.ResolveIdents(r.Context(), didsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}

	followers, following, err := db.GetFollowerFollowing(s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting follow stats repos for %s: %s", ident.DID.String(), err)
	}

	loggedInUser := s.auth.GetUser(r)
	followStatus := db.IsNotFollowing
	if loggedInUser != nil {
		followStatus = db.GetFollowStatus(s.db, loggedInUser.Did, ident.DID.String())
	}

	profileAvatarUri, err := GetAvatarUri(ident.Handle.String())
	if err != nil {
		log.Println("failed to fetch bsky avatar", err)
	}

	s.pages.ProfilePage(w, pages.ProfilePageParams{
		LoggedInUser:       loggedInUser,
		UserDid:            ident.DID.String(),
		UserHandle:         ident.Handle.String(),
		Repos:              repos,
		CollaboratingRepos: collaboratingRepos,
		ProfileStats: pages.ProfileStats{
			Followers: followers,
			Following: following,
		},
		FollowStatus:    db.FollowStatus(followStatus),
		DidHandleMap:    didHandleMap,
		AvatarUri:       profileAvatarUri,
		ProfileTimeline: timeline,
	})
}
