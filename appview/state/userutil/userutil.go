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

	return strings.Replace(s, "-", ":", 2)
}

// IsFlattenedDid checks if the given string is a flattened DID.
func IsFlattenedDid(s string) bool {
	// Check if the string starts with "did-"
	if !strings.HasPrefix(s, "did-") {
		return false
	}

	// Reconstruct as a standard DID format using Replace
	// Example: "did-plc-xyz-abc" becomes "did:plc:xyz-abc"
	reconstructed := strings.Replace(s, "-", ":", 2)
	re := regexp.MustCompile(`^did:[a-z]+:[a-zA-Z0-9._:%-]*[a-zA-Z0-9._-]$`)

	return re.MatchString(reconstructed)
}

// FlattenDid converts a DID to a flattened format.
// A flattened DID is a DID with the :s swapped to -s to satisfy certain
// application requirements, such as Go module naming conventions.
func FlattenDid(s string) string {
	if IsDid(s) {
		return strings.Replace(s, ":", "-", 2)
	}
	return s
}

// IsDid checks if the given string is a standard DID.
func IsDid(s string) bool {
	re := regexp.MustCompile(`^did:[a-z]+:[a-zA-Z0-9._:%-]*[a-zA-Z0-9._-]$`)
	return re.MatchString(s)
}
