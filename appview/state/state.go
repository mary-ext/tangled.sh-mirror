package state

import (
	"encoding/json"
	"log"
	"net/http"

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
func (s *State) GenerateRegistrationKey(w http.ResponseWriter, r *http.Request) {
	session, err := s.Auth.Store.Get(r, appview.SESSION_NAME)
	if err != nil || session.IsNew {
		log.Println("unauthorized attempt to generate registration key")
		http.Error(w, "Forbidden", http.StatusUnauthorized)
		return
	}

	did := session.Values[appview.SESSION_DID].(string)
	domain := r.FormValue("domain")
	if domain == "" {
		http.Error(w, "Invalid form", http.StatusBadRequest)
		return
	}

	key, err := s.Db.GenerateRegistrationKey(domain, did)

	if err != nil {
		log.Println(err)
		http.Error(w, "unable to register this domain", http.StatusNotAcceptable)
		return
	}

	w.Write([]byte(key))
	return
}

type RegisterRequest struct {
	Domain string `json:"domain"`
	Secret string `json:"secret"`
}

func (s *State) Register(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		log.Println("unimplemented")
		return
	case http.MethodPost:
		var req RegisterRequest
		if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
			http.Error(w, "invalid request body", http.StatusBadRequest)
			return
		}

		domain := req.Domain
		secret := req.Secret

		log.Printf("Registered domain: %s with secret: %s", domain, secret)
	}
}

func (s *State) Router() http.Handler {
	r := chi.NewRouter()

	r.Post("/login", s.Login)
	r.Group(func(r chi.Router) {
		r.Use(AuthMiddleware(s))
		r.Post("/node/generate-key", s.GenerateRegistrationKey)
	})

	return r
}
