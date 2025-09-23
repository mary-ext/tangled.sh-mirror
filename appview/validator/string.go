package validator

import (
	"errors"
	"fmt"
	"unicode/utf8"

	"tangled.org/core/appview/models"
)

func (v *Validator) ValidateString(s *models.String) error {
	var err error

	if utf8.RuneCountInString(s.Filename) > 140 {
		err = errors.Join(err, fmt.Errorf("filename too long"))
	}

	if utf8.RuneCountInString(s.Description) > 280 {
		err = errors.Join(err, fmt.Errorf("description too long"))
	}

	if len(s.Contents) == 0 {
		err = errors.Join(err, fmt.Errorf("contents is empty"))
	}

	return err
}
