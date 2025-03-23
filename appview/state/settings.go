package state

import (
	"database/sql"
	"errors"
	"fmt"
	"log"
	"net/http"
	"net/url"
	"strings"
	"time"

	comatproto "github.com/bluesky-social/indigo/api/atproto"
	lexutil "github.com/bluesky-social/indigo/lex/util"
	"github.com/gliderlabs/ssh"
	"github.com/google/uuid"
	"github.com/sotangled/tangled/api/tangled"
	"github.com/sotangled/tangled/appview/db"
	"github.com/sotangled/tangled/appview/email"
	"github.com/sotangled/tangled/appview/pages"
)

func (s *State) Settings(w http.ResponseWriter, r *http.Request) {
	user := s.auth.GetUser(r)
	pubKeys, err := db.GetPublicKeys(s.db, user.Did)
	if err != nil {
		log.Println(err)
	}

	emails, err := db.GetAllEmails(s.db, user.Did)
	if err != nil {
		log.Println(err)
	}

	s.pages.Settings(w, pages.SettingsParams{
		LoggedInUser: user,
		PubKeys:      pubKeys,
		Emails:       emails,
	})
}

// buildVerificationEmail creates an email.Email struct for verification emails
func (s *State) buildVerificationEmail(emailAddr, did, code string) email.Email {
	verifyURL := s.verifyUrl(did, emailAddr, code)

	return email.Email{
		APIKey:  s.config.ResendApiKey,
		From:    "noreply@notifs.tangled.sh",
		To:      emailAddr,
		Subject: "Verify your Tangled email",
		Text: `Click the link below (or copy and paste it into your browser) to verify your email address.
` + verifyURL,
		Html: `<p>Click the link (or copy and paste it into your browser) to verify your email address.</p>
<p><a href="` + verifyURL + `">` + verifyURL + `</a></p>`,
	}
}

// sendVerificationEmail handles the common logic for sending verification emails
func (s *State) sendVerificationEmail(w http.ResponseWriter, did, emailAddr, code string, errorContext string) error {
	emailToSend := s.buildVerificationEmail(emailAddr, did, code)

	err := email.SendEmail(emailToSend)
	if err != nil {
		log.Printf("sending email: %s", err)
		s.pages.Notice(w, "settings-emails-error", fmt.Sprintf("Unable to send verification email at this moment, try again later. %s", errorContext))
		return err
	}

	return nil
}

func (s *State) SettingsEmails(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		s.pages.Notice(w, "settings-emails", "Unimplemented.")
		log.Println("unimplemented")
		return
	case http.MethodPut:
		did := s.auth.GetDid(r)
		emAddr := r.FormValue("email")
		emAddr = strings.TrimSpace(emAddr)

		if !email.IsValidEmail(emAddr) {
			s.pages.Notice(w, "settings-emails-error", "Invalid email address.")
			return
		}

		// check if email already exists in database
		existingEmail, err := db.GetEmail(s.db, did, emAddr)
		if err != nil && !errors.Is(err, sql.ErrNoRows) {
			log.Printf("checking for existing email: %s", err)
			s.pages.Notice(w, "settings-emails-error", "Unable to add email at this moment, try again later.")
			return
		}

		if err == nil {
			if existingEmail.Verified {
				s.pages.Notice(w, "settings-emails-error", "This email is already verified.")
				return
			}

			s.pages.Notice(w, "settings-emails-error", "This email is already added but not verified. Check your inbox for the verification link.")
			return
		}

		code := uuid.New().String()

		// Begin transaction
		tx, err := s.db.Begin()
		if err != nil {
			log.Printf("failed to start transaction: %s", err)
			s.pages.Notice(w, "settings-emails-error", "Unable to add email at this moment, try again later.")
			return
		}
		defer tx.Rollback()

		if err := db.AddEmail(tx, db.Email{
			Did:              did,
			Address:          emAddr,
			Verified:         false,
			VerificationCode: code,
		}); err != nil {
			log.Printf("adding email: %s", err)
			s.pages.Notice(w, "settings-emails-error", "Unable to add email at this moment, try again later.")
			return
		}

		if err := s.sendVerificationEmail(w, did, emAddr, code, ""); err != nil {
			return
		}

		// Commit transaction
		if err := tx.Commit(); err != nil {
			log.Printf("failed to commit transaction: %s", err)
			s.pages.Notice(w, "settings-emails-error", "Unable to add email at this moment, try again later.")
			return
		}

		s.pages.Notice(w, "settings-emails-success", "Click the link in the email we sent you to verify your email address.")
		return
	case http.MethodDelete:
		did := s.auth.GetDid(r)
		emailAddr := r.FormValue("email")
		emailAddr = strings.TrimSpace(emailAddr)

		// Begin transaction
		tx, err := s.db.Begin()
		if err != nil {
			log.Printf("failed to start transaction: %s", err)
			s.pages.Notice(w, "settings-emails-error", "Unable to delete email at this moment, try again later.")
			return
		}
		defer tx.Rollback()

		if err := db.DeleteEmail(tx, did, emailAddr); err != nil {
			log.Printf("deleting email: %s", err)
			s.pages.Notice(w, "settings-emails-error", "Unable to delete email at this moment, try again later.")
			return
		}

		// Commit transaction
		if err := tx.Commit(); err != nil {
			log.Printf("failed to commit transaction: %s", err)
			s.pages.Notice(w, "settings-emails-error", "Unable to delete email at this moment, try again later.")
			return
		}

		s.pages.HxLocation(w, "/settings")
		return
	}
}

func (s *State) verifyUrl(did string, email string, code string) string {
	var appUrl string
	if s.config.Dev {
		appUrl = "http://" + s.config.ListenAddr
	} else {
		appUrl = "https://tangled.sh"
	}

	return fmt.Sprintf("%s/settings/emails/verify?did=%s&email=%s&code=%s", appUrl, url.QueryEscape(did), url.QueryEscape(email), url.QueryEscape(code))
}

func (s *State) SettingsEmailsVerify(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()

	// Get the parameters directly from the query
	emailAddr := q.Get("email")
	did := q.Get("did")
	code := q.Get("code")

	valid, err := db.CheckValidVerificationCode(s.db, did, emailAddr, code)
	if err != nil {
		log.Printf("checking email verification: %s", err)
		s.pages.Notice(w, "settings-emails-error", "Error verifying email. Please try again later.")
		return
	}

	if !valid {
		s.pages.Notice(w, "settings-emails-error", "Invalid verification code. Please request a new verification email.")
		return
	}

	// Mark email as verified in the database
	if err := db.MarkEmailVerified(s.db, did, emailAddr); err != nil {
		log.Printf("marking email as verified: %s", err)
		s.pages.Notice(w, "settings-emails-error", "Error updating email verification status. Please try again later.")
		return
	}

	http.Redirect(w, r, "/settings", http.StatusSeeOther)
}

func (s *State) SettingsEmailsVerifyResend(w http.ResponseWriter, r *http.Request) {
	if r.Method != http.MethodPost {
		s.pages.Notice(w, "settings-emails-error", "Invalid request method.")
		return
	}

	did := s.auth.GetDid(r)
	emAddr := r.FormValue("email")
	emAddr = strings.TrimSpace(emAddr)

	if !email.IsValidEmail(emAddr) {
		s.pages.Notice(w, "settings-emails-error", "Invalid email address.")
		return
	}

	// Check if email exists and is unverified
	existingEmail, err := db.GetEmail(s.db, did, emAddr)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			s.pages.Notice(w, "settings-emails-error", "Email not found. Please add it first.")
		} else {
			log.Printf("checking for existing email: %s", err)
			s.pages.Notice(w, "settings-emails-error", "Unable to resend verification email at this moment, try again later.")
		}
		return
	}

	if existingEmail.Verified {
		s.pages.Notice(w, "settings-emails-error", "This email is already verified.")
		return
	}

	// Check if last verification email was sent less than 10 minutes ago
	if existingEmail.LastSent != nil {
		timeSinceLastSent := time.Since(*existingEmail.LastSent)
		if timeSinceLastSent < 10*time.Minute {
			waitTime := 10*time.Minute - timeSinceLastSent
			s.pages.Notice(w, "settings-emails-error", fmt.Sprintf("Please wait %d minutes before requesting another verification email.", int(waitTime.Minutes()+1)))
			return
		}
	}

	// Generate new verification code
	code := uuid.New().String()

	// Begin transaction
	tx, err := s.db.Begin()
	if err != nil {
		log.Printf("failed to start transaction: %s", err)
		s.pages.Notice(w, "settings-emails-error", "Unable to resend verification email at this moment, try again later.")
		return
	}
	defer tx.Rollback()

	// Update the verification code and last sent time
	if err := db.UpdateVerificationCode(tx, did, emAddr, code); err != nil {
		log.Printf("updating email verification: %s", err)
		s.pages.Notice(w, "settings-emails-error", "Unable to resend verification email at this moment, try again later.")
		return
	}

	// Send verification email
	if err := s.sendVerificationEmail(w, did, emAddr, code, ""); err != nil {
		return
	}

	// Commit transaction
	if err := tx.Commit(); err != nil {
		log.Printf("failed to commit transaction: %s", err)
		s.pages.Notice(w, "settings-emails-error", "Unable to resend verification email at this moment, try again later.")
		return
	}

	s.pages.Notice(w, "settings-emails-success", "Verification email resent. Click the link in the email we sent you to verify your email address.")
}

func (s *State) SettingsEmailsPrimary(w http.ResponseWriter, r *http.Request) {
	did := s.auth.GetDid(r)
	emailAddr := r.FormValue("email")
	emailAddr = strings.TrimSpace(emailAddr)

	if emailAddr == "" {
		s.pages.Notice(w, "settings-emails-error", "Email address cannot be empty.")
		return
	}

	if err := db.MakeEmailPrimary(s.db, did, emailAddr); err != nil {
		log.Printf("setting primary email: %s", err)
		s.pages.Notice(w, "settings-emails-error", "Error setting primary email. Please try again later.")
		return
	}

	s.pages.HxLocation(w, "/settings")
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
