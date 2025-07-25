package state

import (
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	"github.com/bluesky-social/indigo/atproto/syntax"

	lexutil "github.com/bluesky-social/indigo/lex/util"
	"tangled.sh/tangled.sh/core/api/tangled"
	"tangled.sh/tangled.sh/core/appview"
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages"
)

func (s *State) React(w http.ResponseWriter, r *http.Request) {
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

	reactionKind, ok := db.ParseReactionKind(r.URL.Query().Get("kind"))
	if !ok {
		log.Println("invalid reaction kind")
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
			Collection: tangled.FeedReactionNSID,
			Repo:       currentUser.Did,
			Rkey:       rkey,
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.FeedReaction{
					Subject:   subjectUri.String(),
					Reaction:  reactionKind.String(),
					CreatedAt: createdAt,
				},
			},
		})
		if err != nil {
			log.Println("failed to create atproto record", err)
			return
		}

		err = db.AddReaction(s.db, currentUser.Did, subjectUri, reactionKind, rkey)
		if err != nil {
			log.Println("failed to react", err)
			return
		}

		count, err := db.GetReactionCount(s.db, subjectUri, reactionKind)
		if err != nil {
			log.Println("failed to get reaction count for ", subjectUri)
		}

		log.Println("created atproto record: ", resp.Uri)

		s.pages.ThreadReactionFragment(w, pages.ThreadReactionFragmentParams{
			ThreadAt:      subjectUri,
			Kind: reactionKind,
			Count: count,
			IsReacted: true,
		})

		return
	case http.MethodDelete:
		reaction, err := db.GetReaction(s.db, currentUser.Did, subjectUri, reactionKind)
		if err != nil {
			log.Println("failed to get reaction relationship for", currentUser.Did, subjectUri)
			return
		}

		_, err = client.RepoDeleteRecord(r.Context(), &comatproto.RepoDeleteRecord_Input{
			Collection: tangled.FeedReactionNSID,
			Repo:       currentUser.Did,
			Rkey:       reaction.Rkey,
		})

		if err != nil {
			log.Println("failed to remove reaction")
			return
		}

		err = db.DeleteReactionByRkey(s.db, currentUser.Did, reaction.Rkey)
		if err != nil {
			log.Println("failed to delete reaction from DB")
			// this is not an issue, the firehose event might have already done this
		}

		count, err := db.GetReactionCount(s.db, subjectUri, reactionKind)
		if err != nil {
			log.Println("failed to get reaction count for ", subjectUri)
			return
		}

		s.pages.ThreadReactionFragment(w, pages.ThreadReactionFragmentParams{
			ThreadAt:         subjectUri,
			Kind: reactionKind,
			Count: count,
			IsReacted: false,
		})

		return
	}
}
