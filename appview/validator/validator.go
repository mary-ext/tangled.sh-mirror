package validator

import (
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/pages/markup"
)

type Validator struct {
	db        *db.DB
	sanitizer markup.Sanitizer
}

func New(db *db.DB) *Validator {
	return &Validator{
		db:        db,
		sanitizer: markup.NewSanitizer(),
	}
}
