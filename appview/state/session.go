package state

import (
	"context"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/gorilla/sessions"
	"github.com/sotangled/tangled/appview"
	"github.com/sotangled/tangled/appview/auth"
)

func (s *State) StartTokenRefresher(
	ctx context.Context,
	refreshInterval time.Duration,
	r *http.Request,
	w http.ResponseWriter,
	atSessionish auth.Sessionish,
	pdsEndpoint string,
) {
	go func() {
		ticker := time.NewTicker(refreshInterval)
		defer ticker.Stop()

		for {
			select {
			case <-ticker.C:
				err := s.auth.RefreshSession(ctx, r, w, atSessionish, pdsEndpoint)
				if err != nil {
					log.Printf("token refresh failed: %v", err)
				} else {
					log.Println("token refreshed successfully")
				}
			case <-ctx.Done():
				log.Println("stopping token refresher")
				return
			}
		}
	}()
}

// RestoreSessionIfNeeded checks if a session exists in the request and starts a
// token refresher if it doesn't have one running already.
func (s *State) RestoreSessionIfNeeded(r *http.Request, w http.ResponseWriter) error {
	var session *sessions.Session
	var err error
	session, err = s.auth.GetSession(r)
	if err != nil {
		fmt.Errorf("error getting session: %w", err)
	}

	did, ok := session.Values[appview.SessionDid].(string)
	if !ok {
		return fmt.Errorf("session did not contain a did")
	}
	sessionish := auth.ClientSessionish{Session: *session}
	pdsEndpoint := session.Values[appview.SessionPds].(string)

	// If no refresher is running for this session, start one
	if _, exists := s.sessionCancelFuncs[did]; !exists {
		sessionCtx, cancel := context.WithCancel(context.Background())
		s.sessionCancelFuncs[did] = cancel

		s.StartTokenRefresher(sessionCtx, auth.ExpiryDuration, r, w, &sessionish, pdsEndpoint)

		log.Printf("restored session refresher for %s", did)
	}

	return nil
}
