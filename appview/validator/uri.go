package validator

import (
	"fmt"
	"net/url"
)

func (v *Validator) ValidateURI(uri string) error {
	parsed, err := url.Parse(uri)
	if err != nil {
		return fmt.Errorf("invalid uri format")
	}
	if parsed.Scheme == "" {
		return fmt.Errorf("uri scheme missing")
	}
	return nil
}
