package auth

import (
	comatproto "github.com/bluesky-social/indigo/api/atproto"
)

type AtSessionCreate struct {
	comatproto.ServerCreateSession_Output
	PDSEndpoint string
}

type AtSessionRefresh struct {
	comatproto.ServerRefreshSession_Output
	PDSEndpoint string
}
