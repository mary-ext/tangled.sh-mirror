package state

import (
	"fmt"
	"log"
	"net/http"
	"strings"

	"tangled.org/core/appview/pages"
)

func (s *State) Login(w http.ResponseWriter, r *http.Request) {
	switch r.Method {
	case http.MethodGet:
		returnURL := r.URL.Query().Get("return_url")
		s.pages.Login(w, pages.LoginParams{
			ReturnUrl: returnURL,
		})
	case http.MethodPost:
		handle := r.FormValue("handle")

		// when users copy their handle from bsky.app, it tends to have these characters around it:
		//
		// @nelind.dk:
		//   \u202a ensures that the handle is always rendered left to right and
		//   \u202c reverts that so the rest of the page renders however it should
		handle = strings.TrimPrefix(handle, "\u202a")
		handle = strings.TrimSuffix(handle, "\u202c")

		// `@` is harmless
		handle = strings.TrimPrefix(handle, "@")

		// basic handle validation
		if !strings.Contains(handle, ".") {
			log.Println("invalid handle format", "raw", handle)
			s.pages.Notice(
				w,
				"login-msg",
				fmt.Sprintf("\"%s\" is an invalid handle. Did you mean %s.bsky.social or %s.tngl.sh?", handle, handle, handle),
			)
			return
		}

		redirectURL, err := s.oauth.ClientApp.StartAuthFlow(r.Context(), handle)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}

		s.pages.HxRedirect(w, redirectURL)
	}
}

func (s *State) Logout(w http.ResponseWriter, r *http.Request) {
	err := s.oauth.DeleteSession(w, r)
	if err != nil {
		log.Println("failed to logout", "err", err)
	} else {
		log.Println("logged out successfully")
	}

	s.pages.HxRedirect(w, "/login")
}
