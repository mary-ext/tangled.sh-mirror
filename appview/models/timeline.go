package models

import "time"

type TimelineEvent struct {
	*Repo
	*Follow
	*Star

	EventAt time.Time

	// optional: populate only if Repo is a fork
	Source *Repo

	// optional: populate only if event is Follow
	*Profile
	*FollowStats
	*FollowStatus

	// optional: populate only if event is Repo
	IsStarred bool
	StarCount int64
}
