package state

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"log"
	"net/http"

	"github.com/bluesky-social/indigo/atproto/identity"
	"github.com/go-chi/chi/v5"
	"go.opentelemetry.io/otel/attribute"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
)

func (s *State) ProfilePage(w http.ResponseWriter, r *http.Request) {
	ctx, span := s.t.TraceStart(r.Context(), "ProfilePage")
	defer span.End()

	didOrHandle := chi.URLParam(r, "user")
	if didOrHandle == "" {
		http.Error(w, "Bad request", http.StatusBadRequest)
		return
	}

	ident, ok := ctx.Value("resolvedId").(identity.Identity)
	if !ok {
		s.pages.Error404(w)
		span.RecordError(fmt.Errorf("failed to resolve identity"))
		return
	}

	span.SetAttributes(
		attribute.String("user.did", ident.DID.String()),
		attribute.String("user.handle", ident.Handle.String()),
	)

	repos, err := db.GetAllReposByDid(ctx, s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting repos for %s: %s", ident.DID.String(), err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error.repos", err.Error()))
	}
	span.SetAttributes(attribute.Int("repos.count", len(repos)))

	collaboratingRepos, err := db.CollaboratingIn(ctx, s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting collaborating repos for %s: %s", ident.DID.String(), err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error.collaborating_repos", err.Error()))
	}
	span.SetAttributes(attribute.Int("collaborating_repos.count", len(collaboratingRepos)))

	timeline, err := db.MakeProfileTimeline(ctx, s.db, ident.DID.String())
	if err != nil {
		log.Printf("failed to create profile timeline for %s: %s", ident.DID.String(), err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error.timeline", err.Error()))
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
	span.SetAttributes(attribute.Int("dids_to_resolve.count", len(didsToResolve)))

	resolvedIds := s.resolver.ResolveIdents(ctx, didsToResolve)
	didHandleMap := make(map[string]string)
	for _, identity := range resolvedIds {
		if !identity.Handle.IsInvalidHandle() {
			didHandleMap[identity.DID.String()] = fmt.Sprintf("@%s", identity.Handle.String())
		} else {
			didHandleMap[identity.DID.String()] = identity.DID.String()
		}
	}
	span.SetAttributes(attribute.Int("resolved_ids.count", len(resolvedIds)))

	followers, following, err := db.GetFollowerFollowing(s.db, ident.DID.String())
	if err != nil {
		log.Printf("getting follow stats repos for %s: %s", ident.DID.String(), err)
		span.RecordError(err)
		span.SetAttributes(attribute.String("error.follow_stats", err.Error()))
	}
	span.SetAttributes(
		attribute.Int("followers.count", followers),
		attribute.Int("following.count", following),
	)

	loggedInUser := s.auth.GetUser(r)
	followStatus := db.IsNotFollowing
	if loggedInUser != nil {
		followStatus = db.GetFollowStatus(s.db, loggedInUser.Did, ident.DID.String())
		span.SetAttributes(attribute.String("logged_in_user.did", loggedInUser.Did))
	}
	span.SetAttributes(attribute.String("follow_status", string(db.FollowStatus(followStatus))))

	profileAvatarUri := s.GetAvatarUri(ident.Handle.String())
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

func (s *State) GetAvatarUri(handle string) string {
	secret := s.config.AvatarSharedSecret
	h := hmac.New(sha256.New, []byte(secret))
	h.Write([]byte(handle))
	signature := hex.EncodeToString(h.Sum(nil))
	return fmt.Sprintf("%s/%s/%s", s.config.AvatarHost, signature, handle)
}
