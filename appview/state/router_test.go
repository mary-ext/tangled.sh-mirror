package state

import "testing"

func TestUnflattenDid(t *testing.T) {
	unflattenedMap := map[string]string{
		"did-plc-abcdefghijklmnopqrstuvwxyz":                       "did:plc:abcdefghijklmnopqrstuvwxyz",
		"did-plc-1234567890":                                       "did:plc:1234567890",
		"did-key-z6MkhaXgBZDvotDkL5257faiztiGiC2QtKLGpbnnEGta2doK": "did:key:z6MkhaXgBZDvotDkL5257faiztiGiC2QtKLGpbnnEGta2doK",
		"did-plc-abcdefghi-jklmnopqr-stuvwxyz":                     "did:plc:abcdefghi-jklmnopqr-stuvwxyz",
		"plc-abcdefghijklmnopqrstuvwxyz":                           "plc-abcdefghijklmnopqrstuvwxyz",
		"didplc-abcdefghijklmnopqrstuvwxyz":                        "didplc-abcdefghijklmnopqrstuvwxyz",
		"":                                                         "",
		"did-":                                                     "did-",
		"did:plc:abcdefghijklmnopqrstuvwxyz":                       "did:plc:abcdefghijklmnopqrstuvwxyz",
		"did-invalid$format:something":                             "did-invalid$format:something",
	}

	tests := []struct {
		name     string
		input    string
		expected string
	}{}

	for _, tc := range isFlattenedDidTests {
		tests = append(tests, struct {
			name     string
			input    string
			expected string
		}{
			name:     tc.name,
			input:    tc.input,
			expected: unflattenedMap[tc.input],
		})
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			result := unflattenDid(tc.input)
			if result != tc.expected {
				t.Errorf("unflattenDid(%q) = %q, want %q", tc.input, result, tc.expected)
			}
		})
	}
}

var isFlattenedDidTests = []struct {
	name     string
	input    string
	expected bool
}{
	{
		name:     "valid flattened DID",
		input:    "did-plc-abcdefghijklmnopqrstuvwxyz",
		expected: true,
	},
	{
		name:     "valid flattened DID with numbers",
		input:    "did-plc-1234567890",
		expected: true,
	},
	{
		name:     "valid flattened DID with special characters",
		input:    "did-key-z6MkhaXgBZDvotDkL5257faiztiGiC2QtKLGpbnnEGta2doK",
		expected: true,
	},
	{
		name:     "valid flattened DID with dashes",
		input:    "did-plc-abcdefghi-jklmnopqr-stuvwxyz",
		expected: true,
	},

	{
		name:     "doesn't start with did-",
		input:    "plc-abcdefghijklmnopqrstuvwxyz",
		expected: false,
	},
	{
		name:     "no hyphen after did",
		input:    "didplc-abcdefghijklmnopqrstuvwxyz",
		expected: false,
	},
	{
		name:     "empty string",
		input:    "",
		expected: false,
	},
	{
		name:     "only did-",
		input:    "did-",
		expected: false,
	},
	{
		name:     "standard DID format, not flattened",
		input:    "did:plc:abcdefghijklmnopqrstuvwxyz",
		expected: false,
	},
	{
		name:     "invalid reconstructed DID format",
		input:    "did-invalid$format:something",
		expected: false,
	},
}

func TestIsFlattenedDid(t *testing.T) {
	for _, tc := range isFlattenedDidTests {
		t.Run(tc.name, func(t *testing.T) {
			result := isFlattenedDid(tc.input)
			if result != tc.expected {
				t.Errorf("isFlattenedDid(%q) = %v, want %v", tc.input, result, tc.expected)
			}
		})
	}
}
