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
	"github.com/sotangled/tangled/appview/db"
	"github.com/sotangled/tangled/appview/pages"
)

func (s *State) Settings(w http.ResponseWriter, r *http.Request) {
	// for now, this is just pubkeys
	user := s.auth.GetUser(r)
	pubKeys, err := db.GetPublicKeys(s.db, user.Did)
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
		s.pages.Notice(w, "settings-keys", "Unimplemented.")
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
			s.pages.Notice(w, "settings-keys", "That doesn't look like a valid public key. Make sure it's a <strong>public</strong> key.")
			return
		}

		rkey := s.TID()

		tx, err := s.db.Begin()
		if err != nil {
			log.Printf("failed to start tx; adding public key: %s", err)
			s.pages.Notice(w, "settings-keys", "Unable to add public key at this moment, try again later.")
			return
		}
		defer tx.Rollback()

		if err := db.AddPublicKey(tx, did, name, key, rkey); err != nil {
			log.Printf("adding public key: %s", err)
			s.pages.Notice(w, "settings-keys", "Failed to add public key.")
			return
		}

		// store in pds too
		resp, err := comatproto.RepoPutRecord(r.Context(), client, &comatproto.RepoPutRecord_Input{
			Collection: tangled.PublicKeyNSID,
			Repo:       did,
			Rkey:       rkey,
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
			s.pages.Notice(w, "settings-keys", "Failed to create record.")
			return
		}

		log.Println("created atproto record: ", resp.Uri)

		err = tx.Commit()
		if err != nil {
			log.Printf("failed to commit tx; adding public key: %s", err)
			s.pages.Notice(w, "settings-keys", "Unable to add public key at this moment, try again later.")
			return
		}

		s.pages.HxLocation(w, "/settings")
		return

	case http.MethodDelete:
		did := s.auth.GetDid(r)
		q := r.URL.Query()

		name := q.Get("name")
		rkey := q.Get("rkey")
		key := q.Get("key")

		log.Println(name)
		log.Println(rkey)
		log.Println(key)

		client, _ := s.auth.AuthorizedClient(r)

		if err := db.RemovePublicKey(s.db, did, name, key); err != nil {
			log.Printf("removing public key: %s", err)
			s.pages.Notice(w, "settings-keys", "Failed to remove public key.")
			return
		}

		if rkey != "" {
			// remove from pds too
			_, err := comatproto.RepoDeleteRecord(r.Context(), client, &comatproto.RepoDeleteRecord_Input{
				Collection: tangled.PublicKeyNSID,
				Repo:       did,
				Rkey:       rkey,
			})

			// invalid record
			if err != nil {
				log.Printf("failed to delete record from PDS: %s", err)
				s.pages.Notice(w, "settings-keys", "Failed to remove key from PDS.")
				return
			}
		}
		log.Println("deleted successfully")

		s.pages.HxLocation(w, "/settings")
		return
	}
}
