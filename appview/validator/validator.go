package validator

import (
	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
	"tangled.sh/tangled.sh/core/idresolver"
)

type Validator struct {
	db        *db.DB
	sanitizer markup.Sanitizer
	resolver  *idresolver.Resolver
}

func New(db *db.DB, res *idresolver.Resolver) *Validator {
	return &Validator{
		db:        db,
		sanitizer: markup.NewSanitizer(),
		resolver:  res,
	}
}
