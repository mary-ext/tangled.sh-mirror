package validator

import (
	"fmt"
	"strings"

	"tangled.org/core/patchutil"
)

func (v *Validator) ValidatePatch(patch *string) error {
	if patch == nil || *patch == "" {
		return fmt.Errorf("patch is empty")
	}

	// add newline if not present to diff style patches
	if !patchutil.IsFormatPatch(*patch) && !strings.HasSuffix(*patch, "\n") {
		*patch = *patch + "\n"
	}

	if err := patchutil.IsPatchValid(*patch); err != nil {
		return err
	}

	return nil
}
