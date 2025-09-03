package validator

import "tangled.sh/tangled.sh/core/appview/db"

type Validator struct {
	db *db.DB
}

func New(db *db.DB) *Validator {
	return &Validator{
		db: db,
	}
}
