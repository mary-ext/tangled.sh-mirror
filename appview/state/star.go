package state

import (
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	tangled "github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/appview/db"
	"github.com/sotangled/tangled/appview/pages"
)

func (s *State) Star(w http.ResponseWriter, r *http.Request) {
	currentUser := s.auth.GetUser(r)

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

	client, _ := s.auth.AuthorizedClient(r)

	switch r.Method {
	case http.MethodPost:
		createdAt := time.Now().Format(time.RFC3339)
		rkey := s.TID()
		resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
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

		s.pages.StarFragment(w, pages.StarFragmentParams{
			IsStarred: true,
			RepoAt:    subjectUri,
			Stats: db.RepoStats{
				StarCount: starCount,
			},
		})

		return
	case http.MethodDelete:
		// find the record in the db
		star, err := db.GetStar(s.db, currentUser.Did, subjectUri)
		if err != nil {
			log.Println("failed to get star relationship")
			return
		}

		_, err = comatproto.RepoDeleteRecord(r.Context(), client, &comatproto.RepoDeleteRecord_Input{
			Collection: tangled.FeedStarNSID,
			Repo:       currentUser.Did,
			Rkey:       star.Rkey,
		})

		if err != nil {
			log.Println("failed to unstar")
			return
		}

		err = db.DeleteStar(s.db, currentUser.Did, subjectUri)
		if err != nil {
			log.Println("failed to delete star from DB")
			// this is not an issue, the firehose event might have already done this
		}

		starCount, err := db.GetStarCount(s.db, subjectUri)
		if err != nil {
			log.Println("failed to get star count for ", subjectUri)
		}

		s.pages.StarFragment(w, pages.StarFragmentParams{
			IsStarred: false,
			RepoAt:    subjectUri,
			Stats: db.RepoStats{
				StarCount: starCount,
			},
		})

		return
	}

}
