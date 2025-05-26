package knotserver

import (
	"github.com/bluesky-social/indigo/atproto/syntax"
)

var c syntax.TIDClock = syntax.NewTIDClock(0)

func TID() string {
	return c.Next().String()
}
