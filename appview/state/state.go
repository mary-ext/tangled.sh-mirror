package state

import (
	"crypto/hmac"
	"crypto/sha256"
	"encoding/hex"
	"log"
	"net/http"
	"net/url"
	"time"

	"github.com/go-chi/chi/v5"
	"github.com/icyphox/bild/appview"
	"github.com/icyphox/bild/appview/auth"
	"github.com/icyphox/bild/appview/db"
)

type State struct {
	Db   *db.DB
	Auth *auth.Auth
}

func Make() (*State, error) {
	db, err := db.Make("appview.db")
	if err != nil {
		return nil, err
	}

	auth, err := auth.Make()
	if err != nil {
		return nil, err
	}

	return &State{db, auth}, nil
}

func (s *State) Login(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	switch r.Method {
	case http.MethodGet:
		log.Println("unimplemented")
		return
	case http.MethodPost:
		username := r.FormValue("username")
		appPassword := r.FormValue("password")

		atSession, err := s.Auth.CreateInitialSession(ctx, username, appPassword)
		if err != nil {
			log.Printf("creating initial session: %s", err)
			return
		}
		sessionish := auth.CreateSessionWrapper{atSession}

		err = s.Auth.StoreSession(r, w, &sessionish)
		if err != nil {
			log.Printf("storing session: %s", err)
			return
		}

		log.Printf("successfully saved session for %s (%s)", atSession.Handle, atSession.Did)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
}

// requires auth
func (s *State) Key(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		// list open registrations under this did

		return
	case http.MethodPost:
		session, err := s.Auth.Store.Get(r, appview.SESSION_NAME)
		if err != nil || session.IsNew {
			log.Println("unauthorized attempt to generate registration key")
			http.Error(w, "Forbidden", http.StatusUnauthorized)
			return
		}

		did := session.Values[appview.SESSION_DID].(string)

		// check if domain is valid url, and strip extra bits down to just host
		domain := r.FormValue("domain")
		url, err := url.Parse(domain)
		if domain == "" || err != nil {
			http.Error(w, "Invalid form", http.StatusBadRequest)
			return
		}

		key, err := s.Db.GenerateRegistrationKey(url.Host, did)

		if err != nil {
			log.Println(err)
			http.Error(w, "unable to register this domain", http.StatusNotAcceptable)
			return
		}

		w.Write([]byte(key))
		return
	}
}

// create a signed request and check if a node responds to that
//
// we should also rate limit these checks to avoid ddosing knotservers
func (s *State) Check(w http.ResponseWriter, r *http.Request) {
	domain := r.FormValue("domain")
	if domain == "" {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	secret, err := s.Db.GetRegistrationKey(domain)
	if err != nil {
		log.Printf("no key found for domain %s: %s\n", domain, err)
		return
	}

	hmac := hmac.New(sha256.New, []byte(secret))
	signature := hex.EncodeToString(hmac.Sum(nil))

	// make a request do the knotserver with an empty body and above signature
	url, _ := url.Parse(domain)
	url = url.JoinPath("check")
	pingRequest, err := http.NewRequest("GET", url.String(), nil)
	if err != nil {
		log.Println("failed to create ping request for ", url.String())
		return
	}
	pingRequest.Header.Set("X-Signature", signature)

	client := &http.Client{
		Timeout: 5 * time.Second,
	}
	resp, err := client.Do(pingRequest)
	if err != nil {
		w.Write([]byte("no dice"))
		log.Println("domain was unreachable after 5 seconds")
		return
	}

	if resp.StatusCode != http.StatusOK {
		log.Println("status nok")
		w.Write([]byte("no dice"))
		return
	}
	w.Write([]byte("check success"))

	// mark as registered
	s.Db.Register(domain)

	return
}

func (s *State) Router() http.Handler {
	r := chi.NewRouter()

	r.Post("/login", s.Login)

	r.Route("/node", func(r chi.Router) {
		r.Post("/check", s.Check)

		r.Group(func(r chi.Router) {
			r.Use(AuthMiddleware(s))
			r.Post("/key", s.Key)
		})
	})

	return r
}
