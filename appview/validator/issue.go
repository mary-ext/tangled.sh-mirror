package validator

import (
	"fmt"
	"strings"

	"tangled.sh/tangled.sh/core/appview/db"
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

	if sb := strings.TrimSpace(v.sanitizer.SanitizeDefault(comment.Body)); sb == "" {
		return fmt.Errorf("body is empty after HTML sanitization")
	}

	return nil
}

func (v *Validator) ValidateIssue(issue *db.Issue) error {
	if issue.Title == "" {
		return fmt.Errorf("issue title is empty")
	}

	if issue.Body == "" {
		return fmt.Errorf("issue body is empty")
	}

	if st := strings.TrimSpace(v.sanitizer.SanitizeDescription(issue.Title)); st == "" {
		return fmt.Errorf("title is empty after HTML sanitization")
	}

	if sb := strings.TrimSpace(v.sanitizer.SanitizeDefault(issue.Body)); sb == "" {
		return fmt.Errorf("body is empty after HTML sanitization")
	}

	return nil
}
