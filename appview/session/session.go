package session

import (
	"github.com/bluesky-social/indigo/atproto/auth/oauth"
	toauth "tangled.org/core/appview/oauth"
)

// Session is a lightweight wrapper over indigo-oauth ClientSession
type Session struct {
	*oauth.ClientSession
}

func New(atSess *oauth.ClientSession) Session {
	return Session{
		atSess,
	}
}

func (s *Session) User() *toauth.User {
	return &toauth.User{
		Did: string(s.Data.AccountDID),
		Pds: s.Data.HostURL,
	}
}
