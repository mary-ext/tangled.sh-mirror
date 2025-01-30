package state

import (
	"log"
	"net/http"

	"github.com/go-chi/chi/v5"
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

		err = s.Auth.StoreSession(r, w, atSession)
		if err != nil {
			log.Printf("storing session: %s", err)
			return
		}

		log.Printf("successfully saved session for %s (%s)", atSession.Handle, atSession.Did)
		http.Redirect(w, r, "/", http.StatusSeeOther)
		return
	}
}

func (s *State) Router() http.Handler {
	r := chi.NewRouter()

	r.Post("/login", s.Login)

	return r
}
