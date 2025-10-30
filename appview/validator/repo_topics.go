package validator

import (
	"fmt"
	"maps"
	"regexp"
	"slices"
	"strings"
)

const (
	maxTopicLen = 50
	maxTopics   = 20
)

var (
	topicRE = regexp.MustCompile(`\A[a-z0-9-]+\z`)
)

// ValidateRepoTopicStr parses and validates whitespace-separated topic string.
//
// Rules:
//   - topics are separated by whitespace
//   - each topic may contain lowercase letters, digits, and hyphens only
//   - each topic must be <= 50 characters long
//   - no more than 20 topics allowed
//   - duplicates are removed
func (v *Validator) ValidateRepoTopicStr(topicsStr string) ([]string, error) {
	topicsStr = strings.TrimSpace(topicsStr)
	if topicsStr == "" {
		return nil, nil
	}
	parts := strings.Fields(topicsStr)
	if len(parts) > maxTopics {
		return nil, fmt.Errorf("too many topics: %d (maximum %d)", len(parts), maxTopics)
	}

	topicSet := make(map[string]struct{})

	for _, t := range parts {
		if _, exists := topicSet[t]; exists {
			continue
		}
		if len(t) > maxTopicLen {
			return nil, fmt.Errorf("topic '%s' is too long (maximum %d characters)", t, maxTopics)
		}
		if !topicRE.MatchString(t) {
			return nil, fmt.Errorf("topic '%s' contains invalid characters (allowed: lowercase letters, digits, hyphens)", t)
		}
		topicSet[t] = struct{}{}
	}
	return slices.Collect(maps.Keys(topicSet)), nil
}
