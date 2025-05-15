package state

import (
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/posthog/posthog-go"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
)

func (s *State) Star(w http.ResponseWriter, r *http.Request) {
	currentUser := s.oauth.GetUser(r)

	subject := r.URL.Query().Get("subject")
	if subject == "" {
		log.Println("invalid form")
		return
	}

	subjectUri, err := syntax.ParseATURI(subject)
	if err != nil {
		log.Println("invalid form")
		return
	}

	client, err := s.oauth.AuthorizedClient(r)
	if err != nil {
		log.Println("failed to authorize client", err)
		return
	}

	switch r.Method {
	case http.MethodPost:
		createdAt := time.Now().Format(time.RFC3339)
		rkey := appview.TID()
		resp, err := client.RepoPutRecord(r.Context(), &comatproto.RepoPutRecord_Input{
			Collection: tangled.FeedStarNSID,
			Repo:       currentUser.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.FeedStar{
					Subject:   subjectUri.String(),
					CreatedAt: createdAt,
				}},
		})
		if err != nil {
			log.Println("failed to create atproto record", err)
			return
		}

		err = db.AddStar(s.db, currentUser.Did, subjectUri, rkey)
		if err != nil {
			log.Println("failed to star", err)
			return
		}

		starCount, err := db.GetStarCount(s.db, subjectUri)
		if err != nil {
			log.Println("failed to get star count for ", subjectUri)
		}

		log.Println("created atproto record: ", resp.Uri)

		s.pages.RepoActionsFragment(w, pages.RepoActionsFragmentParams{
			IsStarred: true,
			RepoAt:    subjectUri,
			Stats: db.RepoStats{
				StarCount: starCount,
			},
		})

		if !s.config.Core.Dev {
			err = s.posthog.Enqueue(posthog.Capture{
				DistinctId: currentUser.Did,
				Event:      "star",
				Properties: posthog.Properties{"repo_at": subjectUri.String()},
			})
			if err != nil {
				log.Println("failed to enqueue posthog event:", err)
			}
		}

		return
	case http.MethodDelete:
		// find the record in the db
		star, err := db.GetStar(s.db, currentUser.Did, subjectUri)
		if err != nil {
			log.Println("failed to get star relationship")
			return
		}

		_, err = client.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
			Collection: tangled.FeedStarNSID,
			Repo:       currentUser.Did,
			Rkey:       star.Rkey,
		})

		if err != nil {
			log.Println("failed to unstar")
			return
		}

		err = db.DeleteStarByRkey(s.db, currentUser.Did, star.Rkey)
		if err != nil {
			log.Println("failed to delete star from DB")
			// this is not an issue, the firehose event might have already done this
		}

		starCount, err := db.GetStarCount(s.db, subjectUri)
		if err != nil {
			log.Println("failed to get star count for ", subjectUri)
			return
		}

		s.pages.RepoActionsFragment(w, pages.RepoActionsFragmentParams{
			IsStarred: false,
			RepoAt:    subjectUri,
			Stats: db.RepoStats{
				StarCount: starCount,
			},
		})

		if !s.config.Core.Dev {
			err = s.posthog.Enqueue(posthog.Capture{
				DistinctId: currentUser.Did,
				Event:      "unstar",
				Properties: posthog.Properties{"repo_at": subjectUri.String()},
			})
			if err != nil {
				log.Println("failed to enqueue posthog event:", err)
			}
		}

		return
	}

}
