package state

import (
	"log"
	"net/http"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/gliderlabs/ssh"
	"github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/appview/pages"
)

func (s *State) Settings(w http.ResponseWriter, r *http.Request) {
	// for now, this is just pubkeys
	user := s.auth.GetUser(r)
	pubKeys, err := s.db.GetPublicKeys(user.Did)
	if err != nil {
		log.Println(err)
	}

	s.pages.Settings(w, pages.SettingsParams{
		LoggedInUser: user,
		PubKeys:      pubKeys,
	})
}

func (s *State) SettingsKeys(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		w.Write([]byte("unimplemented"))
		log.Println("unimplemented")
		return
	case http.MethodPut:
		did := s.auth.GetDid(r)
		key := r.FormValue("key")
		key = strings.TrimSpace(key)
		name := r.FormValue("name")
		client, _ := s.auth.AuthorizedClient(r)

		_, _, _, _, err := ssh.ParseAuthorizedKey([]byte(key))
		if err != nil {
			log.Printf("parsing public key: %s", err)
			return
		}

		if err := s.db.AddPublicKey(did, name, key); err != nil {
			log.Printf("adding public key: %s", err)
			return
		}

		// store in pds too
		resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.PublicKeyNSID,
			Repo:       did,
			Rkey:       s.TID(),
			Record: &lexutil.LexiconTypeDecoder{
				Val: &tangled.PublicKey{
					Created: time.Now().Format(time.RFC3339),
					Key:     key,
					Name:    name,
				}},
		})
		// invalid record
		if err != nil {
			log.Printf("failed to create record: %s", err)
			return
		}

		log.Println("created atproto record: ", resp.Uri)

		return
	}
}
