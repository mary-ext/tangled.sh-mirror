package models

import (
	"time"
)

type Follow struct {
	UserDid    string
	SubjectDid string
	FollowedAt time.Time
	Rkey       string
}

type FollowStats struct {
	Followers int64
	Following int64
}

type FollowStatus int

const (
	IsNotFollowing FollowStatus = iota
	IsFollowing
	IsSelf
)

func (s FollowStatus) String() string {
	switch s {
	case IsNotFollowing:
		return "IsNotFollowing"
	case IsFollowing:
		return "IsFollowing"
	case IsSelf:
		return "IsSelf"
	default:
		return "IsNotFollowing"
	}
}
