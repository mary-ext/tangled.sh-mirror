package models

import (
	"time"
)

type Email struct {
	ID               int64
	Did              string
	Address          string
	Verified         bool
	Primary          bool
	VerificationCode string
	LastSent         *time.Time
	CreatedAt        time.Time
}
