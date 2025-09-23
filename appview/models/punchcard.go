package models

import "time"

type Punch struct {
	Did   string
	Date  time.Time
	Count int
}

type Punchcard struct {
	Total   int
	Punches []Punch
}
