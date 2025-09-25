package validator

import (
	"tangled.org/core/appview/db"
	"tangled.org/core/appview/pages/markup"
	"tangled.org/core/idresolver"
	"tangled.org/core/rbac"
)

type Validator struct {
	db        *db.DB
	sanitizer markup.Sanitizer
	resolver  *idresolver.Resolver
	enforcer  *rbac.Enforcer
}

func New(db *db.DB, res *idresolver.Resolver, enforcer *rbac.Enforcer) *Validator {
	return &Validator{
		db:        db,
		sanitizer: markup.NewSanitizer(),
		resolver:  res,
		enforcer:  enforcer,
	}
}
