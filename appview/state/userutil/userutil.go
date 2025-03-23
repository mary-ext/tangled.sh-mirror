package userutil

import (
	"regexp"
	"strings"
)

func IsHandleNoAt(s string) bool {
	// ref: https://atproto.com/specs/handle
	re := regexp.MustCompile(`^([a-zA-Z0-9]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?\.)+[a-zA-Z]([a-zA-Z0-9-]{0,61}[a-zA-Z0-9])?$`)
	return re.MatchString(s)
}

func UnflattenDid(s string) string {
	if !IsFlattenedDid(s) {
		return s
	}

	parts := strings.SplitN(s[4:], "-", 2) // Skip "did-" prefix and split on first "-"
	if len(parts) != 2 {
		return s
	}

	return "did:" + parts[0] + ":" + parts[1]
}

// IsFlattenedDid checks if the given string is a flattened DID.
func IsFlattenedDid(s string) bool {
	// Check if the string starts with "did-"
	if !strings.HasPrefix(s, "did-") {
		return false
	}

	// Split the string to extract method and identifier
	parts := strings.SplitN(s[4:], "-", 2) // Skip "did-" prefix and split on first "-"
	if len(parts) != 2 {
		return false
	}

	// Reconstruct as a standard DID format
	// Example: "did-plc-xyz-abc" becomes "did:plc:xyz-abc"
	reconstructed := "did:" + parts[0] + ":" + parts[1]
	re := regexp.MustCompile(`^did:[a-z]+:[a-zA-Z0-9._:%-]*[a-zA-Z0-9._-]$`)

	return re.MatchString(reconstructed)
}

// FlattenDid converts a DID to a flattened format.
// A flattened DID is a DID with the :s swapped to -s to satisfy certain
// application requirements, such as Go module naming conventions.
func FlattenDid(s string) string {
	if !IsFlattenedDid(s) {
		return s
	}

	parts := strings.SplitN(s[4:], ":", 2) // Skip "did:" prefix and split on first ":"
	if len(parts) != 2 {
		return s
	}

	return "did-" + parts[0] + "-" + parts[1]
}
