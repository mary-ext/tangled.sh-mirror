package models

import "time"

type InflightSignup struct {
	Id         int64
	Email      string
	InviteCode string
	Created    time.Time
}
