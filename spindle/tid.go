package spindle

import "github.com/bluesky-social/indigo/atproto/syntax"

var TIDClock = syntax.NewTIDClock(0)

func TID() string {
	return TIDClock.Next().String()
}
