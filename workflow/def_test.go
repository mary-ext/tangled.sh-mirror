package workflow

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestUnmarshalWorkflowWithBranch(t *testing.T) {
	yamlData := `
when:
  - event: ["push", "pull_request"]
    branch: ["main", "develop"]`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err, "YAML should unmarshal without error")

	assert.Len(t, wf.When, 1, "Should have one constraint")
	assert.ElementsMatch(t, []string{"main", "develop"}, wf.When[0].Branch)
	assert.ElementsMatch(t, []string{"push", "pull_request"}, wf.When[0].Event)

	assert.False(t, wf.CloneOpts.Skip, "Skip should default to false")
}

func TestUnmarshalCloneFalse(t *testing.T) {
	yamlData := `
when:
  - event: pull_request_close

clone:
  skip: true
`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err)

	assert.ElementsMatch(t, []string{"pull_request_close"}, wf.When[0].Event)

	assert.True(t, wf.CloneOpts.Skip, "Skip should be false")
}

func TestUnmarshalWorkflowWithTags(t *testing.T) {
	yamlData := `
when:
  - event: ["push"]
    tag: ["v*", "release-*"]`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err, "YAML should unmarshal without error")

	assert.Len(t, wf.When, 1, "Should have one constraint")
	assert.ElementsMatch(t, []string{"v*", "release-*"}, wf.When[0].Tag)
	assert.ElementsMatch(t, []string{"push"}, wf.When[0].Event)
}

func TestUnmarshalWorkflowWithBranchAndTag(t *testing.T) {
	yamlData := `
when:
  - event: ["push"]
    branch: ["main", "develop"]
    tag: ["v*"]`

	wf, err := FromFile("test.yml", []byte(yamlData))
	assert.NoError(t, err, "YAML should unmarshal without error")

	assert.Len(t, wf.When, 1, "Should have one constraint")
	assert.ElementsMatch(t, []string{"main", "develop"}, wf.When[0].Branch)
	assert.ElementsMatch(t, []string{"v*"}, wf.When[0].Tag)
}

func TestMatchesPattern(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		patterns []string
		expected bool
	}{
		{"exact match", "main", []string{"main"}, true},
		{"exact match in list", "develop", []string{"main", "develop"}, true},
		{"no match", "feature", []string{"main", "develop"}, false},
		{"wildcard prefix", "v1.0.0", []string{"v*"}, true},
		{"wildcard suffix", "release-1.0", []string{"*-1.0"}, true},
		{"wildcard middle", "feature-123-test", []string{"feature-*-test"}, true},
		{"double star prefix", "release-1.0.0", []string{"release-**"}, true},
		{"double star with slashes", "release/1.0/hotfix", []string{"release/**"}, true},
		{"double star matches multiple levels", "foo/bar/baz/qux", []string{"foo/**"}, true},
		{"double star no match", "feature/test", []string{"release/**"}, false},
		{"no patterns matches nothing", "anything", []string{}, false},
		{"pattern doesn't match", "v1.0.0", []string{"release-*"}, false},
		{"complex pattern", "release/v1.2.3", []string{"release/*"}, true},
		{"single star stops at slash", "release/1.0/hotfix", []string{"release/*"}, false},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := matchesPattern(tt.input, tt.patterns)
			assert.Equal(t, tt.expected, result, "matchesPattern(%q, %v) should be %v", tt.input, tt.patterns, tt.expected)
		})
	}
}

func TestConstraintMatchRef_Branches(t *testing.T) {
	tests := []struct {
		name       string
		constraint Constraint
		ref        string
		expected   bool
	}{
		{
			name:       "exact branch match",
			constraint: Constraint{Branch: []string{"main"}},
			ref:        "refs/heads/main",
			expected:   true,
		},
		{
			name:       "branch glob match",
			constraint: Constraint{Branch: []string{"feature-*"}},
			ref:        "refs/heads/feature-123",
			expected:   true,
		},
		{
			name:       "branch no match",
			constraint: Constraint{Branch: []string{"main"}},
			ref:        "refs/heads/develop",
			expected:   false,
		},
		{
			name:       "no constraints matches nothing",
			constraint: Constraint{},
			ref:        "refs/heads/anything",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := tt.constraint.MatchRef(tt.ref)
			assert.Equal(t, tt.expected, result, "MatchRef should return %v for ref %q", tt.expected, tt.ref)
		})
	}
}

func TestConstraintMatchRef_Tags(t *testing.T) {
	tests := []struct {
		name       string
		constraint Constraint
		ref        string
		expected   bool
	}{
		{
			name:       "exact tag match",
			constraint: Constraint{Tag: []string{"v1.0.0"}},
			ref:        "refs/tags/v1.0.0",
			expected:   true,
		},
		{
			name:       "tag glob match",
			constraint: Constraint{Tag: []string{"v*"}},
			ref:        "refs/tags/v1.2.3",
			expected:   true,
		},
		{
			name:       "tag glob with pattern",
			constraint: Constraint{Tag: []string{"release-*"}},
			ref:        "refs/tags/release-2024",
			expected:   true,
		},
		{
			name:       "tag no match",
			constraint: Constraint{Tag: []string{"v*"}},
			ref:        "refs/tags/release-1.0",
			expected:   false,
		},
		{
			name:       "tag not matched when only branch constraint",
			constraint: Constraint{Branch: []string{"main"}},
			ref:        "refs/tags/v1.0.0",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := tt.constraint.MatchRef(tt.ref)
			assert.Equal(t, tt.expected, result, "MatchRef should return %v for ref %q", tt.expected, tt.ref)
		})
	}
}

func TestConstraintMatchRef_Combined(t *testing.T) {
	tests := []struct {
		name       string
		constraint Constraint
		ref        string
		expected   bool
	}{
		{
			name:       "matches branch in combined constraint",
			constraint: Constraint{Branch: []string{"main"}, Tag: []string{"v*"}},
			ref:        "refs/heads/main",
			expected:   true,
		},
		{
			name:       "matches tag in combined constraint",
			constraint: Constraint{Branch: []string{"main"}, Tag: []string{"v*"}},
			ref:        "refs/tags/v1.0.0",
			expected:   true,
		},
		{
			name:       "no match in combined constraint",
			constraint: Constraint{Branch: []string{"main"}, Tag: []string{"v*"}},
			ref:        "refs/heads/develop",
			expected:   false,
		},
		{
			name:       "glob patterns in combined constraint - branch",
			constraint: Constraint{Branch: []string{"release-*"}, Tag: []string{"v*"}},
			ref:        "refs/heads/release-2024",
			expected:   true,
		},
		{
			name:       "glob patterns in combined constraint - tag",
			constraint: Constraint{Branch: []string{"release-*"}, Tag: []string{"v*"}},
			ref:        "refs/tags/v2.0.0",
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := tt.constraint.MatchRef(tt.ref)
			assert.Equal(t, tt.expected, result, "MatchRef should return %v for ref %q", tt.expected, tt.ref)
		})
	}
}

func TestConstraintMatchBranch_GlobPatterns(t *testing.T) {
	tests := []struct {
		name       string
		constraint Constraint
		branch     string
		expected   bool
	}{
		{
			name:       "exact match",
			constraint: Constraint{Branch: []string{"main"}},
			branch:     "main",
			expected:   true,
		},
		{
			name:       "glob match",
			constraint: Constraint{Branch: []string{"feature-*"}},
			branch:     "feature-123",
			expected:   true,
		},
		{
			name:       "no match",
			constraint: Constraint{Branch: []string{"main"}},
			branch:     "develop",
			expected:   false,
		},
		{
			name:       "multiple patterns with match",
			constraint: Constraint{Branch: []string{"main", "release-*"}},
			branch:     "release-1.0",
			expected:   true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := tt.constraint.MatchBranch(tt.branch)
			assert.Equal(t, tt.expected, result, "MatchBranch should return %v for branch %q", tt.expected, tt.branch)
		})
	}
}

func TestConstraintMatchTag_GlobPatterns(t *testing.T) {
	tests := []struct {
		name       string
		constraint Constraint
		tag        string
		expected   bool
	}{
		{
			name:       "exact match",
			constraint: Constraint{Tag: []string{"v1.0.0"}},
			tag:        "v1.0.0",
			expected:   true,
		},
		{
			name:       "glob match",
			constraint: Constraint{Tag: []string{"v*"}},
			tag:        "v2.3.4",
			expected:   true,
		},
		{
			name:       "no match",
			constraint: Constraint{Tag: []string{"v*"}},
			tag:        "release-1.0",
			expected:   false,
		},
		{
			name:       "multiple patterns with match",
			constraint: Constraint{Tag: []string{"v*", "release-*"}},
			tag:        "release-2024",
			expected:   true,
		},
		{
			name:       "empty tag list matches nothing",
			constraint: Constraint{Tag: []string{}},
			tag:        "v1.0.0",
			expected:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result, _ := tt.constraint.MatchTag(tt.tag)
			assert.Equal(t, tt.expected, result, "MatchTag should return %v for tag %q", tt.expected, tt.tag)
		})
	}
}
