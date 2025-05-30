package state

import (
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/posthog/posthog-go"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
)

func (s *State) Follow(w http.ResponseWriter, r *http.Request) {
	currentUser := s.oauth.GetUser(r)

	subject := r.URL.Query().Get("subject")
	if subject == "" {
		log.Println("invalid form")
		return
	}

	subjectIdent, err := s.idResolver.ResolveIdent(r.Context(), subject)
	if err != nil {
		log.Println("failed to follow, invalid did")
	}

	if currentUser.Did == subjectIdent.DID.String() {
		log.Println("cant follow or unfollow yourself")
		return
	}

	client, err := s.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to authorize client")
		return
	}

	switch r.Method {
	case http.MethodPost:
		createdAt := time.Now().Format(time.RFC3339)
		rkey := appview.TID()
		resp, err := client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
			Collection: tangled.GraphFollowNSID,
			Repo:       currentUser.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.GraphFollow{
					Subject:   subjectIdent.DID.String(),
					CreatedAt: createdAt,
				}},
		})
		if err != nil {
			log.Println("failed to create atproto record", err)
			return
		}

		err = db.AddFollow(s.db, currentUser.Did, subjectIdent.DID.String(), rkey)
		if err != nil {
			log.Println("failed to follow", err)
			return
		}

		log.Println("created atproto record: ", resp.Uri)

		s.pages.FollowFragment(w, pages.FollowFragmentParams{
			UserDid:      subjectIdent.DID.String(),
			FollowStatus: db.IsFollowing,
		})

		if !s.config.Core.Dev {
			err = s.posthog.Enqueue(posthog.Capture{
				DistinctId: currentUser.Did,
				Event:      "follow",
				Properties: posthog.Properties{"subject": subjectIdent.DID.String()},
			})
			if err != nil {
				log.Println("failed to enqueue posthog event:", err)
			}
		}

		return
	case http.MethodDelete:
		// find the record in the db
		follow, err := db.GetFollow(s.db, currentUser.Did, subjectIdent.DID.String())
		if err != nil {
			log.Println("failed to get follow relationship")
			return
		}

		_, err = client.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
			Collection: tangled.GraphFollowNSID,
			Repo:       currentUser.Did,
			Rkey:       follow.Rkey,
		})

		if err != nil {
			log.Println("failed to unfollow")
			return
		}

		err = db.DeleteFollowByRkey(s.db, currentUser.Did, follow.Rkey)
		if err != nil {
			log.Println("failed to delete follow from DB")
			// this is not an issue, the firehose event might have already done this
		}

		s.pages.FollowFragment(w, pages.FollowFragmentParams{
			UserDid:      subjectIdent.DID.String(),
			FollowStatus: db.IsNotFollowing,
		})

		if !s.config.Core.Dev {
			err = s.posthog.Enqueue(posthog.Capture{
				DistinctId: currentUser.Did,
				Event:      "unfollow",
				Properties: posthog.Properties{"subject": subjectIdent.DID.String()},
			})
			if err != nil {
				log.Println("failed to enqueue posthog event:", err)
			}
		}

		return
	}

}
