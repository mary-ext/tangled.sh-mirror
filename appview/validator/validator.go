package validator

import (
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
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
