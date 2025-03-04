package state

import (
	"fmt"
	"log"
	"net/http"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	tangled "github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/appview/db"
)

func (s *State) Follow(w http.ResponseWriter, r *http.Request) {
	currentUser := s.auth.GetUser(r)

	subject := r.URL.Query().Get("subject")
	if subject == "" {
		log.Println("invalid form")
		return
	}

	subjectIdent, err := s.resolver.ResolveIdent(r.Context(), subject)
	if err != nil {
		log.Println("failed to follow, invalid did")
	}

	if currentUser.Did == subjectIdent.DID.String() {
		log.Println("cant follow or unfollow yourself")
		return
	}

	client, _ := s.auth.AuthorizedClient(r)

	switch r.Method {
	case http.MethodPost:
		createdAt := time.Now().Format(time.RFC3339)
		rkey := s.TID()
		resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
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

		w.Write([]byte(fmt.Sprintf(`
			<button id="followBtn"
				class="btn mt-2 w-full"
				hx-delete="/follow?subject=%s"
				hx-trigger="click"
				hx-target="#followBtn"
				hx-swap="outerHTML">
				Unfollow
			</button>
		`, subjectIdent.DID.String())))

		return
	case http.MethodDelete:
		// find the record in the db
		follow, err := db.GetFollow(s.db, currentUser.Did, subjectIdent.DID.String())
		if err != nil {
			log.Println("failed to get follow relationship")
			return
		}

		_, err = comatproto.RepoDeleteRecord(r.Context(), client, &comatproto.RepoDeleteRecord_Input{
			Collection: tangled.GraphFollowNSID,
			Repo:       currentUser.Did,
			Rkey:       follow.RKey,
		})

		if err != nil {
			log.Println("failed to unfollow")
			return
		}

		err = db.DeleteFollow(s.db, currentUser.Did, subjectIdent.DID.String())
		if err != nil {
			log.Println("failed to delete follow from DB")
			// this is not an issue, the firehose event might have already done this
		}

		w.Write([]byte(fmt.Sprintf(`
			<button id="followBtn"
				class="btn mt-2 w-full"
				hx-post="/follow?subject=%s"
				hx-trigger="click"
				hx-target="#followBtn"
				hx-swap="outerHTML">
				Follow
			</button>
		`, subjectIdent.DID.String())))
		return
	}

}
