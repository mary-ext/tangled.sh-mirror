package models

import "time"

// Registration represents a knot registration. Knot would've been a better
// name but we're stuck with this for historical reasons.
type Registration struct {
	Id           int64
	Domain       string
	ByDid        string
	Created      *time.Time
	Registered   *time.Time
	NeedsUpgrade bool
}

func (r *Registration) Status() Status {
	if r.NeedsUpgrade {
		return NeedsUpgrade
	} else if r.Registered != nil {
		return Registered
	} else {
		return Pending
	}
}

func (r *Registration) IsRegistered() bool {
	return r.Status() == Registered
}

func (r *Registration) IsNeedsUpgrade() bool {
	return r.Status() == NeedsUpgrade
}

func (r *Registration) IsPending() bool {
	return r.Status() == Pending
}

type Status uint32

const (
	Registered Status = iota
	Pending
	NeedsUpgrade
)
