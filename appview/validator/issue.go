package validator

import (
	"fmt"
	"strings"

	"tangled.sh/tangled.sh/core/appview/db"
	"tangled.sh/tangled.sh/core/appview/pages/markup"
)

func (v *Validator) ValidateIssueComment(comment *db.IssueComment) error {
	// if comments have parents, only ingest ones that are 1 level deep
	if comment.ReplyTo != nil {
		parents, err := db.GetIssueComments(v.db, db.FilterEq("at_uri", *comment.ReplyTo))
		if err != nil {
			return fmt.Errorf("failed to fetch parent comment: %w", err)
		}
		if len(parents) != 1 {
			return fmt.Errorf("incorrect number of parent comments returned: %d", len(parents))
		}

		// depth check
		parent := parents[0]
		if parent.ReplyTo != nil {
			return fmt.Errorf("incorrect depth, this comment is replying at depth >1")
		}
	}

	sanitizer := markup.NewSanitizer()
	if sb := strings.TrimSpace(sanitizer.SanitizeDefault(comment.Body)); sb == "" {
		return fmt.Errorf("body is empty after HTML sanitization")
	}

	return nil
}
